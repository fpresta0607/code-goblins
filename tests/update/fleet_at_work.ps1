# fleet_at_work.ps1 - a person's Code Goblins at work, as an update finds it:
# one goblin in their checkout with work it has not landed and a stored
# credential (tests\uninstall\one_goblin_session.ps1), the CEO running in its
# terminal, a setting of the fleet's and one of Claude Code's of their own,
# and their project's record. It writes what an update must keep, as
# fleet_facts.ps1 reads it, to <facts>.
#
# pi has no account on a runner, so the goblin and the CEO run pi signed in
# to the stand-in model, which keeps running for the update to find them at
# work; its pid is in <facts>.stand-in.pid for the job to end it by.
# -PreRename runs it under a release from before the rename, by the names
# that release knows itself by.
#
#   .\tests\update\fleet_at_work.ps1 -Project <checkout> -Facts <file> [-PreRename]
param(
    [Parameter(Mandatory = $true)][string]$Project,
    [Parameter(Mandatory = $true)][string]$Facts,
    [switch]$PreRename
)

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "fleet_facts.ps1")

# pi downloads fd and rg from GitHub the first time it starts, and until they
# land it hands a submitted prompt back to its editor. v0.5.15 presses Enter
# once, so a slow download fails the job. pi takes a file of either name as
# the tool and nothing here runs one, so an empty file saves the download.
$piTools = Join-Path $env:USERPROFILE ".pi\agent\bin"
New-Item -ItemType File -Force -Path (Join-Path $piTools "fd.exe"), (Join-Path $piTools "rg.exe") | Out-Null

& (Join-Path $PSScriptRoot "..\uninstall\one_goblin_session.ps1") -Project $Project -PreRename:$PreRename
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")
$command, $settingPrefix, $ceoTerminal = Get-BuildNames -PreRename:$PreRename
$ceoHome = [Environment]::GetEnvironmentVariable("${settingPrefix}HOME", "User")
Set-Item -LiteralPath "env:${settingPrefix}HOME" -Value $ceoHome
$state = Join-Path $ceoHome "state"

# The session ended the stand-in once the goblin was at work; the CEO needs
# it to start, and both keep it through the update.
$port = 8767
$standIn = Start-Process -FilePath python -ArgumentList (Join-Path $PSScriptRoot "..\uninstall\stand_in_model.py"), $port -WindowStyle Hidden -PassThru
Set-Content -LiteralPath "$Facts.stand-in.pid" -Value $standIn.Id
$deadline = (Get-Date).AddSeconds(30)
while ($true) {
    try { Invoke-WebRequest "http://127.0.0.1:$port/v1/models" -UseBasicParsing | Out-Null; break }
    catch { if ((Get-Date) -gt $deadline) { throw "the stand-in model did not answer within 30 seconds" }; Start-Sleep -Milliseconds 250 }
}

& goblins allowance-floor claude 7
if ($LASTEXITCODE -ne 0) { throw "goblins allowance-floor exited with code $LASTEXITCODE" }
$settingsFile = Join-Path $env:USERPROFILE ".claude\settings.json"
$settings = Get-Content -Raw -LiteralPath $settingsFile | ConvertFrom-Json
$settings | Add-Member -NotePropertyName codeGoblinsUpdateProof -NotePropertyValue "kept" -Force
[IO.File]::WriteAllText($settingsFile, (ConvertTo-Json -InputObject $settings -Depth 20))
& { $ErrorActionPreference = "Continue"; & goblins project show $Project 2>$null | Out-Null }
if ($LASTEXITCODE -ne 0) {
    & goblins project init $Project
    if ($LASTEXITCODE -ne 0) { throw "goblins project init exited with code $LASTEXITCODE" }
}

# The quick start, told the agent, starts the CEO in its terminal and ends on
# a screen that asks which to open; with no console to ask in it ends there,
# and the CEO keeps running.
Set-Content -LiteralPath "$Facts.no-input" -Value $null
$quickStart = Start-Process -FilePath (Join-Path $ceoHome "bin\goblins.exe") -ArgumentList "--harness", "pi" -NoNewWindow -PassThru `
    -RedirectStandardInput "$Facts.no-input" -RedirectStandardOutput "$Facts.quick-start.log" -RedirectStandardError "$Facts.quick-start.err"
if (-not $quickStart.WaitForExit(180000)) {
    Stop-Process -Id $quickStart.Id -Force
    throw "goblins --harness pi did not end within 3 minutes"
}
Get-Content -LiteralPath "$Facts.quick-start.log", "$Facts.quick-start.err" | Write-Host
$deadline = (Get-Date).AddMinutes(2)
while (-not (Test-Path -LiteralPath (Join-Path $state "hosts\$ceoTerminal.json"))) {
    if ((Get-Date) -gt $deadline) { throw "the CEO's terminal did not start within 2 minutes of the quick start" }
    Start-Sleep -Seconds 1
}

$before = Get-FleetFacts -Project $Project -PreRename:$PreRename
$before | Format-Table -AutoSize -Wrap | Out-String -Width 400 | Write-Host
foreach ($running in "board", "ceo host", "ceo harness", "demo-goblin host", "demo-goblin harness") {
    if ($before[$running] -match "not running") { throw "before the update the $running is not running: $($before[$running])" }
}
if ($before.credentials -notmatch "DEMO_TOKEN") { throw "the stored credential is not listed before the update: $($before.credentials)" }
[IO.File]::WriteAllText($Facts, (ConvertTo-Json -InputObject $before))
$global:LASTEXITCODE = 0
