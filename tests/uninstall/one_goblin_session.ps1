# one_goblin_session.ps1 - a short first session of Code Goblins on a clean
# runner, as a person's first use is: one goblin in their own checkout,
# briefed and spawned as the CEO does, with work it has not landed, and one
# stored credential. The uninstall after it must keep that work at the
# person's choice and leave nothing else.
#
# pi has no account on a runner, so it is signed in to a stand-in model on
# this machine, as a person signs pi in to theirs after installing it. The
# stand-in runs while the goblin starts and is ended, by the pid this script
# started it under, once the goblin is at work.
#
# Run it after the install, in a new step, with the checkout it names made
# before the snapshot the uninstall is compared with. -PreRename runs it under
# a release from before the rename, which reads its home by its old setting:
#
#   .\tests\uninstall\one_goblin_session.ps1 -Project <checkout> [-PreRename]
#
# -SelfTest runs the goblin's start against a stand-in goblins command made up
# here, in a home of its own, so a fixture proves it without a runner.
param(
    [string]$Project = '',
    [switch]$PreRename,
    [string]$SelfTest = ''
)

# A native program's stderr is no failure here: each exit code is read.
$ErrorActionPreference = "Continue"

# Start-DemoGoblin spawns demo-goblin in $Project from a brief it writes to
# $Brief, outside the home's data folder: the board's scheduler starts a brief
# it finds there by itself, and its start beside this spawn fails one of the
# two. A failed spawn ends the session there, so the job reports it where it
# happens and not as a goblin a later step cannot find. A release from before
# brief readiness ignores the ready: line; a build since admits only a brief
# that carries it.
function Start-DemoGoblin([string]$Project, [string]$Brief) {
    @"
# Brief demo-goblin

## Project

$Project

## Task

Remain available for the uninstall smoke test without changing the project.

## Acceptance criteria

Start in an isolated worktree so the uninstall test can verify retained work.

## Constraints

Do not change project files or use project credentials or MCP servers.

## Authentication

credentials: none
mcp: none

## Delivery

kind: ship
mode: local-only
ready: true
"@ | Set-Content -LiteralPath $Brief
    & goblins spawn demo-goblin --project $Project --brief $Brief --harness pi --mode local-only --title "Demo goblin"
    if ($LASTEXITCODE -ne 0) { throw "goblins spawn exited with code $LASTEXITCODE" }
}

if ($SelfTest -ne '') {
    function Assert-SelfTest([bool]$Condition, [string]$Message) {
        if (-not $Condition) { throw "one goblin session self-test ${SelfTest}: $Message" }
    }
    $scratch = Join-Path ([IO.Path]::GetTempPath()) "one-goblin-session-$([guid]::NewGuid())"
    $ceoHome = Join-Path $scratch "home"
    $queued = Join-Path $ceoHome "data\demo-goblin\brief.md"
    $sessionBrief = Join-Path $scratch "demo-goblin-brief.md"
    $spawns = [Collections.Generic.List[object]]::new()
    $spawnExit = 0
    # The stand-in leaves in the home what the real goblins does: brief
    # writes data\<id>\brief.md, which the board's scheduler starts by itself
    # once nothing else has, and prints its path; brief ready marks it ready;
    # spawn starts from the brief it is given. Each spawn is recorded with the
    # brief it read and whether the queued brief was there as it ran.
    function goblins {
        switch ($args[0]) {
            'brief' {
                $global:LASTEXITCODE = 0
                if ($args[1] -eq 'ready') {
                    (Get-Content -Raw -LiteralPath $queued).Replace("ready: false", "ready: true") | Set-Content -LiteralPath $queued
                    return
                }
                New-Item -ItemType Directory -Force -Path (Split-Path -Parent $queued) | Out-Null
                Set-Content -LiteralPath $queued -Value "ready: false"
                return $queued
            }
            'spawn' {
                $brief = $args[[array]::IndexOf($args, '--brief') + 1]
                $spawns.Add([pscustomobject]@{ Brief = $brief; IsQueued = (Test-Path -LiteralPath $queued); Text = (Get-Content -Raw -LiteralPath $brief) })
                $global:LASTEXITCODE = $spawnExit
            }
            default { throw "the stand-in goblins has no $($args[0])" }
        }
    }
    try {
        New-Item -ItemType Directory -Force -Path (Join-Path $ceoHome "data") | Out-Null
        switch ($SelfTest) {
            'spawn-starts-from-a-brief-the-board-does-not-queue' {
                Start-DemoGoblin -Project (Join-Path $scratch "demo") -Brief $sessionBrief
                Assert-SelfTest ($spawns.Count -eq 1) "want one spawn, got $($spawns.Count)"
                Assert-SelfTest (-not $spawns[0].IsQueued) "the spawn ran while $queued was there, a brief the board's scheduler starts by itself beside it"
                Assert-SelfTest ($spawns[0].Brief -eq $sessionBrief) "want the spawn to read $sessionBrief, got $($spawns[0].Brief)"
                Assert-SelfTest ($spawns[0].Text -match '(?m)^ready: true\r?$') "the brief the spawn read is not marked ready, so a build that admits only a ready brief refuses it:`n$($spawns[0].Text)"
            }
            'failed-spawn-ends-the-session' {
                $spawnExit = 1
                $failure = ''
                try { Start-DemoGoblin -Project (Join-Path $scratch "demo") -Brief $sessionBrief }
                catch { $failure = $_.Exception.Message }
                Assert-SelfTest ($failure -eq "goblins spawn exited with code 1") "want the session to end on the failed spawn with its exit code, got '$failure'"
            }
            default { throw "Unknown one goblin session self-test $SelfTest." }
        }
    }
    finally {
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $scratch
    }
    Write-Output "one goblin session self-test $SelfTest passed."
    exit 0
}

if (-not $Project) { throw "one_goblin_session.ps1 needs -Project." }

# A new terminal sees what the install wrote to the registry; a new step
# does not.
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")
$homeSetting = "GOBLINS_HOME"
if ($PreRename) {
    . (Join-Path $PSScriptRoot "..\pre_rename_build.ps1")
    $homeSetting = "${PreRenameSettingPrefix}HOME"
}
$ceoHome = [Environment]::GetEnvironmentVariable($homeSetting, "User")
Set-Item -LiteralPath "env:$homeSetting" -Value $ceoHome

$port = 8767
$pi = Join-Path $env:USERPROFILE ".pi\agent"
New-Item -ItemType Directory -Force -Path $pi | Out-Null
$models = [ordered]@{ providers = [ordered]@{ "stand-in" = [ordered]@{
    baseUrl = "http://127.0.0.1:$port/v1"; api = "openai-completions"; apiKey = "stand-in"
    compat = [ordered]@{ supportsDeveloperRole = $false; supportsReasoningEffort = $false }
    models = @([ordered]@{ id = "stand-in" })
} } }
[IO.File]::WriteAllText((Join-Path $pi "models.json"), (ConvertTo-Json -InputObject $models -Depth 6))
$settingsFile = Join-Path $pi "settings.json"
$settings = [ordered]@{}
if (Test-Path -LiteralPath $settingsFile) {
    foreach ($property in (Get-Content -Raw -LiteralPath $settingsFile | ConvertFrom-Json).PSObject.Properties) {
        $settings[$property.Name] = $property.Value
    }
}
$settings["defaultProvider"] = "stand-in"
$settings["defaultModel"] = "stand-in"
[IO.File]::WriteAllText($settingsFile, (ConvertTo-Json -InputObject $settings -Depth 6))
$standIn = Start-Process -FilePath python -ArgumentList (Join-Path $PSScriptRoot "stand_in_model.py"), $port -WindowStyle Hidden -PassThru
$deadline = (Get-Date).AddSeconds(30)
while ($true) {
    try { Invoke-WebRequest "http://127.0.0.1:$port/v1/models" -UseBasicParsing | Out-Null; break }
    catch { if ((Get-Date) -gt $deadline) { throw "the stand-in model did not answer within 30 seconds" }; Start-Sleep -Milliseconds 250 }
}

# A build that selects delivery at intake reads the demo project's record, a
# local-only posture with a check it can run, as the CEO records it. An
# earlier release, as the update job runs, takes the mode alone, and its
# loader refuses a record that names a posture.
if ((& goblins brief demo-goblin -h 2>&1 | Out-String) -match "-surface") {
    $name = Split-Path -Leaf $Project
    $record = Join-Path $ceoHome "data\projects\$name\project.json"
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $record) | Out-Null
    [IO.File]::WriteAllText($record, '{"project":' + (ConvertTo-Json $name) + ',"delivery":{"posture":"local-only"},"verification":{"fast":[["git","status"]]}}')
}
try { Start-DemoGoblin -Project $Project -Brief (Join-Path $env:RUNNER_TEMP "demo-goblin-brief.md") }
finally { Stop-Process -Id $standIn.Id -ErrorAction SilentlyContinue }

$worktree = Join-Path $ceoHome "worktrees\$(Split-Path -Leaf $Project)\demo-goblin"
if (Test-Path -LiteralPath $worktree) {
    Set-Content -LiteralPath (Join-Path $worktree "unpushed.txt") -Value "a commit the goblin made and never pushed"
    git -C $worktree add unpushed.txt
    git -C $worktree -c user.name=Goblin -c user.email=goblin@example.invalid commit -q -m "work not pushed"
    Set-Content -LiteralPath (Join-Path $worktree "uncommitted.txt") -Value "work the goblin has not committed"
    git -C $worktree status --short --branch
}
else {
    throw "the goblin has no worktree at $worktree, so the uninstall would meet no goblin"
}

"a value for the demo" | & goblins auth store DEMO_TOKEN
if ($LASTEXITCODE -ne 0) { throw "goblins auth store exited with code $LASTEXITCODE" }
& goblins fleet-view
$global:LASTEXITCODE = 0
