# update_and_check.ps1 - goblins update, as the person runs it in a terminal
# of their own, to the release the staged API names, or with -InstallFrom the
# one-line install of the release served there run over the home, and what
# it must keep. Want installed: the release's build runs and the board serves
# it. Want rolled-back: the build from before runs and the board serves it
# again. Either way the home, the person's settings, project record, stored
# credential and the goblin's work are as fleet_at_work.ps1 found them, and
# dictation is ready: its files as they were, or, where the release pins a
# newer model, that model fetched in their place by the board the update
# started. A rollback restores the build from before, whose pinned asset
# upstream may have re-uploaded since this checkout pinned it: it keeps every
# verified part it had and reads as not ready, an expected difference and not
# a loss. When the runner could not make that download, the installed check
# runs `goblins dictation setup` once and records it as an environment
# result, never a failure of the change.
#
# -PreRename starts from a home that a release from before the rename runs,
# updated by the command that release knows itself by, with no hand cutover.
# Installed, the home has moved to goblins by itself: its settings begin
# GOBLINS_ and none begins with the old prefix, the move's record names the
# folder its undo reads, and the CEO's and the goblin's terminals restart
# under the new names, each once it is idle, after which the old programs
# the move kept for them are gone. Rolled back, the home is as it was before
# the update, old names and all. Otherwise the CEO's and the goblin's
# terminals keep running through the update.
#
#   .\tests\update\update_and_check.ps1 (-Api <url> | -InstallFrom <url>) -Release <folder> -Want installed|rolled-back -Project <checkout> -Facts <file> [-PreRename]
param(
    [string]$Api,
    [string]$InstallFrom,
    [Parameter(Mandatory = $true)][string]$Release,
    [Parameter(Mandatory = $true)][ValidateSet("installed", "rolled-back")][string]$Want,
    [Parameter(Mandatory = $true)][string]$Project,
    [Parameter(Mandatory = $true)][string]$Facts,
    [switch]$PreRename
)

$ErrorActionPreference = "Stop"
if ([bool]$Api -eq [bool]$InstallFrom) { throw "give -Api, to update the home, or -InstallFrom, to install over it, and not both" }
. (Join-Path $PSScriptRoot "fleet_facts.ps1")
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")
$command, $settingPrefix, $null = Get-BuildNames -PreRename:$PreRename
$ceoHome = [Environment]::GetEnvironmentVariable("${settingPrefix}HOME", "User")
Set-Item -LiteralPath "env:${settingPrefix}HOME" -Value $ceoHome
$state = Join-Path $ceoHome "state"
$bin = Join-Path $ceoHome "bin"
$isCrossing = $PreRename -and $Want -eq "installed"
$before = Get-Content -Raw -LiteralPath $Facts | ConvertFrom-Json
$tag = (Get-Content -Raw -LiteralPath (Join-Path $Release "latest") | ConvertFrom-Json).tag_name
$released = (Get-FileHash (Join-Path $Release "goblins.exe") -Algorithm SHA256).Hash.ToLowerInvariant()

if ($Api) {
    # An update runs only in a terminal of the person's own, which a runner's
    # step is not: the step runs it under a stand-in for Windows Terminal, as
    # the person's update runs under theirs.
    $terminal = Join-Path $env:RUNNER_TEMP "terminal\WindowsTerminal.exe"
    New-Item -ItemType Directory -Force -Path (Split-Path $terminal) | Out-Null
    Copy-Item -LiteralPath (Join-Path $env:SystemRoot "System32\cmd.exe") -Destination $terminal -Force
    # cmd takes the line whole, so the log's path holds no space to quote.
    $output = Join-Path $env:RUNNER_TEMP "update-$Want.log"
    if ($output.Contains(" ")) { throw "the update's log path '$output' holds a space, which the line cmd runs cannot carry unquoted" }
    $env:CODE_GOBLINS_RELEASE_API = $Api
    & $terminal /d /c "$command update > $output 2>&1"
    $code = $LASTEXITCODE
    Remove-Item Env:CODE_GOBLINS_RELEASE_API
    $ran = "$command update"
}
else {
    # The one-line install, this checkout's install.ps1, as the person runs it
    # in Windows PowerShell, downloading the release served at -InstallFrom.
    $output = Join-Path $env:RUNNER_TEMP "install-over-$Want.log"
    $env:CODE_GOBLINS_RELEASE_BASE = $InstallFrom
    $installer = Join-Path (Get-Location) "install.ps1"
    & (Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\powershell.exe") -NoProfile -ExecutionPolicy Bypass -Command "Get-Content -Raw -LiteralPath '$installer' | Invoke-Expression; exit `$LASTEXITCODE" *> $output
    $code = $LASTEXITCODE
    Remove-Item Env:CODE_GOBLINS_RELEASE_BASE
    $ran = "the one-line install"
}
Get-Content -LiteralPath $output | Write-Host
$wantCode = @{ installed = 0; "rolled-back" = 3 }[$Want]
if ($code -ne $wantCode) { throw "$ran exited with code $code, want $wantCode ($Want)" }

$problems = @()
$terminals = "ceo host", "ceo harness", "demo-goblin host", "demo-goblin harness"
if ($isCrossing) {
    # A new terminal sees the settings the move made; this step does not.
    Remove-Item -LiteralPath "env:${settingPrefix}HOME"
    $env:GOBLINS_HOME = $ceoHome
    if ([Environment]::GetEnvironmentVariable("GOBLINS_HOME", "User") -ne $ceoHome) { $problems += "the user setting GOBLINS_HOME is '$([Environment]::GetEnvironmentVariable("GOBLINS_HOME", "User"))', want the home $ceoHome" }
    $oldSettings = @((Get-Item -LiteralPath "HKCU:\Environment").GetValueNames() | Where-Object { $_.StartsWith($settingPrefix, [StringComparison]::OrdinalIgnoreCase) })
    if ($oldSettings.Count -gt 0) { $problems += "these user settings still begin $($settingPrefix): $($oldSettings -join ', ')" }
    $crossing = Get-Content -Raw -LiteralPath (Join-Path $state "crossing.json") | ConvertFrom-Json
    if (-not (Test-Path -LiteralPath (Join-Path $crossing.folder "undo.json"))) { $problems += "the move's record names the folder $($crossing.folder), which holds no undo.json for the way back" }
    $crossedAt = ([datetime]$crossing.at).ToUniversalTime()
    # Each terminal from before the move restarts under the new names once it
    # is idle with nothing typed on its prompt line, which the board's first
    # look after the move only announces; once none is left, nothing runs the
    # old programs the move kept, and the board removes them.
    $deadline = (Get-Date).AddMinutes(10)
    while ($true) {
        $waiting = @(foreach ($id in "ceo", "demo-goblin") {
                $record = Join-Path $state "hosts\$id.json"
                $process = if (Test-Path -LiteralPath $record) { Get-Process -Id (Get-Content -Raw -LiteralPath $record | ConvertFrom-Json).host_pid -ErrorAction SilentlyContinue }
                if (-not $process -or $process.StartTime.ToUniversalTime() -le $crossedAt) { "the $id terminal" }
            })
        $waiting += @("$command.exe", "$command-hook.exe" | Where-Object { Test-Path -LiteralPath (Join-Path $bin $_) } | ForEach-Object { "bin\$_" })
        if ($waiting.Count -eq 0) { break }
        if ((Get-Date) -gt $deadline) {
            $problems += "within 10 minutes of the move these were not restarted under the new names or removed: $($waiting -join ', ')"
            break
        }
        Start-Sleep -Seconds 5
    }
}
elseif ($PreRename) {
    # The rollback put the move back with the build, so the home is still one
    # from before the rename.
    if ($null -ne [Environment]::GetEnvironmentVariable("GOBLINS_HOME", "User")) { $problems += "after the rollback the user setting GOBLINS_HOME is there, want only $($settingPrefix)HOME" }
    if (Test-Path -LiteralPath (Join-Path $state "crossing.json")) { $problems += "after the rollback the home still holds the record of a move to goblins" }
}

$after = Get-FleetFacts -Project $Project -PreRename:($PreRename -and -not $isCrossing)
$after | Format-Table -AutoSize -Wrap | Out-String -Width 400 | Write-Host
if ($Want -eq "installed") {
    if ($after.version -ne "goblins $tag") { $problems += "goblins version prints '$($after.version)', want 'goblins $tag'" }
    if ($after.commandExe -ne $released) { $problems += "bin\goblins.exe is $($after.commandExe), want the release's $released" }
    if ($after.boardExe -ne $released) { $problems += "the board runs a program of SHA-256 $($after.boardExe), want the release's $released" }
    if ($after.board -eq $before.board) { $problems += "the board is still the supervisor from before the update: $($after.board)" }
}
else {
    foreach ($fact in "version", "commandExe", "boardExe") {
        if ($after[$fact] -ne $before.$fact) { $problems += "after the rollback $fact is '$($after[$fact])', want '$($before.$fact)' as before" }
    }
}
if ($after.board -match "not running") { $problems += "the board's supervisor is not running: $($after.board)" }
$kept = @("home", "allowanceFloor", "claudeSetting", "project", "goblinWork")
if ($isCrossing) {
    foreach ($running in $terminals) {
        if ($after[$running] -match "not running") { $problems += "after the move the $running is not running: $($after[$running])" }
    }
    # The credential listing's first line names its store, which for the file
    # store is a folder the move renames; the credentials are the lines after.
    $stored = { param([string]$listing) @($listing -split "`r?`n" | Select-Object -Skip 1) -join "; " }
    if ((& $stored $after.credentials) -ne (& $stored $before.credentials)) { $problems += "the stored credentials changed across the move: '$($before.credentials)' became '$($after.credentials)'" }
}
else {
    $kept += $terminals + "credentials"
}
foreach ($fact in $kept) {
    if ($after[$fact] -ne $before.$fact) { $problems += "$fact changed across the update: '$($before.$fact)' became '$($after[$fact])'" }
}
if ($after.claudeSetting -ne "kept") { $problems += "the person's own Claude Code setting is '$($after.claudeSetting)', want 'kept'" }

# Dictation stays ready. A release that pins the model the home has keeps its
# files untouched; one that pins a newer model has its board fetch that one
# in place of the earlier, once it serves. When the runner could not make
# that download, tests/acceptance/dictation.ps1 runs `goblins dictation setup`
# once and records the runner's failure as an environment result, never a
# product failure; any other outcome fails, as that script decides it.
$model = (Get-Content -Raw config\voice.json | ConvertFrom-Json).model.name
$replaced = $Want -eq "installed" -and $before.dictation -notmatch [regex]::Escape($model)
if (-not $replaced -and $after.dictation -ne $before.dictation) { $problems += "dictation's engine and model changed on disk: '$($before.dictation)' became '$($after.dictation)'" }
$deadline = (Get-Date).AddMinutes(15)
do {
    $dictation = @(& (Join-Path $ceoHome "bin\goblins.exe") doctor) -match "^dictation:"
    $global:LASTEXITCODE = 0
} while ($replaced -and $dictation -notmatch "^dictation: ready: $([regex]::Escape($model)) " -and (Get-Date) -lt $deadline -and -not (Start-Sleep -Seconds 10))
Write-Host $dictation
$dictationReady = $dictation -match "^dictation: ready: "
$environment = $false
if ($Want -eq "installed" -and -not $dictationReady) {
    $resultFile = Join-Path $env:RUNNER_TEMP "dictation-verdict.json"
    & (Join-Path $PSScriptRoot "..\acceptance\dictation.ps1") -Goblins (Join-Path $ceoHome "bin\goblins.exe") -CeoHome $ceoHome -Summary $env:GITHUB_STEP_SUMMARY -ResultFile $resultFile
    $dictationExit = $LASTEXITCODE
    $verdict = Get-Content -Raw -LiteralPath $resultFile | ConvertFrom-Json
    if ($dictationExit -ne 0) {
        $problems += @($verdict.Failures)
    } elseif ($verdict.Outcome -eq "environment") {
        $environment = $true
        Write-Host "the runner could not download dictation's model, so its readiness is an environment result, not a failure of the change: $($verdict.Environment -join '; ')"
    } else {
        $dictationReady = $true
    }
} elseif ($Want -eq "rolled-back" -and -not $dictationReady) {
    # A rollback restores the build from before, whose pinned asset upstream
    # may have re-uploaded since this checkout pinned it: the cache then
    # reads as missing, an expected difference and not a loss. Only a
    # verified part the rollback dropped is the failure.
    $beforeParts = ([regex]::Matches([string]$before.dictation, [regex]::Escape("verified.json"))).Count
    $left = @(Get-ChildItem -LiteralPath (Join-Path $ceoHome "caches\voice") -Directory | Where-Object { Test-Path -LiteralPath (Join-Path $_.FullName "verified.json") })
    if ($left.Count -lt $beforeParts) {
        $problems += "the rollback lost a verified part: caches\voice holds $($left.Count) verified parts, want $beforeParts as before: $($left.Name -join ', ')"
    } else {
        $cacheSums = @($left | ForEach-Object {
                $record = Get-Content -Raw -LiteralPath (Join-Path $_.FullName "verified.json") | ConvertFrom-Json
                "$($_.Name) $($record.sha256)"
            }) -join ", "
        $pin = (Get-Content -Raw config\voice.json | ConvertFrom-Json).model
        Write-Host "dictation is not ready after the rollback because the build it restores pins a different asset than this checkout pins, an expected difference: caches\voice holds $cacheSums; config\voice.json pins $($pin.name) $($pin.version) $($pin.sha256)"
    }
}
if ($replaced -and -not $environment) {
    $left = @(Get-ChildItem -LiteralPath (Join-Path $ceoHome "caches\voice") -Directory | Where-Object { Test-Path -LiteralPath (Join-Path $_.FullName "verified.json") })
    if ($left.Count -ne 2) { $problems += "caches\voice holds $($left.Count) verified parts after the new model's fetch, want its engine and model alone: $($left.Name -join ', ')" }
}

if ($problems.Count -gt 0) { throw "the update from $($before.version) did not keep what it must:`n- $($problems -join "`n- ")" }
$dictationSaid = "dictation is as it must be"
if ($environment) { $dictationSaid = "dictation's readiness is an environment result on this runner, so it is no verdict on the change" }
$moved = ""
if ($isCrossing) { $moved = ", moved to goblins with the CEO and the goblin restarted under the new names," }
Write-Host "$ran from $($before.version): $Want$moved and the home, the CEO, the goblin, the settings, the project, the credential and the goblin's work are as they must be; $dictationSaid."
$global:LASTEXITCODE = 0
