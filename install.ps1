# install.ps1 - download and install the code-goblins CFO binary (cfo.exe).
# Usage:
#   powershell -NoProfile -ExecutionPolicy Bypass -File install.ps1
#   powershell -NoProfile -ExecutionPolicy Bypass -File install.ps1 -Version v1.2.3
param(
    [string]$Version = "latest",
    [string]$InstallDir = ""
)

$ErrorActionPreference = "Stop"
$repo = "fpresta0607/code-goblins"

if (-not $InstallDir) {
    $InstallDir = Join-Path $env:LOCALAPPDATA "cfo"
}
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

if ($Version -eq "latest") {
    Write-Host "Looking up the latest release ..."
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest"
    $Version = $release.tag_name
}

$url = "https://github.com/$repo/releases/download/$Version/cfo.exe"
$dest = Join-Path $InstallDir "cfo.exe"

Write-Host "Downloading cfo $Version -> $dest"
Invoke-WebRequest -Uri $url -OutFile $dest

Write-Host ""
Write-Host "Installed cfo.exe to $InstallDir"
Write-Host "Add $InstallDir to your PATH, then run:"
Write-Host "  cfo doctor"
