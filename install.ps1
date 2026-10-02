# install.ps1 - install Code Goblins.
#
# To use it, from any PowerShell window, with no clone and no Go:
#
#   irm https://github.com/fpresta0607/code-goblins/releases/latest/download/install.ps1 | iex
#
# runs this script as published with the latest release, which downloads that
# same release's cfo.exe, refuses it unless it matches the release's
# SHA256SUMS, lets it set up the CFO home at %LOCALAPPDATA%\CodeGoblins with
# cfo.exe and goblins.exe on your PATH, this window included, asks once for
# the folder that holds your projects, installs the tools, skills and hooks
# the fleet needs, adds Code Goblins to the Start menu, runs goblins doctor,
# and ends with the quick start in this window, which never opens the board
# on its own.
#
# Releases are code-signed from the first signed release on; earlier ones
# are not. The one-line install runs cfo.exe only when it matches the
# release's SHA256SUMS and, for a signed release, carries a valid signature
# from its publisher, and it shows no SmartScreen prompt. A cfo.exe saved
# from a browser gets SmartScreen's "Windows protected your PC" while it is
# unsigned or its certificate is still new, and Smart App Control, where it
# is on, blocks an unsigned one. "On a fresh PC" in docs/install.md says how
# to check the checksum by hand and what to do if Microsoft Defender flags
# it.
#
# To work on it, in a clone:
#
#   .\install.cmd -Dev
#
# builds cfo.exe and goblins.exe from the clone, makes the clone your CFO home
# with both on your PATH, and does everything else the one-line install does.
# It refuses while another CFO home is in use; run goblins uninstall from that
# home first. install.cmd runs this script whatever PowerShell execution
# policy is set locally; one set by Group Policy still applies.
# Every step is idempotent and safe to rerun.

# The body runs in a scope of its own: the one-line install runs inside the
# caller's session, which keeps its own variables and preferences and must
# never be closed by an exit. So the script has no param block, which would
# bind in that session; the block reads the script's arguments itself.
& {
    $ErrorActionPreference = "Stop"
    $Dev = $false
    $arguments = @($args[0])
    for ($i = 0; $i -lt $arguments.Count; $i++) {
        if ($arguments[$i] -eq "-Dev") {
            $Dev = $true
        }
        else {
            throw "Unknown argument '$($arguments[$i])'. In a clone of Code Goblins, run: .\install.cmd -Dev"
        }
    }

    $repo = "fpresta0607/code-goblins"

    # Windows PowerShell 5.1 may not offer TLS 1.2 unless asked, and its
    # progress bar slows every download to a crawl.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    $ProgressPreference = "SilentlyContinue"

    # CODE_GOBLINS_RELEASE_BASE points the download at another copy of the
    # release assets, such as a CI build; the checksum check applies the same.
    # The copy of this script published with a release names that release
    # here in place of latest, so the script and cfo.exe are always one
    # release's.
    $releaseBase = "https://github.com/$repo/releases/latest/download"
    if ($env:CODE_GOBLINS_RELEASE_BASE) {
        $releaseBase = $env:CODE_GOBLINS_RELEASE_BASE.TrimEnd("/")
    }

    # The copy of this script published with a release names the release's
    # publisher here, and it installs only programs carrying a valid signature
    # from that publisher. A copy that names none, such as a clone's run
    # against a CI build, checks the release's SHA256SUMS alone and says so.
    $releasePublisher = ""

    # Read-ReleaseChecksum returns the SHA256 that $Sums, a release's checksum
    # file in the format sha256sum writes, lists for $Name, or "" when it
    # lists none.
    function Read-ReleaseChecksum([string]$Sums, [string]$Name) {
        $expected = ""
        foreach ($line in Get-Content -LiteralPath $Sums) {
            $fields = @($line.Trim() -split "\s+")
            if ($fields.Count -eq 2 -and $fields[1].TrimStart("*") -eq $Name) {
                $expected = $fields[0]
            }
        }
        return $expected
    }

    # Save-VerifiedRelease downloads the release's cfo.exe to $Path and keeps it
    # only when it matches the release's SHA256SUMS and, where this script
    # names the release's publisher, is validly signed by it. It returns $false
    # when the release cannot be downloaded at all, and throws on a mismatch,
    # leaving nothing at $Path.
    function Save-VerifiedRelease([string]$Path) {
        $download = "$Path.download"
        $sums = "$Path.SHA256SUMS"
        try {
            try {
                Write-Host "Downloading cfo.exe from $releaseBase ..."
                Invoke-WebRequest -Uri "$releaseBase/cfo.exe" -OutFile $download -UseBasicParsing
                Invoke-WebRequest -Uri "$releaseBase/SHA256SUMS" -OutFile $sums -UseBasicParsing
            }
            catch {
                Write-Host "No release could be downloaded: $($_.Exception.Message)"
                return $false
            }
            $expected = Read-ReleaseChecksum $sums "cfo.exe"
            $actual = (Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash
            if (-not $expected -or $actual -ne $expected) {
                # The hashes go on a line of their own: PowerShell 7 wraps a
                # long error across its error view.
                Write-Host "SHA256 of the download: $actual; the release's SHA256SUMS lists: '$expected'"
                throw "The downloaded cfo.exe does not match the release's SHA256SUMS, so it was not installed."
            }
            $signed = ""
            if ($releasePublisher) {
                $signature = Get-AuthenticodeSignature -LiteralPath $download
                $signer = ""
                if ($signature.SignerCertificate) {
                    $signer = $signature.SignerCertificate.GetNameInfo("SimpleName", $false)
                }
                if ($signature.Status -ne "Valid" -or $signer -ne $releasePublisher) {
                    Write-Host "Signature of the download: $($signature.Status), by '$signer'; the release is signed by '$releasePublisher'"
                    throw "The downloaded cfo.exe is not validly signed by $releasePublisher, so it was not installed."
                }
                $signed = " and its signature by $releasePublisher"
            }
            else {
                Write-Host "This copy of the install script names no publisher, so the download is checked against the release's SHA256SUMS only."
            }
            Move-Item -LiteralPath $download -Destination $Path -Force
            Write-Host "Verified cfo.exe against the release's SHA256SUMS ($actual)$signed."
            return $true
        }
        finally {
            Remove-Item -LiteralPath $download, $sums -Force -ErrorAction SilentlyContinue
        }
    }

    # Read-ProjectsRoot asks once for the folder that holds the user's
    # checkouts and returns cfo install's argument for it. A recorded folder is
    # kept on every rerun.
    function Read-ProjectsRoot {
        if ([Environment]::GetEnvironmentVariable("CFO_PROJECTS_ROOT", "User")) {
            return @()
        }
        if ([Console]::IsInputRedirected) {
            Write-Host "No projects folder recorded; record one later with: goblins install --projects-root <dir>"
            return @()
        }
        while ($true) {
            $answer = (Read-Host "Folder that holds your project checkouts, so the CFO can find them by name (Enter to skip)").Trim().Trim('"')
            if (-not $answer) {
                return @()
            }
            if (Test-Path -LiteralPath $answer -PathType Container) {
                return @("--projects-root", (Resolve-Path -LiteralPath $answer).ProviderPath)
            }
            Write-Host "$answer is not a folder."
        }
    }

    # A clone is the folder this script sits in when it is a code-goblins
    # checkout; the one-line install has no such folder.
    $scriptFolder = ""
    if ($PSCommandPath) {
        $scriptFolder = Split-Path -Parent $PSCommandPath
    }
    $fromClone = $scriptFolder -and (Test-Path (Join-Path $scriptFolder "AGENTS.md")) -and (Test-Path (Join-Path $scriptFolder "cmd\cfo"))
    if ($fromClone -and -not $Dev) {
        throw "This is a clone of Code Goblins. To build it and make it your CFO home, run: .\install.cmd -Dev"
    }
    if ($Dev -and -not $fromClone) {
        throw "-Dev builds Code Goblins from a clone. Clone it, then run .\install.cmd -Dev in the clone."
    }

    # winget installs git and gh. An install that would need it and cannot
    # have it stops here, before anything is downloaded or changed, with the
    # one fix to make on a line of its own.
    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
        $needed = @("git", "gh" | Where-Object { -not (Get-Command $_ -ErrorAction SilentlyContinue) })
        if ($needed.Count -gt 0) {
            Write-Host "Install App Installer from the Microsoft Store (https://apps.microsoft.com/detail/9NBLGGH4NNS1) for winget, then run this again."
            throw "winget is missing, and the install needs it for $($needed -join ' and ')."
        }
    }

    if ($Dev) {
        $InstallDir = $scriptFolder
        if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
            Write-Host "Install Go with: winget install -e --id GoLang.Go, then open a new terminal and run this again."
            throw "-Dev builds cfo.exe from this clone, which needs Go."
        }

        # A clone bootstrapped before the Lavish ruling still has the retired review
        # surface binary here, where nothing builds it and nothing ignores it any
        # more; left alone it sits untracked forever and can be committed by accident.
        $retiredSurface = Join-Path $InstallDir "showcase-axi.exe"
        if (Test-Path $retiredSurface) {
            Remove-Item $retiredSurface -Force -ErrorAction SilentlyContinue
            if (Test-Path $retiredSurface) {
                Write-Host "WARN     retired review-surface binary still present; delete $retiredSurface by hand"
            }
            else {
                Write-Host "Removed the retired review-surface binary -> $retiredSurface"
            }
        }

        Write-Host "Building cfo.exe from $InstallDir ..."
        $built = Join-Path $InstallDir "cfo.exe.new"
        Push-Location -LiteralPath $InstallDir
        try {
            go build -trimpath -o $built ./cmd/cfo
            if ($LASTEXITCODE -ne 0) { throw "go build failed" }
        }
        finally {
            Pop-Location
        }
        # cfo.exe and goblins.exe are one program under two names. A build
        # still running from here, such as a supervisor or a terminal's host,
        # cannot be overwritten but can be renamed, so each old copy moves
        # aside under a name of its own and goes once nothing runs it, on
        # this run or a later one.
        foreach ($name in "cfo.exe", "goblins.exe") {
            $target = Join-Path $InstallDir $name
            $aside = $null
            if (Test-Path -LiteralPath $target) {
                $aside = "$target.$([Guid]::NewGuid().ToString("N")).old"
                Move-Item -LiteralPath $target -Destination $aside
            }
            try {
                Copy-Item -LiteralPath $built -Destination $target
            }
            catch {
                if ($aside) { Move-Item -LiteralPath $aside -Destination $target -Force }
                throw
            }
            Get-ChildItem -LiteralPath $InstallDir -Filter "$name.*.old" | Remove-Item -Force -ErrorAction SilentlyContinue
        }
        Remove-Item -LiteralPath $built -Force
        $dest = Join-Path $InstallDir "cfo.exe"
        Write-Host "Built cfo.exe and goblins.exe -> $InstallDir"

        # From the clone: run there, cfo install makes the clone the CFO home.
        $projectsRoot = Read-ProjectsRoot
        Push-Location -LiteralPath $InstallDir
        try {
            & $dest install @projectsRoot
            if ($LASTEXITCODE -ne 0) { throw "cfo install exited with code $LASTEXITCODE" }
        }
        finally {
            Pop-Location
        }
    }
    else {
        $download = Join-Path ([IO.Path]::GetTempPath()) ("code-goblins-" + [Guid]::NewGuid().ToString("N"))
        New-Item -ItemType Directory -Path $download | Out-Null
        try {
            $downloaded = Join-Path $download "cfo.exe"
            if (-not (Save-VerifiedRelease $downloaded)) {
                throw "Code Goblins was not installed: the release could not be downloaded from $releaseBase."
            }

            $projectsRoot = Read-ProjectsRoot
            # From a neutral folder: run inside a checkout, cfo install would
            # wire that checkout instead of setting up the per-user home.
            Push-Location -LiteralPath $download
            try {
                & $downloaded install @projectsRoot
                if ($LASTEXITCODE -ne 0) { throw "cfo install exited with code $LASTEXITCODE" }
            }
            finally {
                Pop-Location
            }
        }
        finally {
            Remove-Item -LiteralPath $download -Recurse -Force -ErrorAction SilentlyContinue
        }

        $InstallDir = Join-Path $env:LOCALAPPDATA "CodeGoblins"
        $dest = Join-Path $InstallDir "goblins.exe"
    }
    # cfo install set both at user scope; this session needs them now.
    $env:CFO_HOME = $InstallDir
    $env:Path = "$InstallDir;$env:Path"

    # From here on, native stderr (npm progress, installer notes, mklink) must
    # not abort the install. Real failures are detected explicitly via exit
    # codes and existence checks instead.
    $ErrorActionPreference = "Continue"

    # Claude reads project skills only from .claude/skills, so a junction points it
    # at .agents/skills; codex, pi and kimi read .agents/skills directly, and a
    # .codex/skills link would only give codex a second route to the same skills,
    # so one an earlier install made is removed.
    # A junction keeps one copy tracked in git (no developer-mode symlinks).
    function Ensure-SkillJunctions {
        param([string]$Root)
        $source = Join-Path $Root ".agents\skills"
        if (-not (Test-Path $source)) {
            Write-Host "WARN     skills           .agents\skills not found; skipping skill junctions"
            return
        }
        foreach ($rel in @(".claude\skills")) {
            $link = Join-Path $Root $rel
            if (Test-Path $link) {
                $item = Get-Item $link -Force
                if ($item.LinkType -eq "Junction") {
                    Write-Host ("ok       {0,-20} skill junction present" -f $rel)
                }
                else {
                    Write-Host ("WARN     {0,-20} exists and is not a junction; leaving it alone" -f $rel)
                }
                continue
            }
            $parent = Split-Path -Parent $link
            if (-not (Test-Path $parent)) {
                New-Item -ItemType Directory -Path $parent -Force | Out-Null
            }
            $mklinkOut = cmd /c mklink /J `"$link`" `"$source`" 2>&1
            if (Test-Path $link) {
                Write-Host ("ok       {0,-20} skill junction created" -f $rel)
            }
            else {
                Write-Host ("WARN     {0,-20} could not create skill junction ({1}); run: cmd /c mklink /J {0} .agents\skills" -f $rel, ($mklinkOut -join "; "))
            }
        }
        $rel = ".codex\skills"
        $link = Join-Path $Root $rel
        if (Test-Path $link) {
            $item = Get-Item $link -Force
            if ($item.LinkType -eq "Junction" -and [IO.Path]::GetFullPath(@($item.Target)[0]).TrimEnd("\") -eq [IO.Path]::GetFullPath($source).TrimEnd("\")) {
                $rmdirOut = cmd /c rmdir `"$link`" 2>&1
                if (Test-Path $link) {
                    Write-Host ("WARN     {0,-20} could not remove stale skill junction ({1}); run: cmd /c rmdir {0}" -f $rel, ($rmdirOut -join "; "))
                }
                else {
                    Write-Host ("ok       {0,-20} stale skill junction removed" -f $rel)
                }
            }
            else {
                Write-Host ("WARN     {0,-20} exists and is not a junction to .agents\skills; leaving it alone" -f $rel)
            }
        }
    }

    # Add-UserPath puts $Folder on this session's PATH and on the user's, where
    # every new terminal finds it, and returns whether the user's PATH changed.
    # CFO_USER_ENV_FILE, where set, names the JSON file that stands in for the
    # user-scope environment, as it does for cfo install. The user PATH keeps
    # its registry kind, as cfo install keeps it, so its %VARIABLE% entries
    # survive.
    function Add-UserPath([string]$Folder) {
        if (-not (@($env:Path -split ';') -contains $Folder)) {
            $env:Path = "$env:Path;$Folder"
        }
        if ($env:CFO_USER_ENV_FILE) {
            $values = [ordered]@{}
            if (Test-Path -LiteralPath $env:CFO_USER_ENV_FILE) {
                foreach ($property in (Get-Content -Raw -LiteralPath $env:CFO_USER_ENV_FILE | ConvertFrom-Json).PSObject.Properties) {
                    $values[$property.Name] = $property.Value
                }
            }
            $entries = @([string]$values["Path"] -split ';' | Where-Object { $_ -ne "" })
            if ($entries -contains $Folder) {
                return $false
            }
            $values["Path"] = ($entries + $Folder) -join ';'
            [IO.File]::WriteAllText($env:CFO_USER_ENV_FILE, (ConvertTo-Json -InputObject $values))
            return $true
        }
        $environment = Get-Item -LiteralPath "HKCU:\Environment"
        $userPath = [string]$environment.GetValue("Path", "", "DoNotExpandEnvironmentNames")
        if (@($userPath -split ';') -contains $Folder) {
            return $false
        }
        $kind = if ($null -ne $environment.GetValue("Path", $null)) { $environment.GetValueKind("Path") } else { "ExpandString" }
        Set-ItemProperty -LiteralPath "HKCU:\Environment" -Name Path -Value ((@($userPath -split ';' | Where-Object { $_ -ne "" }) + $Folder) -join ';') -Type $kind
        # WM_SETTINGCHANGE makes Explorer, and every window it opens after
        # this, see the new PATH before the next sign-in.
        if (-not ("CfoInstall.Win32" -as [type])) {
            Add-Type -Namespace CfoInstall -Name Win32 -MemberDefinition @'
[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)]
public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);
'@
        }
        $result = [UIntPtr]::Zero
        [void][CfoInstall.Win32]::SendMessageTimeout([IntPtr]0xffff, 0x1A, [UIntPtr]::Zero, 'Environment', 2, 5000, [ref]$result)
        return $true
    }

    # Save-Download saves $Uri to $OutFile. A download that fails, as one does
    # on a rate limit or a 503, is tried again after a wait, three attempts in
    # all.
    function Save-Download([string]$Uri, [string]$OutFile) {
        for ($attempt = 1; ; $attempt++) {
            try {
                Invoke-WebRequest -Uri $Uri -OutFile $OutFile -UseBasicParsing -ErrorAction Stop
                return
            }
            catch {
                if ($attempt -eq 3) {
                    throw "$Uri could not be downloaded after 3 attempts: $($_.Exception.Message)"
                }
                Write-Host "Downloading $Uri failed: $($_.Exception.Message) Trying again in $(5 * $attempt) s ..."
                Start-Sleep -Seconds (5 * $attempt)
            }
        }
    }

    # no-mistakes gates every goblin's work, so the fleet runs the release
    # pinned here. It is downloaded from the release itself, never through
    # GitHub's API, which answers an anonymous caller 60 times an hour and
    # failed the install on shared CI runners. Moving the pin forward and
    # rerunning the install updates a machine to it.
    $noMistakesVersion = "1.75.1"

    # The one no-mistakes the install manages and ever replaces, where
    # no-mistakes' own installer and its update put it.
    $noMistakesProgram = Join-Path $env:LOCALAPPDATA "no-mistakes\no-mistakes.exe"

    # Read-NoMistakesVersion returns the version $Program reports, such as
    # 1.75.1, or "" when it reports none. Only stdout holds the version:
    # stderr may carry an update notice that names another.
    function Read-NoMistakesVersion([string]$Program) {
        if ((& $Program --version 2>$null | Out-String) -match '(?m)^no-mistakes version v(\d+\.\d+\.\d+)(\s|$)') {
            return $Matches[1]
        }
        return ""
    }

    # Install-NoMistakes installs the pinned no-mistakes release from $Release,
    # its download folder, as the managed copy, once the archive matches the
    # release's checksums.txt, and starts its daemon. An older managed copy's
    # daemon is stopped first; no-mistakes refuses that while a gate runs, and
    # the older copy then stays as it is.
    function Install-NoMistakes([string]$Release) {
        $arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
        $archive = "no-mistakes-v$noMistakesVersion-windows-$arch.zip"
        $target = $noMistakesProgram
        $folder = Split-Path -Parent $target
        $download = Join-Path ([IO.Path]::GetTempPath()) ("code-goblins-" + [Guid]::NewGuid().ToString("N"))
        New-Item -ItemType Directory -Path $download | Out-Null
        try {
            Save-Download "$Release/$archive" (Join-Path $download $archive)
            Save-Download "$Release/checksums.txt" (Join-Path $download "checksums.txt")
            $expected = Read-ReleaseChecksum (Join-Path $download "checksums.txt") $archive
            $actual = (Get-FileHash -LiteralPath (Join-Path $download $archive) -Algorithm SHA256).Hash
            if (-not $expected -or $actual -ne $expected) {
                Write-Host "SHA256 of the download: $actual; the release's checksums.txt lists: '$expected'"
                throw "The downloaded $archive does not match the release's checksums.txt, so it was not installed."
            }
            Write-Host "Verified $archive against the release's checksums.txt ($actual)."
            Expand-Archive -LiteralPath (Join-Path $download $archive) -DestinationPath $download -ErrorAction Stop
            $program = Join-Path $download "no-mistakes.exe"
            if (-not (Test-Path -LiteralPath $program -PathType Leaf)) {
                throw "$archive holds no no-mistakes.exe, so it was not installed."
            }

            # A no-mistakes command still running holds its program, which can
            # be renamed but not replaced, so it moves aside to
            # no-mistakes.exe.old, where no-mistakes' own update puts it and
            # removes it once nothing runs it. One left there from before goes
            # now, while the daemon still runs.
            if (Test-Path -LiteralPath "$target.old") {
                Remove-Item -LiteralPath "$target.old" -Force -ErrorAction Stop
            }
            if (Test-Path -LiteralPath $target) {
                & $target daemon stop
                if ($LASTEXITCODE -ne 0) {
                    throw "the installed no-mistakes stays as it is, since its daemon did not stop, which no-mistakes refuses while a gate runs; rerun the install once no gate runs"
                }
            }
            New-Item -ItemType Directory -Force -Path $folder | Out-Null
            if (Test-Path -LiteralPath $target) {
                Move-Item -LiteralPath $target -Destination "$target.old" -ErrorAction Stop
            }
            Move-Item -LiteralPath $program -Destination $target -ErrorAction Stop
        }
        finally {
            Remove-Item -LiteralPath $download -Recurse -Force -ErrorAction SilentlyContinue
        }
        if (Add-UserPath $folder) {
            Write-Host ("ok       {0,-20} {1} added to your PATH" -f "no-mistakes", $folder)
        }
        & $target daemon start
        if ($LASTEXITCODE -ne 0) {
            throw "no-mistakes v$noMistakesVersion is installed, but its daemon did not start; run: no-mistakes daemon start"
        }
    }

    # The toolchain mirrors `cfo doctor`; its hints are the source of truth for
    # where each tool comes from, except no-mistakes, which comes from the
    # release pinned above. npm is called as npm.cmd, which runs under any
    # execution policy, where the npm.ps1 shim is refused by the default one and
    # the one-line install cannot set it. Kind says how a missing tool gets
    # installed:
    #   winget     - a winget package (needs winget)
    #   npm        - a global npm package (needs npm, i.e. Node.js)
    #   npm-file   - a global npm package from a release file, which npm would
    #                install unchecked: the file is downloaded here and handed
    #                to npm only when it matches the SHA256 pinned beside it
    #   powershell - an official install.ps1, saved to a file and run from it
    #                in a child shell, so its own `exit` cannot kill this
    #                install. It is never run as a download-and-run one-liner
    #                (irm <url> | iex) on a child's command line, which
    #                Defender blocks as Trojan:Win32/Commando.A!ml.
    #   release    - the release pinned above, which Install-NoMistakes
    #                installs, and updates when an older one is present
    #   manual     - no scriptable installer; print the manual step instead
    $tools = @(
        @{ Name = "git";                 Kind = "winget";     Cmd = "winget install -e --id Git.Git --accept-package-agreements --accept-source-agreements" },
        @{ Name = "gh";                  Kind = "winget";     Cmd = "winget install -e --id GitHub.cli --accept-package-agreements --accept-source-agreements" },
        @{ Name = "claude";              Kind = "powershell"; Cmd = "https://claude.ai/install.ps1" },
        @{ Name = "herdr";               Kind = "powershell"; Cmd = "https://herdr.dev/install.ps1" },
        @{ Name = "codex";               Kind = "npm";        Cmd = "npm.cmd install -g @openai/codex" },
        @{ Name = "pi";                  Kind = "npm";        Cmd = "npm.cmd install -g @earendil-works/pi-coding-agent" },
        @{ Name = "kimi";                Kind = "manual";     Cmd = "install the Kimi Code CLI from https://www.kimi.com (no scriptable installer; sign in after)" },
        @{ Name = "tasks-axi";           Kind = "npm";        Cmd = "npm.cmd install -g tasks-axi" },
        @{ Name = "quota-axi";           Kind = "npm";        Cmd = "npm.cmd install -g quota-axi" },
        @{ Name = "no-mistakes";         Kind = "release";    Cmd = "https://github.com/kunchenguid/no-mistakes/releases/download/v$noMistakesVersion" },
        @{ Name = "gh-axi";              Kind = "npm";        Cmd = "npm.cmd install -g gh-axi" },
        @{ Name = "chrome-devtools-axi"; Kind = "npm";        Cmd = "npm.cmd install -g chrome-devtools-axi" },
        @{ Name = "lavish-axi";          Kind = "npm-file";   Cmd = "https://github.com/fpresta0607/lavish-axi/releases/download/v0.1.79-codegoblins.3/lavish-axi-0.1.79-codegoblins.3.tgz"; Sha256 = "C9D491112C4B971A957B7B72D7E72378E42ECA20BFB4A3E725E60E76C18BA9B9" }
    )

    $npmPresent = [bool](Get-Command npm -ErrorAction SilentlyContinue)
    $wingetPresent = [bool](Get-Command winget -ErrorAction SilentlyContinue)

    $manualSteps = @()
    $failedInstalls = @()
    $installedAny = $false
    foreach ($tool in $tools) {
        $found = Get-Command $tool.Name -ErrorAction SilentlyContinue
        # A native terminal starts Claude Code only as claude.exe, so a script
        # such as npm's claude.cmd does not count as present.
        if ($found -and $tool.Name -eq "claude" -and [IO.Path]::GetExtension($found.Source) -ne ".exe") {
            $found = $null
        }
        # The release is judged by the copy the install manages, whatever this
        # window's PATH holds: a window opened before that copy was installed
        # does not find it. An older copy that comes first on PATH from
        # anywhere else is never replaced, only named. The managed copy is
        # updated only when older than the pin; one that reports no version,
        # such as a build of its own, is left alone.
        if ($tool.Kind -eq "release") {
            $pathVersion = if ($found) { Read-NoMistakesVersion $found.Source } else { "" }
            if ($pathVersion -and $found.Source -ne $noMistakesProgram -and [version]$pathVersion -lt [version]$noMistakesVersion) {
                Write-Host ("WARN     {0,-20} {1} is v{2}, older than the pinned v{3}, and comes first on PATH; run: no-mistakes update, or remove the older copy" -f $tool.Name, $found.Source, $pathVersion, $noMistakesVersion)
                $failedInstalls += $tool.Name
                continue
            }
            if (Test-Path -LiteralPath $noMistakesProgram) {
                $managedVersion = Read-NoMistakesVersion $noMistakesProgram
                if ($managedVersion -and [version]$managedVersion -lt [version]$noMistakesVersion) {
                    Write-Host ("update   {0,-20} v{1} is older than the pinned v{2}" -f $tool.Name, $managedVersion, $noMistakesVersion)
                    $found = $null
                }
                else {
                    if (Add-UserPath (Split-Path -Parent $noMistakesProgram)) {
                        Write-Host ("ok       {0,-20} {1} added to your PATH" -f $tool.Name, (Split-Path -Parent $noMistakesProgram))
                    }
                    $found = Get-Command $noMistakesProgram
                }
            }
        }
        if ($found) {
            Write-Host ("ok       {0,-20} present" -f $tool.Name)
            continue
        }
        if ($tool.Kind -eq "manual") {
            Write-Host ("MANUAL   {0,-20} {1}" -f $tool.Name, $tool.Cmd)
            $manualSteps += $tool.Name
            continue
        }
        if (($tool.Kind -in "npm", "npm-file") -and -not $npmPresent) {
            Write-Host ("PREREQ   {0,-20} install Node.js first: winget install OpenJS.NodeJS.LTS" -f $tool.Name)
            $failedInstalls += $tool.Name
            continue
        }
        if ($tool.Kind -eq "winget" -and -not $wingetPresent) {
            Write-Host ("PREREQ   {0,-20} install winget first (ships with Windows App Installer)" -f $tool.Name)
            $failedInstalls += $tool.Name
            continue
        }
        Write-Host ("install  {0,-20} {1}" -f $tool.Name, $tool.Cmd)
        try {
            if ($tool.Kind -eq "release") {
                # Install-NoMistakes puts its folder on this session's PATH
                # itself, so the PATH needs no refresh for it.
                Install-NoMistakes $tool.Cmd
            }
            elseif ($tool.Kind -eq "powershell") {
                $installer = Join-Path ([IO.Path]::GetTempPath()) ("code-goblins-" + [Guid]::NewGuid().ToString("N") + ".ps1")
                try {
                    Invoke-WebRequest -Uri $tool.Cmd -OutFile $installer -UseBasicParsing -ErrorAction Stop
                    & powershell -NoProfile -ExecutionPolicy Bypass -File $installer
                    if ($LASTEXITCODE -ne 0) { throw "installer exited with code $LASTEXITCODE" }
                }
                finally {
                    Remove-Item -LiteralPath $installer -Force -ErrorAction SilentlyContinue
                }
                $installedAny = $true
            }
            elseif ($tool.Kind -eq "npm-file") {
                $download = Join-Path ([IO.Path]::GetTempPath()) ("code-goblins-" + [Guid]::NewGuid().ToString("N"))
                New-Item -ItemType Directory -Path $download | Out-Null
                try {
                    $file = Split-Path -Leaf $tool.Cmd
                    $saved = Join-Path $download $file
                    Save-Download $tool.Cmd $saved
                    $actual = (Get-FileHash -LiteralPath $saved -Algorithm SHA256).Hash
                    if ($actual -ne $tool.Sha256) {
                        Write-Host "SHA256 of the download: $actual; this install pins: $($tool.Sha256)"
                        throw "The downloaded $file does not match the SHA256 this install pins, so it was not installed."
                    }
                    Write-Host "Verified $file against the SHA256 this install pins ($actual)."
                    & npm.cmd install -g $saved
                    if ($LASTEXITCODE -ne 0) { throw "exited with code $LASTEXITCODE" }
                }
                finally {
                    Remove-Item -LiteralPath $download -Recurse -Force -ErrorAction SilentlyContinue
                }
                $installedAny = $true
            }
            else {
                Invoke-Expression $tool.Cmd
                if ($LASTEXITCODE -ne 0) { throw "exited with code $LASTEXITCODE" }
                $installedAny = $true
            }
            Write-Host ("ok       {0,-20} installed" -f $tool.Name)
        }
        catch {
            Write-Host ("WARN     {0,-20} install failed: {1}" -f $tool.Name, $_.Exception.Message)
            $failedInstalls += $tool.Name
        }
    }

    # Claude Code's native installer puts claude.exe, the build a native
    # terminal starts, in ~\.local\bin and may leave that off PATH.
    $claudeBin = Join-Path $env:USERPROFILE ".local\bin"
    if ((Test-Path -LiteralPath (Join-Path $claudeBin "claude.exe")) -and (Add-UserPath $claudeBin)) {
        Write-Host ("ok       {0,-20} {1} added to your PATH" -f "claude", $claudeBin)
        $installedAny = $true
    }

    # Installers write PATH entries to the registry; make them visible in this
    # shell (union with the current PATH, so nothing already present is lost).
    if ($installedAny) {
        Write-Host ""
        Write-Host "Refreshing PATH so newly installed tools are visible in this session ..."
        $parts = @($env:Path -split ';') + @([Environment]::GetEnvironmentVariable("Path", "Machine") -split ';') + @([Environment]::GetEnvironmentVariable("Path", "User") -split ';')
        $env:Path = ($parts | Where-Object { $_ -ne "" } | Select-Object -Unique) -join ';'
    }

    # The native build's folder is appended to PATH, so a script left earlier
    # on it, such as npm's claude.cmd, still wins until the user removes it.
    # Warn only once the native build is installed: until then the script is
    # the only working claude.
    $claude = Get-Command claude -ErrorAction SilentlyContinue
    if ($claude -and [IO.Path]::GetExtension($claude.Source) -ne ".exe" -and (Test-Path -LiteralPath (Join-Path $claudeBin "claude.exe"))) {
        Write-Host ("WARN     {0,-20} resolves to {1}, a script a native terminal cannot start; run: npm.cmd uninstall -g @anthropic-ai/claude-code" -f "claude", $claude.Source)
        $failedInstalls += "claude: npm.cmd uninstall -g @anthropic-ai/claude-code"
    }

    # Point Claude Code's project skills directory at the clone's .agents/skills.
    if ($Dev) {
        Write-Host ""
        Ensure-SkillJunctions -Root $InstallDir
    }

    # The tools the fleet drives publish their own skills, installed once at
    # user scope so every harness and every project sees them.
    Write-Host ""
    foreach ($skill in @("gh-axi", "chrome-devtools-axi", "no-mistakes")) {
        if (-not (Get-Command npx.cmd -ErrorAction SilentlyContinue)) {
            Write-Host ("PREREQ   {0,-20} skill needs Node.js: winget install OpenJS.NodeJS.LTS" -f $skill)
            $failedInstalls += "$skill skill"
            continue
        }
        # Only the fleet's harnesses, as copies: a symlink needs a right an
        # ordinary Windows user may not have.
        Write-Host ("skill    {0,-20} npx skills add kunchenguid/{0} --skill {0} -g -y -a claude-code -a codex -a pi --copy" -f $skill)
        & npx.cmd -y skills add "kunchenguid/$skill" --skill $skill -g -y -a claude-code -a codex -a pi --copy
        if ($LASTEXITCODE -ne 0) {
            Write-Host ("WARN     {0,-20} skill install exited with code {1}" -f $skill, $LASTEXITCODE)
            $failedInstalls += "$skill skill"
        }
    }
    # The board's native lifecycle hooks, for each harness installed here.
    foreach ($harness in @("claude", "codex", "pi")) {
        if (-not (Get-Command $harness -ErrorAction SilentlyContinue)) {
            continue
        }
        & $dest hooks install $harness
        if ($LASTEXITCODE -ne 0) {
            Write-Host ("WARN     {0,-20} native hooks were not installed; see the line above" -f $harness)
            $failedInstalls += "$harness hooks"
        }
    }

    # Code Goblins in the Start menu runs the quick start in a window of its
    # own: it starts the supervisor and the CFO when they are not running and
    # ends on a screen that offers the CFO's terminal and the board.
    $goblins = Join-Path $InstallDir "goblins.exe"
    $shortcutPath = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs\Code Goblins.lnk"
    try {
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $shortcutPath) -ErrorAction Stop | Out-Null
        $shortcut = (New-Object -ComObject WScript.Shell).CreateShortcut($shortcutPath)
        $shortcut.TargetPath = $goblins
        $shortcut.Arguments = ""
        $shortcut.WorkingDirectory = $InstallDir
        $shortcut.WindowStyle = 1
        $shortcut.Description = "Start Code Goblins"
        $shortcut.Save()
        Write-Host ("shortcut {0,-20} {1}" -f "Code Goblins", $shortcutPath)
    }
    catch {
        Write-Host ("WARN     {0,-20} the Start-menu shortcut was not made: {1}" -f "Code Goblins", $_.Exception.Message)
        $failedInstalls += "Start-menu shortcut"
    }

    Write-Host ""
    Write-Host "Verifying the toolchain ..."
    & $dest doctor
    $doctorExit = $LASTEXITCODE

    if ($manualSteps.Count -gt 0 -or $failedInstalls.Count -gt 0) {
        Write-Host ""
        if ($manualSteps.Count -gt 0) {
            Write-Host "Still needs a manual step:"
            foreach ($m in $manualSteps) {
                Write-Host "  - $m"
            }
        }
        if ($failedInstalls.Count -gt 0) {
            Write-Host "These installs did not complete; see the lines above:"
            foreach ($m in $failedInstalls) {
                Write-Host "  - $m"
            }
        }
    }

    Write-Host ""
    if ($Dev) {
        Write-Host "Code Goblins is built and installed from $InstallDir, which is your CFO home. Code Goblins in the Start menu starts it again; open a new terminal so cfo and goblins are on your PATH."
    }
    else {
        Write-Host "Code Goblins is installed in $InstallDir. Code Goblins in the Start menu starts it again, and goblins works in this window and in any new one."
    }

    # Last, the quick start in this window: it starts the supervisor, sets up
    # the agent the CFO runs on, starts the CFO and ends on a screen that
    # offers the CFO's terminal and the board. It never opens the board on
    # its own, and where nobody can answer it, as in a script, it accepts
    # nothing and says to run goblins in a terminal.
    Write-Host ""
    & $goblins
    if ($LASTEXITCODE -ne 0) {
        Write-Host ("NOTE     {0,-20} run goblins in a terminal to finish the quick start" -f "goblins")
    }
    if ($Dev) {
        exit $doctorExit
    }
} $args
