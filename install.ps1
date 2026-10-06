# install.ps1 - install Code Goblins.
#
# To use it, from any PowerShell window, with no clone and no Go:
#
#   irm https://github.com/fpresta0607/code-goblins/releases/latest/download/install.ps1 | iex
#
# runs this script as published with the latest release, the same install
# CodeGoblinsSetup.exe runs out of sight. In four steps it downloads that
# same release's cfo.exe and desktop app, checks them against the release's
# SHA256SUMS, lets cfo.exe set up the home with the app and the cfo command
# line together in its bin folder, on your PATH, this window included, and
# installs the tools, skills and hooks the fleet needs and Code Goblins in
# the Start menu, then opens the app. The home is the one already in use
# where CFO_HOME names one, kept where it is, and otherwise
# %LOCALAPPDATA%\CodeGoblins. It asks nothing, prints only those steps and
# a line for anything to know, and keeps every detail in its log.
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
# builds cfo.exe, goblins.exe and the desktop window goblins-window.exe from
# the clone and does everything else the one-line install does. A build from
# source is unsigned, and the install says so. install.cmd runs this script
# whatever PowerShell execution policy is set locally; one set by Group
# Policy still applies.
# Every step is idempotent and safe to rerun.

# The body runs in a scope of its own: the one-line install runs inside the
# caller's session, which keeps its own variables and preferences and must
# never be closed by an exit. So the script has no param block, which would
# bind in that session; the block reads the script's arguments itself.
& {
    $ErrorActionPreference = "Stop"

    # The install says what it is doing in a few plain lines, the same ones
    # whether it runs here or in the setup window, which reads them: a step
    # as it begins, what the step under way is doing, a note to read, and how
    # it ended. Every other detail goes to its log: the setup names the log
    # it keeps in CODE_GOBLINS_LOG, and otherwise the install keeps its own.
    $log = $env:CODE_GOBLINS_LOG
    if (-not $log) {
        $log = Join-Path ([IO.Path]::GetTempPath()) "CodeGoblinsInstall.log"
        Set-Content -LiteralPath $log -Value "Code Goblins install, $((Get-Date).ToString("u"))" -Encoding UTF8
    }
    # The steps a release's install says, which the setup window lists, in
    # their order; a clone's install builds its programs in place of the
    # first two.
    $releaseSteps = @("Download Code Goblins", "Check the download", "Install Code Goblins and its tools", "Open Code Goblins")
    $steps = $releaseSteps

    function Write-Detail([string]$Text) {
        Add-Content -LiteralPath $log -Value $Text -Encoding UTF8
    }
    function Write-Plain([string]$Text) {
        Write-Host $Text
        Write-Detail $Text
    }
    function Write-Step([string]$Title) {
        Write-Plain ("[{0}/{1}] {2}" -f ([array]::IndexOf($steps, $Title) + 1), $steps.Count, $Title)
    }
    function Write-Doing([string]$Text) {
        Write-Plain "      $Text"
    }
    function Write-Note([string]$Text) {
        Write-Plain "Note: $Text"
    }

    # A program run here writes everything it prints to the log, and the
    # notes cfo install has for the person to the screen too. Its exit code
    # is returned, and its last line kept for a failure to name; one that is
    # not there fails as a program that ran and failed does.
    $lastLine = @{ Text = "" }
    function Invoke-Logged([string]$Program, [string[]]$Arguments) {
        if (-not (Get-Command $Program -ErrorAction SilentlyContinue)) {
            $lastLine.Text = "$Program is not there"
            Write-Detail $lastLine.Text
            return 1
        }
        $ErrorActionPreference = "Continue"
        & $Program @Arguments 2>&1 | ForEach-Object {
            $line = "$_"
            Write-Detail $line
            if ($line.StartsWith("Note: ")) {
                Write-Host $line
            }
            if ($line.Trim()) {
                $lastLine.Text = $line.Trim()
            }
        }
        return $LASTEXITCODE
    }

    # Whatever stops the install is said in one sentence, the error's own,
    # with the log that holds the rest, never as PowerShell's error record.
    # Run as a file, as the setup and install.cmd run it, the install then
    # exits 1; run through Invoke-Expression, as the one-line install is, it
    # ends with LASTEXITCODE 1, as a program that failed leaves it, and leaves
    # the session it runs in open.
    trap {
        Write-Detail ($_ | Out-String)
        Write-Plain "Failed: $($_.Exception.Message)"
        Write-Plain "The full log is $log"
        if ($PSCommandPath) {
            exit 1
        }
        $global:LASTEXITCODE = 1
        return
    }

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

    # Save-VerifiedRelease downloads the release's cfo.exe into $Folder, and
    # its desktop window when the release's SHA256SUMS lists one, keeping each
    # only when it matches its sum and, where this script names the release's
    # publisher, is validly signed by it. It returns $false when the release
    # cannot be downloaded at all, and throws on a mismatch before anything
    # downloaded is run; the caller then removes $Folder.
    function Save-VerifiedRelease([string]$Folder) {
        $sums = Join-Path $Folder "SHA256SUMS"
        $expected = @{}
        $names = @("cfo.exe")
        try {
            try {
                Write-Detail "Downloading cfo.exe from $releaseBase ..."
                Invoke-WebRequest -Uri "$releaseBase/SHA256SUMS" -OutFile $sums -UseBasicParsing
                if (Read-ReleaseChecksum $sums "goblins-window.exe") {
                    $names += "goblins-window.exe"
                }
                foreach ($name in $names) {
                    $expected[$name] = Read-ReleaseChecksum $sums $name
                    Invoke-WebRequest -Uri "$releaseBase/$name" -OutFile (Join-Path $Folder "$name.download") -UseBasicParsing
                }
            }
            catch {
                Write-Detail "No release could be downloaded: $($_.Exception.Message)"
                return $false
            }
            Write-Step "Check the download"
            if (-not $releasePublisher) {
                Write-Detail "This copy of the install script names no publisher, so the download is checked against the release's SHA256SUMS only."
            }
            foreach ($name in $names) {
                $download = Join-Path $Folder "$name.download"
                $actual = (Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash
                if (-not $expected[$name] -or $actual -ne $expected[$name]) {
                    # The hashes go on a line of their own: PowerShell 7 wraps
                    # a long error across its error view.
                    Write-Detail "SHA256 of the download: $actual; the release's SHA256SUMS lists: '$($expected[$name])'"
                    throw "The downloaded $name does not match the release's checksum, so nothing was installed. Try again in a few minutes."
                }
                $signed = ""
                if ($releasePublisher) {
                    $signature = Get-AuthenticodeSignature -LiteralPath $download
                    $signer = ""
                    if ($signature.SignerCertificate) {
                        $signer = $signature.SignerCertificate.GetNameInfo("SimpleName", $false)
                    }
                    if ($signature.Status -ne "Valid" -or $signer -ne $releasePublisher) {
                        Write-Detail "Signature of the download: $($signature.Status), by '$signer'; the release is signed by '$releasePublisher'"
                        throw "The downloaded $name is not signed by $releasePublisher, so nothing was installed. Try again in a few minutes."
                    }
                    $signed = " and its signature by $releasePublisher"
                }
                Move-Item -LiteralPath $download -Destination (Join-Path $Folder $name) -Force
                Write-Detail "Verified $name against the release's SHA256SUMS ($actual)$signed."
            }
            return $true
        }
        finally {
            $leftovers = @($sums) + @($names | ForEach-Object { Join-Path $Folder "$_.download" })
            Remove-Item -LiteralPath $leftovers -Force -ErrorAction SilentlyContinue
        }
    }

    # Get-UserEnvironment returns the user-scope variable $Name. Where
    # CFO_USER_ENV_FILE is set it reads that file, which stands in for the
    # user-scope environment as it does for Add-UserPath and cfo install, so a
    # test's install never reads the machine's own user environment.
    function Get-UserEnvironment([string]$Name) {
        if (-not $env:CFO_USER_ENV_FILE) {
            return [Environment]::GetEnvironmentVariable($Name, "User")
        }
        if (-not (Test-Path -LiteralPath $env:CFO_USER_ENV_FILE)) {
            return $null
        }
        return (Get-Content -Raw -LiteralPath $env:CFO_USER_ENV_FILE | ConvertFrom-Json).$Name
    }

    # Install-Home runs cfo install from $Program, which picks the home itself:
    # the one already in use, kept where it is, or the per-user home. Its
    # report goes to the log, and its notes to the screen.
    function Install-Home([string]$Program) {
        if ((Invoke-Logged $Program @("install")) -ne 0) {
            throw "Code Goblins could not be set up: $($lastLine.Text -replace '^install: ', '')"
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
    # have it stops here, before anything is downloaded or changed, naming the
    # one fix to make.
    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
        $needed = @("git", "gh" | Where-Object { -not (Get-Command $_ -ErrorAction SilentlyContinue) })
        if ($needed.Count -gt 0) {
            throw "This PC has no winget, which installs $($needed -join ' and ') for Code Goblins: install App Installer from the Microsoft Store (https://apps.microsoft.com/detail/9NBLGGH4NNS1), then try again."
        }
    }

    if ($Dev) {
        $steps = @("Build Code Goblins from this clone", "Install Code Goblins and its tools", "Open Code Goblins")
        if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
            throw "-Dev builds cfo.exe from this clone, which needs Go: install it with winget install -e --id GoLang.Go, then open a new terminal and run this again."
        }
        if (-not (Get-Command npm -ErrorAction SilentlyContinue)) {
            throw "-Dev builds the board cfo.exe embeds from this clone, which needs Node.js: install it with winget install -e --id OpenJS.NodeJS.LTS, then open a new terminal and run this again."
        }

        # A clone bootstrapped before the Lavish ruling still has the retired review
        # surface binary here, where nothing builds it and nothing ignores it any
        # more; left alone it sits untracked forever and can be committed by accident.
        $retiredSurface = Join-Path $scriptFolder "showcase-axi.exe"
        if (Test-Path $retiredSurface) {
            Remove-Item $retiredSurface -Force -ErrorAction SilentlyContinue
            if (Test-Path $retiredSurface) {
                Write-Detail "WARN     retired review-surface binary still present; delete $retiredSurface by hand"
            }
            else {
                Write-Detail "Removed the retired review-surface binary -> $retiredSurface"
            }
        }

        # The programs build to a folder of their own, and cfo install puts them
        # in the per-user home, as the one-line install does. The clone keeps
        # only what git ignores: the board npm builds, which cfo.exe embeds
        # from there, and frontend's node_modules.
        $build = Join-Path ([IO.Path]::GetTempPath()) ("code-goblins-build-" + [Guid]::NewGuid().ToString("N"))
        New-Item -ItemType Directory -Path $build | Out-Null
        try {
            Write-Step "Build Code Goblins from this clone"
            Write-Detail "Building the board, cfo.exe and the desktop window from $scriptFolder ..."
            $dest = Join-Path $build "cfo.exe"
            Push-Location -LiteralPath $scriptFolder
            try {
                # cfo.exe embeds the board npm builds from frontend, and without it
                # serves a page saying the board was not built.
                if ((Invoke-Logged "npm.cmd" @("--prefix", "frontend", "ci")) -ne 0) { throw "npm ci in frontend failed: $($lastLine.Text)" }
                if ((Invoke-Logged "npm.cmd" @("--prefix", "frontend", "run", "build")) -ne 0) { throw "npm run build in frontend failed: $($lastLine.Text)" }
                if ((Invoke-Logged "go" @("build", "-trimpath", "-o", $dest, "./cmd/cfo")) -ne 0) { throw "go build failed: $($lastLine.Text)" }
                # -H windowsgui: the window is a program with no console.
                # production: as a release builds it, with no developer tools and
                # no browser menu on a right click.
                if ((Invoke-Logged "go" @("build", "-trimpath", "-o", (Join-Path $build "goblins-window.exe"), "-ldflags", "-H windowsgui", "-tags", "production", "./cmd/goblins-window")) -ne 0) { throw "go build of the desktop window failed: $($lastLine.Text)" }
            }
            finally {
                Pop-Location
            }
            # Said plainly, because nothing else will: a build from source has no
            # publisher, and no release is signed yet.
            Write-Detail "These programs are unsigned: they were built on this PC from this clone, and Code Goblins has no signed release yet."
            Write-Detail "Windows runs a program built here without asking. A copy taken to another PC is unsigned there too: SmartScreen may show ""Windows protected your PC"" with an unknown publisher, where More info and then Run anyway starts it, and Smart App Control, where it is on, blocks it."

            # cfo install puts the window built beside it in the home.
            $deliveredWindow = $true
            Write-Step "Install Code Goblins and its tools"
            Install-Home $dest
        }
        finally {
            Remove-Item -LiteralPath $build -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
    else {
        $download = Join-Path ([IO.Path]::GetTempPath()) ("code-goblins-" + [Guid]::NewGuid().ToString("N"))
        New-Item -ItemType Directory -Path $download | Out-Null
        try {
            $downloaded = Join-Path $download "cfo.exe"
            Write-Step "Download Code Goblins"
            if (-not (Save-VerifiedRelease $download)) {
                throw "Code Goblins could not be downloaded from $releaseBase. Check the internet connection, then try again."
            }
            # cfo install puts the window downloaded beside it in the home.
            $deliveredWindow = Test-Path -LiteralPath (Join-Path $download "goblins-window.exe")

            Write-Step "Install Code Goblins and its tools"
            Install-Home $downloaded
        }
        finally {
            Remove-Item -LiteralPath $download -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
    # The home cfo install picked: the one already in use, wherever it is,
    # or the per-user home.
    $InstallDir = Get-UserEnvironment "CFO_HOME"
    if (-not $InstallDir) {
        $InstallDir = Join-Path $env:LOCALAPPDATA "CodeGoblins"
    }
    # cfo install set both at user scope; this session needs them now.
    $env:CFO_HOME = $InstallDir
    $env:Path = "$(Join-Path $InstallDir "bin");$env:Path"
    # What follows runs the build the home now holds.
    $dest = Join-Path $InstallDir "bin\cfo.exe"

    # From here on, native stderr (npm progress, installer notes, mklink) must
    # not abort the install. Real failures are detected explicitly via exit
    # codes and existence checks instead.
    $ErrorActionPreference = "Continue"


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
                Write-Detail "Downloading $Uri failed: $($_.Exception.Message) Trying again in $(5 * $attempt) s ..."
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
                Write-Detail "SHA256 of the download: $actual; the release's checksums.txt lists: '$expected'"
                throw "The downloaded $archive does not match the release's checksums.txt, so it was not installed."
            }
            Write-Detail "Verified $archive against the release's checksums.txt ($actual)."
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
                if ((Invoke-Logged $target @("daemon", "stop")) -ne 0) {
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
            Write-Detail ("ok       {0,-20} {1} added to your PATH" -f "no-mistakes", $folder)
        }
        if ((Invoke-Logged $target @("daemon", "start")) -ne 0) {
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
    $tools = @(
        @{ Name = "git";                 Kind = "winget";     Cmd = "winget install -e --id Git.Git --accept-package-agreements --accept-source-agreements" },
        @{ Name = "gh";                  Kind = "winget";     Cmd = "winget install -e --id GitHub.cli --accept-package-agreements --accept-source-agreements" },
        @{ Name = "claude";              Kind = "powershell"; Cmd = "https://claude.ai/install.ps1" },
        @{ Name = "codex";               Kind = "npm";        Cmd = "npm.cmd install -g @openai/codex" },
        @{ Name = "pi";                  Kind = "npm";        Cmd = "npm.cmd install -g @earendil-works/pi-coding-agent" },
        @{ Name = "tasks-axi";           Kind = "npm";        Cmd = "npm.cmd install -g tasks-axi" },
        @{ Name = "quota-axi";           Kind = "npm";        Cmd = "npm.cmd install -g quota-axi" },
        @{ Name = "no-mistakes";         Kind = "release";    Cmd = "https://github.com/kunchenguid/no-mistakes/releases/download/v$noMistakesVersion" },
        @{ Name = "gh-axi";              Kind = "npm";        Cmd = "npm.cmd install -g gh-axi" },
        @{ Name = "chrome-devtools-axi"; Kind = "npm";        Cmd = "npm.cmd install -g chrome-devtools-axi" },
        @{ Name = "lavish-axi";          Kind = "npm-file";   Cmd = "https://github.com/fpresta0607/lavish-axi/releases/download/v0.1.79-codegoblins.3/lavish-axi-0.1.79-codegoblins.3.tgz"; Sha256 = "C9D491112C4B971A957B7B72D7E72378E42ECA20BFB4A3E725E60E76C18BA9B9" }
    )

    $npmPresent = [bool](Get-Command npm -ErrorAction SilentlyContinue)
    $wingetPresent = [bool](Get-Command winget -ErrorAction SilentlyContinue)

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
                Write-Detail ("WARN     {0,-20} {1} is v{2}, older than the pinned v{3}, and comes first on PATH; run: no-mistakes update, or remove the older copy" -f $tool.Name, $found.Source, $pathVersion, $noMistakesVersion)
                $failedInstalls += $tool.Name
                continue
            }
            if (Test-Path -LiteralPath $noMistakesProgram) {
                $managedVersion = Read-NoMistakesVersion $noMistakesProgram
                if ($managedVersion -and [version]$managedVersion -lt [version]$noMistakesVersion) {
                    Write-Detail ("update   {0,-20} v{1} is older than the pinned v{2}" -f $tool.Name, $managedVersion, $noMistakesVersion)
                    $found = $null
                }
                else {
                    if (Add-UserPath (Split-Path -Parent $noMistakesProgram)) {
                        Write-Detail ("ok       {0,-20} {1} added to your PATH" -f $tool.Name, (Split-Path -Parent $noMistakesProgram))
                    }
                    $found = Get-Command $noMistakesProgram
                }
            }
        }
        if ($found) {
            Write-Detail ("ok       {0,-20} present" -f $tool.Name)
            continue
        }
        if (($tool.Kind -in "npm", "npm-file") -and -not $npmPresent) {
            Write-Detail ("PREREQ   {0,-20} install Node.js first: winget install OpenJS.NodeJS.LTS" -f $tool.Name)
            $failedInstalls += $tool.Name
            continue
        }
        if ($tool.Kind -eq "winget" -and -not $wingetPresent) {
            Write-Detail ("PREREQ   {0,-20} install winget first (ships with Windows App Installer)" -f $tool.Name)
            $failedInstalls += $tool.Name
            continue
        }
        Write-Detail ("install  {0,-20} {1}" -f $tool.Name, $tool.Cmd)
        $label = @{ git = "Git"; gh = "the GitHub CLI"; claude = "Claude Code"; codex = "Codex"; pi = "Pi" }[$tool.Name]
        Write-Doing "Installing $(if ($label) { $label } else { $tool.Name })"
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
                    $code = Invoke-Logged "powershell" @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $installer)
                    if ($code -ne 0) { throw "installer exited with code $code" }
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
                        Write-Detail "SHA256 of the download: $actual; this install pins: $($tool.Sha256)"
                        throw "The downloaded $file does not match the SHA256 this install pins, so it was not installed."
                    }
                    Write-Detail "Verified $file against the SHA256 this install pins ($actual)."
                    $code = Invoke-Logged "npm.cmd" @("install", "-g", $saved)
                    if ($code -ne 0) { throw "exited with code $code" }
                }
                finally {
                    Remove-Item -LiteralPath $download -Recurse -Force -ErrorAction SilentlyContinue
                }
                $installedAny = $true
            }
            else {
                $command = @($tool.Cmd -split ' ')
                $code = Invoke-Logged $command[0] @($command | Select-Object -Skip 1)
                if ($code -ne 0) { throw "exited with code $code" }
                $installedAny = $true
            }
            Write-Detail ("ok       {0,-20} installed" -f $tool.Name)
        }
        catch {
            Write-Detail ("WARN     {0,-20} install failed: {1}" -f $tool.Name, $_.Exception.Message)
            $failedInstalls += $tool.Name
        }
    }

    # Claude Code's native installer puts claude.exe, the build a native
    # terminal starts, in ~\.local\bin and may leave that off PATH.
    $claudeBin = Join-Path $env:USERPROFILE ".local\bin"
    if ((Test-Path -LiteralPath (Join-Path $claudeBin "claude.exe")) -and (Add-UserPath $claudeBin)) {
        Write-Detail ("ok       {0,-20} {1} added to your PATH" -f "claude", $claudeBin)
        $installedAny = $true
    }

    # Installers write PATH entries to the registry; make them visible in this
    # shell (union with the current PATH, so nothing already present is lost).
    if ($installedAny) {
        Write-Detail ""
        Write-Detail "Refreshing PATH so newly installed tools are visible in this session ..."
        $parts = @($env:Path -split ';') + @([Environment]::GetEnvironmentVariable("Path", "Machine") -split ';') + @([string](Get-UserEnvironment "Path") -split ';')
        $env:Path = ($parts | Where-Object { $_ -ne "" } | Select-Object -Unique) -join ';'
    }

    # The native build's folder is appended to PATH, so a script left earlier
    # on it, such as npm's claude.cmd, still wins until the user removes it.
    # Warn only once the native build is installed: until then the script is
    # the only working claude.
    $claude = Get-Command claude -ErrorAction SilentlyContinue
    if ($claude -and [IO.Path]::GetExtension($claude.Source) -ne ".exe" -and (Test-Path -LiteralPath (Join-Path $claudeBin "claude.exe"))) {
        Write-Detail ("WARN     {0,-20} resolves to {1}, a script a native terminal cannot start; run: npm.cmd uninstall -g @anthropic-ai/claude-code" -f "claude", $claude.Source)
        $failedInstalls += "claude"
    }

    # The tools the fleet drives publish their own skills, installed once at
    # user scope so every harness and every project sees them.
    Write-Detail ""
    foreach ($skill in @("gh-axi", "chrome-devtools-axi", "no-mistakes")) {
        if (-not (Get-Command npx.cmd -ErrorAction SilentlyContinue)) {
            Write-Detail ("PREREQ   {0,-20} skill needs Node.js: winget install OpenJS.NodeJS.LTS" -f $skill)
            $failedInstalls += "$skill skill"
            continue
        }
        # Only the fleet's harnesses, as copies: a symlink needs a right an
        # ordinary Windows user may not have.
        Write-Detail ("skill    {0,-20} npx skills add kunchenguid/{0} --skill {0} -g -y -a claude-code -a codex -a pi --copy" -f $skill)
        $code = Invoke-Logged "npx.cmd" @("-y", "skills", "add", "kunchenguid/$skill", "--skill", $skill, "-g", "-y", "-a", "claude-code", "-a", "codex", "-a", "pi", "--copy")
        if ($code -ne 0) {
            Write-Detail ("WARN     {0,-20} skill install exited with code {1}" -f $skill, $code)
            $failedInstalls += "$skill skill"
        }
    }
    # The board's native lifecycle hooks, for each harness installed here.
    foreach ($harness in @("claude", "codex", "pi")) {
        if (-not (Get-Command $harness -ErrorAction SilentlyContinue)) {
            continue
        }
        if ((Invoke-Logged $dest @("hooks", "install", $harness)) -ne 0) {
            Write-Detail ("WARN     {0,-20} native hooks were not installed; see the line above" -f $harness)
            $failedInstalls += "$harness hooks"
        }
    }

    # Code Goblins in the Start menu opens the app. Where this install put the
    # desktop window in the home, built or downloaded just now, the entry
    # starts that program alone: it runs goblins out of sight, which finds the
    # supervisor or starts it, and opens the board in the window, so no
    # terminal shows; it starts no CFO, which the board's first-run page does.
    # A window the home only kept, as a release with none leaves the one from
    # before, may be from before a window started alone opened the app, so the
    # entry runs goblins --window, which opens any window, with its console
    # minimized for as long as that takes. A home with no window runs the quick
    # start in a window of its own: it starts the supervisor and the CFO when
    # they are not running and ends on a screen that offers the CFO's terminal
    # and the board. In a terminal, goblins is the quick start either way.
    $goblins = Join-Path $InstallDir "bin\goblins.exe"
    $window = Join-Path $InstallDir "bin\goblins-window.exe"
    $opensWindow = Test-Path -LiteralPath $window
    $programs = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs"
    $shortcutPath = Join-Path $programs "Code Goblins.lnk"
    try {
        New-Item -ItemType Directory -Force -Path $programs -ErrorAction Stop | Out-Null
        $shortcut = (New-Object -ComObject WScript.Shell).CreateShortcut($shortcutPath)
        $shortcut.WorkingDirectory = $InstallDir
        $shortcut.Arguments = ""
        $shortcut.WindowStyle = 1
        if ($deliveredWindow) {
            $shortcut.TargetPath = $window
            $shortcut.Description = "Open Code Goblins"
        }
        elseif ($opensWindow) {
            $shortcut.TargetPath = $goblins
            $shortcut.Arguments = "--window"
            $shortcut.WindowStyle = 7
            $shortcut.Description = "Open Code Goblins"
        }
        else {
            $shortcut.TargetPath = $goblins
            $shortcut.Description = "Start Code Goblins"
        }
        $shortcut.Save()
        Write-Detail ("shortcut {0,-20} {1}" -f "Code Goblins", $shortcutPath)
        # A window this install delivered replaces the standalone entry.
        $earlierShortcut = Join-Path $programs "Code Goblins Window.lnk"
        if ($deliveredWindow -and (Test-Path -LiteralPath $earlierShortcut)) {
            Remove-Item -LiteralPath $earlierShortcut -Force
            Write-Detail ("removed  {0,-20} {1}" -f "Code Goblins Window", $earlierShortcut)
        }
    }
    catch {
        Write-Detail ("WARN     {0,-20} the Start-menu shortcut was not made: {1}" -f "Code Goblins", $_.Exception.Message)
        $failedInstalls += "Start-menu shortcut"
    }

    Write-Detail ""
    Write-Detail "Verifying the toolchain ..."
    $doctorExit = Invoke-Logged $dest @("doctor")

    # A tool that could not be installed leaves out only what needs it, so the
    # install goes on and names it in one line; the log says why.
    if ($failedInstalls.Count -gt 0) {
        Write-Note ("These could not be installed, so only what needs them is left out: {0}. The log says why." -f ($failedInstalls -join ", "))
    }

    # Last, the app: opening it is enough, as Code Goblins in the Start menu
    # opens it. It finds the supervisor or starts it and shows the board, whose
    # first-run page starts the CFO. A home with no app, as a release that
    # ships none leaves, runs the quick start in this window instead: it starts
    # the supervisor, and where nobody can answer it, as in the setup, it
    # accepts nothing.
    Write-Step "Open Code Goblins"
    $opened = $false
    if ($opensWindow) {
        try {
            Start-Process -FilePath $window -WorkingDirectory $InstallDir -ErrorAction Stop
            $opened = $true
        }
        catch {
            Write-Detail "$window did not start: $($_.Exception.Message)"
        }
    }
    elseif (Test-Path -LiteralPath $goblins) {
        & $goblins
        $opened = $LASTEXITCODE -eq 0
    }
    if (-not $opened) {
        Write-Note "Code Goblins did not open by itself; open it from Code Goblins in the Start menu."
    }
    Write-Plain "Done: Code Goblins is installed in $InstallDir."
    Write-Plain "The full log is $log"
    if ($Dev) {
        exit $doctorExit
    }
    $global:LASTEXITCODE = 0
} $args
