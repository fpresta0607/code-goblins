# cutover_and_check.ps1 - the one move a home that a release from before the
# rename runs makes to goblins, since such a release cannot update itself
# across the rename: with the fleet paused, this commit's build lists the
# cutover with --dry-run and makes that plan with --plan, then goblins resume
# brings the board, the CEO and the goblin back under goblins. The fleet pauses as a
# reboot pauses it: the supervisor stops when asked, each terminal, the
# CEO's and the goblin's, ends, its harness and then its host, by the pids its
# record names, and the desktop window closes, the board's own window that
# the release's install opened as its last step and that outlives a stopped
# supervisor. The cutover refuses while it runs from the home's bin and asks
# for it to be closed first, as the person running the cutover does; the
# window keeps no record, so it is found by its program's full path and ended
# by its pid.
# The home, the person's settings, project record, stored
# credential and the goblin's work are as fleet_at_work.ps1 -PreRename found
# them, and the fleet's facts under goblins replace <facts>, for the updates
# after it to keep.
#
#   .\tests\update\cutover_and_check.ps1 -Goblins <this commit's goblins.exe> -Project <checkout> -Facts <file>
param(
    [Parameter(Mandatory = $true)][string]$Goblins,
    [Parameter(Mandatory = $true)][string]$Project,
    [Parameter(Mandatory = $true)][string]$Facts
)

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "fleet_facts.ps1")
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")
$oldCommand, $oldSettingPrefix, $null = Get-BuildNames -PreRename
$ceoHome = [Environment]::GetEnvironmentVariable("${oldSettingPrefix}HOME", "User")
Set-Item -LiteralPath "env:${oldSettingPrefix}HOME" -Value $ceoHome
$state = Join-Path $ceoHome "state"
$bin = Join-Path $ceoHome "bin"
$before = Get-Content -Raw -LiteralPath $Facts | ConvertFrom-Json

& (Join-Path $bin "$oldCommand.exe") stop
if ($LASTEXITCODE -ne 0) { throw "$oldCommand stop exited with code $LASTEXITCODE" }
foreach ($record in Get-ChildItem -LiteralPath (Join-Path $state "hosts") -Filter *.json) {
    $terminal = Get-Content -Raw -LiteralPath $record.FullName | ConvertFrom-Json
    foreach ($id in $terminal.child_pid, $terminal.host_pid) {
        if ($id -gt 0 -and (Get-Process -Id $id -ErrorAction SilentlyContinue)) { Stop-Process -Id $id -Force }
    }
}
$window = Join-Path $bin "goblins-window.exe"
foreach ($process in @(Get-Process | Where-Object { $_.Path -and [string]::Equals($_.Path, $window, [StringComparison]::OrdinalIgnoreCase) })) {
    Stop-Process -Id $process.Id -Force
}
# The cutover refuses while any program runs from the home's bin, naming each
# with its pid; a terminal's host takes a moment to end.
$deadline = (Get-Date).AddSeconds(60)
do {
    $running = @(Get-Process | Where-Object { $_.Path -and $_.Path.StartsWith("$bin\", [StringComparison]::OrdinalIgnoreCase) })
} while ($running.Count -gt 0 -and (Get-Date) -lt $deadline -and -not (Start-Sleep -Seconds 1))
if ($running.Count -gt 0) { throw "a minute after the fleet paused these still run from the home's bin: $(($running | ForEach-Object { "$($_.Path) (pid $($_.Id))" }) -join ', ')" }

$dryRun = @(& $Goblins cutover --dry-run)
$code = $LASTEXITCODE
$dryRun | Write-Host
if ($code -ne 0) { throw "goblins cutover --dry-run exited with code $code" }
$problems = @()
# Each kind of change a release's install and the fleet at work leave to
# move heads its group.
foreach ($kind in "program", "setting", "harness file", "skill", "credential", "state file") {
    if (-not ($dryRun -match "^$([regex]::Escape($kind)):$")) { $problems += "the dry run lists no $kind change" }
}
$plan = @($dryRun | Select-String -Pattern '^plan: (\S+)$')
if ($plan.Count -ne 1) { throw "the dry run printed $($plan.Count) plan: lines, want one" }
$digest = $plan[0].Matches[0].Groups[1].Value

$made = @(& $Goblins cutover --plan $digest)
$code = $LASTEXITCODE
$made | Write-Host
if ($code -ne 0) { throw "goblins cutover --plan $digest exited with code $code" }
if (-not ($made -match '^undo: ')) { $problems += "the cutover printed no undo command" }
if ([Environment]::GetEnvironmentVariable("GOBLINS_HOME", "User") -ne $ceoHome) { $problems += "the user setting GOBLINS_HOME is '$([Environment]::GetEnvironmentVariable("GOBLINS_HOME", "User"))', want the home $ceoHome" }
if ($null -ne [Environment]::GetEnvironmentVariable("${oldSettingPrefix}HOME", "User")) { $problems += "the user setting ${oldSettingPrefix}HOME is still there" }
if (Test-Path -LiteralPath (Join-Path $bin "$oldCommand.exe")) { $problems += "bin\$oldCommand.exe is still there" }
$built = (Get-FileHash -LiteralPath $Goblins -Algorithm SHA256).Hash.ToLowerInvariant()

# A new terminal sees the settings the cutover moved; a running step does not.
Remove-Item -LiteralPath "env:${oldSettingPrefix}HOME"
$env:GOBLINS_HOME = $ceoHome
Set-Content -LiteralPath "$Facts.no-input" -Value $null
$resume = Start-Process -FilePath (Join-Path $bin "goblins.exe") -ArgumentList "resume", "--harness", "pi" -NoNewWindow -PassThru `
    -RedirectStandardInput "$Facts.no-input" -RedirectStandardOutput "$Facts.resume.log" -RedirectStandardError "$Facts.resume.err"
if (-not $resume.WaitForExit(180000)) {
    Stop-Process -Id $resume.Id -Force
    throw "goblins resume did not end within 3 minutes"
}
Get-Content -LiteralPath "$Facts.resume.log", "$Facts.resume.err" | Write-Host
# The cutover renamed the CEO's terminal's record, which names the host the
# pause ended, so each terminal is back once its record's host runs.
$deadline = (Get-Date).AddMinutes(2)
while ($true) {
    $ended = @(foreach ($id in "ceo", "demo-goblin") {
            $record = Join-Path $state "hosts\$id.json"
            if (-not (Test-Path -LiteralPath $record) -or -not (Get-Process -Id (Get-Content -Raw -LiteralPath $record | ConvertFrom-Json).host_pid -ErrorAction SilentlyContinue)) { $id }
        })
    if ($ended.Count -eq 0) { break }
    if ((Get-Date) -gt $deadline) { throw "within 2 minutes of goblins resume these terminals did not run again: $($ended -join ', ')" }
    Start-Sleep -Seconds 1
}

# This commit pins a newer dictation model than the release before it, which
# the board goblins resume started fetches once it serves; the fleet's facts
# are read once that settles, so the updates after it find dictation as it
# was. Whether it is ready is the updates' to judge.
$model = (Get-Content -Raw config\voice.json | ConvertFrom-Json).model.name
$deadline = (Get-Date).AddMinutes(15)
do {
    $dictation = @(& (Join-Path $bin "goblins.exe") doctor) -match "^dictation:"
    $global:LASTEXITCODE = 0
} while ($dictation -notmatch "^dictation: ready: $([regex]::Escape($model)) " -and (Get-Date) -lt $deadline -and -not (Start-Sleep -Seconds 10))
Write-Host $dictation

$after = Get-FleetFacts -Project $Project
$after | Format-Table -AutoSize -Wrap | Out-String -Width 400 | Write-Host
foreach ($running in "board", "goblins host", "ceo harness", "demo-goblin host", "demo-goblin harness") {
    if ($after[$running] -match "not running") { $problems += "after the cutover the $running is not running: $($after[$running])" }
}
if ($after.version -notmatch '^goblins ') { $problems += "goblins version prints '$($after.version)', want goblins'" }
foreach ($program in "commandExe", "boardExe") {
    if ($after[$program] -ne $built) { $problems += "after the cutover $program is $($after[$program]), want this commit's $built" }
}
# The credential listing's first line names its store, which for the file
# store is a folder the cutover renames; the credentials are the lines after.
$stored = { param([string]$listing) @($listing -split "`r?`n" | Select-Object -Skip 1) -join "; " }
if ((& $stored $after.credentials) -ne (& $stored $before.credentials)) { $problems += "the stored credentials changed across the cutover: '$($before.credentials)' became '$($after.credentials)'" }
foreach ($kept in "home", "allowanceFloor", "claudeSetting", "project", "goblinWork") {
    if ($after[$kept] -ne $before.$kept) { $problems += "$kept changed across the cutover: '$($before.$kept)' became '$($after[$kept])'" }
}
if ($problems.Count -gt 0) { throw "the cutover from $($before.version) did not do what it must:`n- $($problems -join "`n- ")" }
[IO.File]::WriteAllText($Facts, (ConvertTo-Json -InputObject $after))
Write-Host "goblins cutover from $($before.version): the home runs goblins, and the board, the CEO and the goblin work under it with the home, the settings, the project, the credential and the goblin's work as they were."
$global:LASTEXITCODE = 0
