# stage_releases.ps1 - two releases of this checkout, served as GitHub serves
# a latest release, for an update from a published release to prove itself
# against on a runner: good, this commit built as a release builds it, and
# broken, the same commit with a board that exits as it starts, which the
# update must roll back. Each is a folder with its programs, its SHA256SUMS,
# this checkout's install.ps1, which names no publisher as an unsigned
# release's does, and the record GitHub's API answers for the latest
# release, at <base>/<name>/latest.
#
# Run it from the checkout, after npm ci and npm run build in frontend, since
# cfo.exe embeds the board:
#
#   .\tests\update\stage_releases.ps1 -Version v0.5.99 -Folder <folder> -Port 8765
#
# It leaves the server running, its pid in <folder>\server.pid, for the job to
# end by that pid.
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$Folder,
    [Parameter(Mandatory = $true)][int]$Port
)

$ErrorActionPreference = "Stop"
$base = "http://127.0.0.1:$Port"
$serve = Join-Path (Get-Location) "cmd\cfo\serve.go"

function Build-Release([string]$Name) {
    $out = Join-Path $Folder $Name
    New-Item -ItemType Directory -Force -Path $out | Out-Null
    go build -trimpath -ldflags "-X main.version=$Version" -o (Join-Path $out "cfo.exe") ./cmd/cfo
    if ($LASTEXITCODE -ne 0) { throw "go build of $Name cfo.exe exited with code $LASTEXITCODE" }
    go build -trimpath -ldflags "-H windowsgui -X main.version=$Version" -o (Join-Path $out "cfo-hook.exe") ./cmd/cfo
    if ($LASTEXITCODE -ne 0) { throw "go build of $Name cfo-hook.exe exited with code $LASTEXITCODE" }
    go build -trimpath -tags production -ldflags "-H windowsgui" -o (Join-Path $out "goblins-window.exe") ./cmd/goblins-window
    if ($LASTEXITCODE -ne 0) { throw "go build of $Name goblins-window.exe exited with code $LASTEXITCODE" }
    Copy-Item install.ps1 (Join-Path $out "install.ps1")
    $names = "cfo.exe", "cfo-hook.exe", "goblins-window.exe"
    $lines = foreach ($file in $names) { "$((Get-FileHash (Join-Path $out $file) -Algorithm SHA256).Hash.ToLowerInvariant())  $file" }
    [IO.File]::WriteAllText((Join-Path $out "SHA256SUMS"), ($lines -join "`n") + "`n")
    $assets = foreach ($file in $names + "install.ps1", "SHA256SUMS") { [ordered]@{ name = $file; browser_download_url = "$base/$Name/$file" } }
    $record = [ordered]@{
        tag_name     = $Version
        html_url     = "$base/$Name/tag/$Version"
        published_at = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
        draft        = $false
        prerelease   = $false
        body         = "## What's Changed`n* The $Name build of this commit by @code-goblins in $base/$Name`n"
        assets       = @($assets)
    }
    [IO.File]::WriteAllText((Join-Path $out "latest"), (ConvertTo-Json -InputObject $record -Depth 4))
}

# install.ps1 must say whether its release is signed, as the update reads it.
if ((Get-Content -Raw install.ps1) -notmatch '(?m)^\s*\$releasePublisher = ""') { throw "this checkout's install.ps1 names no empty `$releasePublisher, so an update would refuse the staged release" }

Build-Release "good"

# The broken build's board ends as it starts, as a build that does not start
# does; everything else, its update among it, is this commit's.
$source = Get-Content -Raw $serve
$opening = "func runServe(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {"
if (-not $source.Contains($opening)) { throw "cmd\cfo\serve.go has no '$opening' to break" }
try {
    [IO.File]::WriteAllText($serve, $source.Replace($opening, "$opening`n`tfmt.Fprintln(stderr, `"this build's board does not start`")`n`treturn 1"))
    Build-Release "broken"
}
finally {
    [IO.File]::WriteAllText($serve, $source)
}

$server = Start-Process python -ArgumentList "-m", "http.server", "$Port", "--bind", "127.0.0.1", "--directory", $Folder -WindowStyle Hidden -PassThru
Set-Content -LiteralPath (Join-Path $Folder "server.pid") -Value $server.Id
$deadline = (Get-Date).AddSeconds(30)
while ($true) {
    try { Invoke-WebRequest "$base/good/latest" -UseBasicParsing | Out-Null; break }
    catch { if ((Get-Date) -gt $deadline) { throw "the release server did not answer within 30 seconds" }; Start-Sleep -Milliseconds 250 }
}
