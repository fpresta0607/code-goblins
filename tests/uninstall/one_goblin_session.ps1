# one_goblin_session.ps1 - a short first session of Code Goblins on a clean
# runner, as a person's first use is: one goblin in their own checkout,
# briefed and spawned as the CFO does, with work it has not landed, and one
# stored credential. The uninstall after it must keep that work at the
# person's choice and leave nothing else.
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

$brief = & goblins brief demo-goblin --project $Project --mode local-only
if ($LASTEXITCODE -ne 0) { throw "goblins brief exited with code $LASTEXITCODE" }

# pi starts with no account, so its terminal reaches its composer and takes
# the brief; a harness that stops at a sign-in leaves the goblin started all
# the same, which is what an uninstall meets.
& goblins spawn demo-goblin --project $Project --brief $brief --harness pi --mode local-only --title "Demo goblin"
Write-Host "goblins spawn exited with code $LASTEXITCODE"

$worktree = Join-Path $env:CFO_HOME "worktrees\$(Split-Path -Leaf $Project)\demo-goblin"
if (Test-Path -LiteralPath $worktree) {
    Set-Content -LiteralPath (Join-Path $worktree "unpushed.txt") -Value "a commit the goblin made and never pushed"
    git -C $worktree add unpushed.txt
    git -C $worktree -c user.name=Goblin -c user.email=goblin@example.invalid commit -q -m "work not pushed"
    Set-Content -LiteralPath (Join-Path $worktree "uncommitted.txt") -Value "work the goblin has not committed"
    git -C $worktree status --short --branch
}
else {
    Write-Host "the goblin has no worktree at $worktree"
}

"a value for the demo" | & goblins auth store DEMO_TOKEN
Write-Host "goblins auth store exited with code $LASTEXITCODE"
& goblins fleet-view
$global:LASTEXITCODE = 0
