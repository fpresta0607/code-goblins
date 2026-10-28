# fleet_facts.ps1 - what an update of a person's Code Goblins must keep, read
# the same way before and after it: the build the home runs and the board
# serves, the CFO's and the goblin's terminals by process, the dictation
# engine and model on disk, the person's settings, project record, stored
# credential and the goblin's work.
#
#   . .\tests\update\fleet_facts.ps1
#   $facts = Get-FleetFacts -Project <checkout>

function Get-ProcessFact([int]$Id) {
    $process = Get-Process -Id $Id -ErrorAction SilentlyContinue
    if (-not $process) { return "pid $Id, not running" }
    "pid $Id since $($process.StartTime.ToUniversalTime().ToString('o'))"
}

function Get-FleetFacts([Parameter(Mandatory = $true)][string]$Project) {
    $cfoHome = [Environment]::GetEnvironmentVariable("CFO_HOME", "User")
    $state = Join-Path $cfoHome "state"
    $bin = Join-Path $cfoHome "bin"
    $facts = [ordered]@{}
    $facts.home = $cfoHome
    $facts.version = ((& (Join-Path $bin "goblins.exe") version) | Out-String).Trim()
    $facts.cfoExe = (Get-FileHash (Join-Path $bin "cfo.exe") -Algorithm SHA256).Hash.ToLowerInvariant()

    # The board's record names the supervisor, which answers /api/alive with
    # its pid; the program that pid runs is the build the board serves.
    $board = Get-Content -Raw -LiteralPath (Join-Path $state "board.json") | ConvertFrom-Json
    $alive = Invoke-RestMethod -Uri "$($board.url)/api/alive" -TimeoutSec 10
    $served = (Get-Process -Id $alive.pid -ErrorAction Stop).Path
    $facts.boardUrl = $board.url
    $facts.board = Get-ProcessFact $alive.pid
    $facts.boardExe = (Get-FileHash -LiteralPath $served -Algorithm SHA256).Hash.ToLowerInvariant()

    # Each native terminal's record names its host and the harness it runs.
    foreach ($terminal in "cfo", "demo-goblin") {
        $record = Get-Content -Raw -LiteralPath (Join-Path $state "hosts\$terminal.json") | ConvertFrom-Json
        $facts["$terminal host"] = Get-ProcessFact $record.host_pid
        $facts["$terminal harness"] = Get-ProcessFact $record.child_pid
    }

    $voice = Join-Path $cfoHome "caches\voice"
    $facts.dictation = @(Get-ChildItem -LiteralPath $voice -Recurse -File | Sort-Object FullName | ForEach-Object {
            "$($_.FullName.Substring($voice.Length + 1)) $($_.Length) bytes, written $($_.LastWriteTimeUtc.ToString('o'))"
        }) -join "; "

    $facts.allowanceFloor = ((& (Join-Path $bin "goblins.exe") allowance-floor) | Out-String).Trim()
    $claudeSettings = Get-Content -Raw -LiteralPath (Join-Path $env:USERPROFILE ".claude\settings.json") | ConvertFrom-Json
    $facts.claudeSetting = "$($claudeSettings.codeGoblinsUpdateProof)"
    $facts.project = ((& (Join-Path $bin "goblins.exe") project show $Project) | Out-String).Trim()
    $facts.credentials = ((& (Join-Path $bin "goblins.exe") auth list) | Out-String).Trim()

    $worktree = Join-Path $cfoHome "worktrees\$(Split-Path -Leaf $Project)\demo-goblin"
    $facts.goblinWork = @(
        (git -C $worktree log -1 --format=%H%x20%s)
        (Get-Content -Raw -LiteralPath (Join-Path $worktree "uncommitted.txt")).Trim()
    ) -join "; "
    $global:LASTEXITCODE = 0
    $facts
}
