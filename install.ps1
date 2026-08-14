# install.ps1 - build or download the code-goblins CFO binary (cfo.exe) into
# this repo, then verify the toolchain. Run it once per clone:
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File install.ps1
param(
    [string]$InstallDir = ""
)

$ErrorActionPreference = "Stop"
$repo = "fpresta0607/code-goblins"

if (-not $InstallDir) {
    $InstallDir = Split-Path -Parent $MyInvocation.MyCommand.Path
}
$dest = Join-Path $InstallDir "cfo.exe"

# Prefer a published release; fall back to building from source (needs Go).
try {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest"
    $version = $release.tag_name
    $url = "https://github.com/$repo/releases/download/$version/cfo.exe"
    Write-Host "Downloading cfo $version ..."
    Invoke-WebRequest -Uri $url -OutFile $dest
}
catch {
    Write-Host "No release binary found; building from source (requires Go) ..."
    go build -o $dest ./cmd/cfo
}

Write-Host ""
Write-Host "Installed cfo.exe -> $dest"
Write-Host "The .claude/settings.json hooks are already wired to $dest."
Write-Host ""
Write-Host "Verifying the toolchain ..."
& $dest doctor
