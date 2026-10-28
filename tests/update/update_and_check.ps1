# update_and_check.ps1 - goblins update, as the person runs it in a terminal
# of their own, to the release the staged API names, and what it must keep.
# Want installed: the release's build runs and the board serves it. Want
# rolled-back: the build from before runs and the board serves it again.
# Either way the home, the CFO's and the goblin's running terminals, the
# person's settings, project record, stored credential and the goblin's work
# are as fleet_at_work.ps1 found them, and dictation is ready: its files as
# they were, or, where the release pins a newer model, that model fetched in
# their place by the board the update started.
#
#   .\tests\update\update_and_check.ps1 -Api <url> -Release <folder> -Want installed|rolled-back -Project <checkout> -Facts <file>
param(
    [Parameter(Mandatory = $true)][string]$Api,
    [Parameter(Mandatory = $true)][string]$Release,
    [Parameter(Mandatory = $true)][ValidateSet("installed", "rolled-back")][string]$Want,
    [Parameter(Mandatory = $true)][string]$Project,
    [Parameter(Mandatory = $true)][string]$Facts
)

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "fleet_facts.ps1")
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")
$env:CFO_HOME = [Environment]::GetEnvironmentVariable("CFO_HOME", "User")
$before = Get-Content -Raw -LiteralPath $Facts | ConvertFrom-Json
$tag = (Get-Content -Raw -LiteralPath (Join-Path $Release "latest") | ConvertFrom-Json).tag_name
$released = (Get-FileHash (Join-Path $Release "cfo.exe") -Algorithm SHA256).Hash.ToLowerInvariant()

# An update runs only in a terminal of the person's own, which a runner's
# step is not: the step runs it under a stand-in for Windows Terminal, as the
# person's goblins update runs under theirs.
$terminal = Join-Path $env:RUNNER_TEMP "terminal\WindowsTerminal.exe"
New-Item -ItemType Directory -Force -Path (Split-Path $terminal) | Out-Null
Copy-Item -LiteralPath (Join-Path $env:SystemRoot "System32\cmd.exe") -Destination $terminal -Force
$output = Join-Path $env:RUNNER_TEMP "update-$Want.log"
$env:CODE_GOBLINS_RELEASE_API = $Api
& $terminal /d /c "goblins update > `"$output`" 2>&1"
$code = $LASTEXITCODE
Remove-Item Env:CODE_GOBLINS_RELEASE_API
Get-Content -LiteralPath $output | Write-Host
$wantCode = @{ installed = 0; "rolled-back" = 3 }[$Want]
if ($code -ne $wantCode) { throw "goblins update exited with code $code, want $wantCode ($Want)" }

$after = Get-FleetFacts -Project $Project
$after | Format-Table -AutoSize -Wrap | Out-String -Width 400 | Write-Host
$problems = @()
if ($Want -eq "installed") {
    if ($after.version -ne "cfo $tag") { $problems += "goblins version prints '$($after.version)', want 'cfo $tag'" }
    if ($after.cfoExe -ne $released) { $problems += "bin\cfo.exe is $($after.cfoExe), want the release's $released" }
    if ($after.boardExe -ne $released) { $problems += "the board runs a program of SHA-256 $($after.boardExe), want the release's $released" }
    if ($after.board -eq $before.board) { $problems += "the board is still the supervisor from before the update: $($after.board)" }
}
else {
    foreach ($fact in "version", "cfoExe", "boardExe") {
        if ($after[$fact] -ne $before.$fact) { $problems += "after the rollback $fact is '$($after[$fact])', want '$($before.$fact)' as before" }
    }
}
if ($after.board -match "not running") { $problems += "the board's supervisor is not running: $($after.board)" }
foreach ($kept in "home", "cfo host", "cfo harness", "demo-goblin host", "demo-goblin harness", "allowanceFloor", "claudeSetting", "project", "credentials", "goblinWork") {
    if ($after[$kept] -ne $before.$kept) { $problems += "$kept changed across the update: '$($before.$kept)' became '$($after[$kept])'" }
}
if ($after.claudeSetting -ne "kept") { $problems += "the person's own Claude Code setting is '$($after.claudeSetting)', want 'kept'" }

# Dictation stays ready. A release that pins the model the home has keeps its
# files untouched; one that pins a newer model has its board fetch that one
# in place of the earlier, once it serves.
$model = (Get-Content -Raw config\voice.json | ConvertFrom-Json).model.name
$replaced = $Want -eq "installed" -and $before.dictation -notmatch [regex]::Escape($model)
if (-not $replaced -and $after.dictation -ne $before.dictation) { $problems += "dictation's engine and model changed on disk: '$($before.dictation)' became '$($after.dictation)'" }
$deadline = (Get-Date).AddMinutes(15)
do {
    $dictation = @(& (Join-Path $env:CFO_HOME "bin\goblins.exe") doctor) -match "^dictation:"
    $global:LASTEXITCODE = 0
} while ($replaced -and $dictation -notmatch "^dictation: ready: $([regex]::Escape($model)) " -and (Get-Date) -lt $deadline -and -not (Start-Sleep -Seconds 10))
Write-Host $dictation
if ($Want -eq "installed" -and $dictation -notmatch "^dictation: ready: $([regex]::Escape($model)) ") { $problems += "dictation is not ready on the release's model $model : $dictation" }
if ($Want -eq "rolled-back" -and $dictation -notmatch "^dictation: ready: ") { $problems += "dictation is not ready after the rollback: $dictation" }
if ($replaced) {
    $left = @(Get-ChildItem -LiteralPath (Join-Path $env:CFO_HOME "caches\voice") -Directory | Where-Object { Test-Path -LiteralPath (Join-Path $_.FullName "verified.json") })
    if ($left.Count -ne 2) { $problems += "caches\voice holds $($left.Count) verified parts after the new model's fetch, want its engine and model alone: $($left.Name -join ', ')" }
}

if ($problems.Count -gt 0) { throw "the update from $($before.version) did not keep what it must:`n- $($problems -join "`n- ")" }
Write-Host "goblins update from $($before.version): $Want, and the home, the CFO, the goblin, the settings, the project, the credential, the goblin's work and dictation are as they must be."
$global:LASTEXITCODE = 0
