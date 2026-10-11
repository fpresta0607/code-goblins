# one_goblin_session.ps1 - a short first session of Code Goblins on a clean
# runner, as a person's first use is: one goblin in their own checkout,
# briefed and spawned as the CFO does, with work it has not landed, and one
# stored credential. The uninstall after it must keep that work at the
# person's choice and leave nothing else.
#
# pi has no account on a runner, so it is signed in to a stand-in model on
# this machine, as a person signs pi in to theirs after installing it. The
# stand-in runs while the goblin starts and is ended, by the pid this script
# started it under, once the goblin is at work.
#
# Run it after the install, in a new step, with the checkout it names made
# before the snapshot the uninstall is compared with:
#
#   .\tests\uninstall\one_goblin_session.ps1 -Project <checkout>
param([Parameter(Mandatory = $true)][string]$Project)

# A native program's stderr is no failure here: each exit code is read.
$ErrorActionPreference = "Continue"

# A new terminal sees what the install wrote to the registry; a new step
# does not.
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")
$env:CFO_HOME = [Environment]::GetEnvironmentVariable("CFO_HOME", "User")

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

$brief = & goblins brief demo-goblin --project $Project --mode local-only
if ($LASTEXITCODE -ne 0) { throw "goblins brief exited with code $LASTEXITCODE" }
& goblins spawn demo-goblin --project $Project --brief $brief --harness pi --mode local-only --title "Demo goblin"
Write-Host "goblins spawn exited with code $LASTEXITCODE"
Stop-Process -Id $standIn.Id -ErrorAction SilentlyContinue

$worktree = Join-Path $env:CFO_HOME "worktrees\$(Split-Path -Leaf $Project)\demo-goblin"
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
