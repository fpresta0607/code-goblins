# install.ps1 - build or download the code-goblins CFO binary (cfo.exe) and
# the showcase-axi review-surface binary (showcase-axi.exe) into this repo,
# then verify the toolchain. Run it once per clone:
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File install.ps1 -Bootstrap
#
# Without -Bootstrap it detects each tool `cfo doctor` checks and prints the
# exact install command for anything missing. With -Bootstrap it runs those
# installs automatically. Every install is idempotent and safe to rerun.
param(
    [string]$InstallDir = "",
    [switch]$Bootstrap
)

$ErrorActionPreference = "Stop"
$repo = "fpresta0607/code-goblins"

if (-not $InstallDir) {
    $InstallDir = Split-Path -Parent $MyInvocation.MyCommand.Path
}
$dest = Join-Path $InstallDir "cfo.exe"
$destShowcase = Join-Path $InstallDir "showcase-axi.exe"

# Prefer a published release; fall back to building from source (needs Go).
try {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest"
    $version = $release.tag_name
    Write-Host "Downloading cfo $version ..."
    Invoke-WebRequest -Uri "https://github.com/$repo/releases/download/$version/cfo.exe" -OutFile $dest
    Write-Host "Downloading showcase-axi $version ..."
    Invoke-WebRequest -Uri "https://github.com/$repo/releases/download/$version/showcase-axi.exe" -OutFile $destShowcase
}
catch {
    Write-Host "No release binary found; building from source (requires Go) ..."
    Push-Location $InstallDir
    try {
        go build -o $dest ./cmd/cfo
        if ($LASTEXITCODE -ne 0) { throw "go build failed" }
        go build -o $destShowcase ./cmd/showcase-axi
        if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    }
    finally {
        Pop-Location
    }
}

Write-Host ""
Write-Host "Installed cfo.exe -> $dest"
Write-Host "Installed showcase-axi.exe -> $destShowcase"
Write-Host "Next: run cfo install from this checkout to wire CFO_HOME, PATH, and the Claude Code hooks in your user settings."
Write-Host ""

# From here on, native stderr (npm progress, installer notes, mklink) must
# not abort a bootstrap. Real failures are detected explicitly via exit codes
# and existence checks instead.
$ErrorActionPreference = "Continue"

# Ship the bundled skills to the harnesses that read a different project
# directory: claude reads .claude/skills and codex reads .codex/skills, while
# kimi and pi read .agents/skills directly. A junction keeps one copy tracked
# in git (no duplicate files, no developer-mode symlinks).
function Ensure-SkillJunctions {
    param([string]$Root)
    $source = Join-Path $Root ".agents\skills"
    if (-not (Test-Path $source)) {
        Write-Host "WARN     skills           .agents\skills not found; skipping skill junctions"
        return
    }
    foreach ($rel in @(".claude\skills", ".codex\skills")) {
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
}

# The toolchain mirrors `cfo doctor`; its hints are the source of truth for
# where each tool comes from. Kind says how a missing tool gets installed:
#   winget     - a winget package (needs winget)
#   npm        - a global npm package (needs npm, i.e. Node.js)
#   powershell - an official install.ps1, fetched and run in a child shell so
#                its own `exit` cannot kill this bootstrap
#   manual     - no scriptable installer; print the manual step instead
$tools = @(
    @{ Name = "git";                 Kind = "winget";     Cmd = "winget install -e --id Git.Git --accept-package-agreements --accept-source-agreements" },
    @{ Name = "gh";                  Kind = "winget";     Cmd = "winget install -e --id GitHub.cli --accept-package-agreements --accept-source-agreements" },
    @{ Name = "claude";              Kind = "npm";        Cmd = "npm install -g @anthropic-ai/claude-code" },
    @{ Name = "herdr";               Kind = "powershell"; Cmd = "irm https://herdr.dev/install.ps1 | iex" },
    @{ Name = "codex";               Kind = "npm";        Cmd = "npm install -g @openai/codex" },
    @{ Name = "pi";                  Kind = "npm";        Cmd = "npm install -g @earendil-works/pi-coding-agent" },
    @{ Name = "kimi";                Kind = "manual";     Cmd = "install the Kimi Code CLI from https://www.kimi.com (no scriptable installer; sign in after)" },
    @{ Name = "tasks-axi";           Kind = "npm";        Cmd = "npm install -g tasks-axi" },
    @{ Name = "quota-axi";           Kind = "npm";        Cmd = "npm install -g quota-axi" },
    @{ Name = "no-mistakes";         Kind = "powershell"; Cmd = "irm https://raw.githubusercontent.com/kunchenguid/no-mistakes/main/docs/install.ps1 | iex" },
    @{ Name = "gh-axi";              Kind = "npm";        Cmd = "npm install -g gh-axi" },
    @{ Name = "chrome-devtools-axi"; Kind = "npm";        Cmd = "npm install -g chrome-devtools-axi" }
)

$npmPresent = [bool](Get-Command npm -ErrorAction SilentlyContinue)
$wingetPresent = [bool](Get-Command winget -ErrorAction SilentlyContinue)

$missing = @()
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
        $missing += $tool.Name
        $manualSteps += $tool.Name
        continue
    }
    if (-not $Bootstrap) {
        Write-Host ("MISSING  {0,-20} install: {1}" -f $tool.Name, $tool.Cmd)
        $missing += $tool.Name
        continue
    }
    if ($tool.Kind -eq "npm" -and -not $npmPresent) {
        Write-Host ("PREREQ   {0,-20} install Node.js first: winget install OpenJS.NodeJS.LTS" -f $tool.Name)
        $missing += $tool.Name
        $failedInstalls += $tool.Name
        continue
    }
    if ($tool.Kind -eq "winget" -and -not $wingetPresent) {
        Write-Host ("PREREQ   {0,-20} install winget first (ships with Windows App Installer)" -f $tool.Name)
        $missing += $tool.Name
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
        $missing += $tool.Name
        $failedInstalls += $tool.Name
    }
}

# Installers write PATH entries to the registry; make them visible in this
# shell (union with the current PATH, so nothing already present is lost).
if ($Bootstrap -and $installedAny) {
    Write-Host ""
    Write-Host "Refreshing PATH so newly installed tools are visible in this session ..."
    $parts = @($env:Path -split ';') + @([Environment]::GetEnvironmentVariable("Path", "Machine") -split ';') + @([Environment]::GetEnvironmentVariable("Path", "User") -split ';')
    $env:Path = ($parts | Where-Object { $_ -ne "" } | Select-Object -Unique) -join ';'
}

# Wire the bundled skills into the harness project-scope directories.
if ($Bootstrap) {
    Write-Host ""
    Ensure-SkillJunctions -Root $InstallDir
}

Write-Host ""
Write-Host "Verifying the toolchain ..."
& $dest doctor
$doctorExit = $LASTEXITCODE

if ($missing.Count -gt 0) {
    Write-Host ""
    if ($Bootstrap) {
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
    else {
        Write-Host "Run install.ps1 -Bootstrap to install the missing tools, or run the install commands printed above:"
        foreach ($m in $missing) {
            Write-Host "  - $m"
        }
    }
}

exit $doctorExit
