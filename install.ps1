# install.ps1 - install Code Goblins.
#
# To use it, from any PowerShell window, with no clone and no Go:
#
#   irm https://raw.githubusercontent.com/fpresta0607/code-goblins/main/install.ps1 | iex
#
# downloads the latest release, refuses it unless it matches the release's
# SHA256SUMS, lets it set up the CFO home at %LOCALAPPDATA%\CodeGoblins with
# cfo.exe and goblins.exe on your PATH, asks once for the folder that holds
# your projects, installs the tools, skills and hooks the fleet needs, and
# ends with goblins doctor.
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
    $releaseBase = "https://github.com/$repo/releases/latest/download"
    if ($env:CODE_GOBLINS_RELEASE_BASE) {
        $releaseBase = $env:CODE_GOBLINS_RELEASE_BASE.TrimEnd("/")
    }

    # Save-VerifiedRelease downloads the release's cfo.exe to $Path and keeps it
    # only when it matches the release's SHA256SUMS. It returns $false when the
    # release cannot be downloaded at all, and throws on a mismatch, leaving
    # nothing at $Path.
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
            $expected = ""
            foreach ($line in Get-Content -LiteralPath $sums) {
                $fields = @($line.Trim() -split "\s+")
                if ($fields.Count -eq 2 -and $fields[1].TrimStart("*") -eq "cfo.exe") {
                    $expected = $fields[0]
                }
            }
            $actual = (Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash
            if (-not $expected -or $actual -ne $expected) {
                # The hashes go on a line of their own: PowerShell 7 wraps a
                # long error across its error view.
                Write-Host "SHA256 of the download: $actual; the release's SHA256SUMS lists: '$expected'"
                throw "The downloaded cfo.exe does not match the release's SHA256SUMS, so it was not installed."
            }
            Move-Item -LiteralPath $download -Destination $Path -Force
            Write-Host "Verified cfo.exe against the release's SHA256SUMS ($actual)."
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
            go build -o $built ./cmd/cfo
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
            Get-ChildItem -LiteralPath $InstallDir -Filter "$name.*.old" | Remove-Item -Force -ErrorAction SilentlyContinue
            if (Test-Path -LiteralPath $target) {
                Move-Item -LiteralPath $target -Destination "$target.$([Guid]::NewGuid().ToString("N")).old"
            }
            Copy-Item -LiteralPath $built -Destination $target
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

    # The toolchain mirrors `cfo doctor`; its hints are the source of truth for
    # where each tool comes from. npm is called as npm.cmd, which runs under any
    # execution policy, where the npm.ps1 shim is refused by the default one and
    # the one-line install cannot set it. Kind says how a missing tool gets
    # installed:
    #   winget     - a winget package (needs winget)
    #   npm        - a global npm package (needs npm, i.e. Node.js)
    #   powershell - an official install.ps1, fetched and run in a child shell so
    #                its own `exit` cannot kill this install
    #   manual     - no scriptable installer; print the manual step instead
    $tools = @(
        @{ Name = "git";                 Kind = "winget";     Cmd = "winget install -e --id Git.Git --accept-package-agreements --accept-source-agreements" },
        @{ Name = "gh";                  Kind = "winget";     Cmd = "winget install -e --id GitHub.cli --accept-package-agreements --accept-source-agreements" },
        @{ Name = "claude";              Kind = "npm";        Cmd = "npm.cmd install -g @anthropic-ai/claude-code" },
        @{ Name = "herdr";               Kind = "powershell"; Cmd = "irm https://herdr.dev/install.ps1 | iex" },
        @{ Name = "codex";               Kind = "npm";        Cmd = "npm.cmd install -g @openai/codex" },
        @{ Name = "pi";                  Kind = "npm";        Cmd = "npm.cmd install -g @earendil-works/pi-coding-agent" },
        @{ Name = "kimi";                Kind = "manual";     Cmd = "install the Kimi Code CLI from https://www.kimi.com (no scriptable installer; sign in after)" },
        @{ Name = "tasks-axi";           Kind = "npm";        Cmd = "npm.cmd install -g tasks-axi" },
        @{ Name = "quota-axi";           Kind = "npm";        Cmd = "npm.cmd install -g quota-axi" },
        @{ Name = "no-mistakes";         Kind = "powershell"; Cmd = "irm https://raw.githubusercontent.com/kunchenguid/no-mistakes/main/docs/install.ps1 | iex" },
        @{ Name = "gh-axi";              Kind = "npm";        Cmd = "npm.cmd install -g gh-axi" },
        @{ Name = "chrome-devtools-axi"; Kind = "npm";        Cmd = "npm.cmd install -g chrome-devtools-axi" },
        @{ Name = "lavish-axi";          Kind = "npm";        Cmd = "npm.cmd install -g lavish-axi@latest" }
    )

    $npmPresent = [bool](Get-Command npm -ErrorAction SilentlyContinue)
    $wingetPresent = [bool](Get-Command winget -ErrorAction SilentlyContinue)

    $manualSteps = @()
    $failedInstalls = @()
    $installedAny = $false
    foreach ($tool in $tools) {
        $found = Get-Command $tool.Name -ErrorAction SilentlyContinue
        if ($found) {
            Write-Host ("ok       {0,-20} present" -f $tool.Name)
            continue
        }
        if ($tool.Kind -eq "manual") {
            Write-Host ("MANUAL   {0,-20} {1}" -f $tool.Name, $tool.Cmd)
            $manualSteps += $tool.Name
            continue
        }
        if ($tool.Kind -eq "npm" -and -not $npmPresent) {
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
            if ($tool.Kind -eq "powershell") {
                & powershell -NoProfile -ExecutionPolicy Bypass -Command $tool.Cmd
                if ($LASTEXITCODE -ne 0) { throw "installer exited with code $LASTEXITCODE" }
            }
            else {
                Invoke-Expression $tool.Cmd
                if ($LASTEXITCODE -ne 0) { throw "exited with code $LASTEXITCODE" }
            }
            $installedAny = $true
            Write-Host ("ok       {0,-20} installed" -f $tool.Name)
        }
        catch {
            Write-Host ("WARN     {0,-20} install failed: {1}" -f $tool.Name, $_.Exception.Message)
            $failedInstalls += $tool.Name
        }
    }

    # Installers write PATH entries to the registry; make them visible in this
    # shell (union with the current PATH, so nothing already present is lost).
    if ($installedAny) {
        Write-Host ""
        Write-Host "Refreshing PATH so newly installed tools are visible in this session ..."
        $parts = @($env:Path -split ';') + @([Environment]::GetEnvironmentVariable("Path", "Machine") -split ';') + @([Environment]::GetEnvironmentVariable("Path", "User") -split ';')
        $env:Path = ($parts | Where-Object { $_ -ne "" } | Select-Object -Unique) -join ';'
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
        Write-Host "Code Goblins is built and installed from $InstallDir, which is your CFO home. Open a new terminal so cfo and goblins are on your PATH."
        exit $doctorExit
    }
    Write-Host "Code Goblins is installed in $InstallDir. Type goblins to start; a terminal that was already open finds it once you open a new one."
} $args
