[CmdletBinding()]
param(
    [string]$SelfTest = ''
)

$ErrorActionPreference = 'Stop'

if ($env:CFO_PLAN3_REAL -ne '1') {
    throw 'Plan 3 Windows acceptance is opt-in. Set CFO_PLAN3_REAL=1 to run it in a disposable environment.'
}

function Invoke-Checked {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$Description
    )

    $output = @(& $FilePath @Arguments 2>&1)
    if ($LASTEXITCODE -ne 0) {
        throw "$Description failed with exit code $LASTEXITCODE.`n$($output -join [Environment]::NewLine)"
    }
    return $output
}

function Get-FullPath {
    param([Parameter(Mandatory = $true)][string]$Path)
    return [System.IO.Path]::GetFullPath($Path)
}

function Test-ContainedPath {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Path
    )

    $fullRoot = (Get-FullPath $Root).TrimEnd([System.IO.Path]::DirectorySeparatorChar, [System.IO.Path]::AltDirectorySeparatorChar)
    $fullPath = Get-FullPath $Path
    $prefix = $fullRoot + [System.IO.Path]::DirectorySeparatorChar
    return $fullPath.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase)
}

function Assert-ContainedPath {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Description
    )

    if (-not (Test-ContainedPath -Root $Root -Path $Path)) {
        throw "ACCEPTANCE BLOCKER: $Description escapes disposable root. root=$(Get-FullPath $Root) path=$(Get-FullPath $Path)"
    }
}

function Assert-ObjectProperty {
    param(
        [Parameter(Mandatory = $true)]$Object,
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Description
    )

    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property -or $null -eq $property.Value -or [string]::IsNullOrWhiteSpace([string]$property.Value)) {
        throw "ACCEPTANCE BLOCKER: $Description is missing JSON property $Name."
    }
    return $property.Value
}

function Invoke-HerdrJson {
    param(
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$Description
    )

    $output = @(& herdr @Arguments 2>&1)
    if ($LASTEXITCODE -ne 0) {
        throw "ACCEPTANCE BLOCKER: $Description failed. The installed Herdr preview must support this explicit --json operation without a fallback.`n$($output -join [Environment]::NewLine)"
    }
    $text = $output -join [Environment]::NewLine
    try {
        return $text | ConvertFrom-Json -ErrorAction Stop
    }
    catch {
        throw "ACCEPTANCE BLOCKER: $Description returned non-JSON output. The installed Herdr preview is incompatible with Plan 3's subprocess JSON contract.`n$text"
    }
}

function Read-TaskMeta {
    param([Parameter(Mandatory = $true)][string]$Path)

    $meta = @{}
    foreach ($line in Get-Content -LiteralPath $Path -ErrorAction Stop) {
        $separator = $line.IndexOf('=')
        if ($separator -lt 1) {
            continue
        }
        $meta[$line.Substring(0, $separator)] = $line.Substring($separator + 1)
    }
    return $meta
}

function Assert-MetaValue {
    param(
        [Parameter(Mandatory = $true)][hashtable]$Meta,
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Description
    )

    if (-not $Meta.ContainsKey($Name) -or [string]::IsNullOrWhiteSpace($Meta[$Name])) {
        throw "ACCEPTANCE BLOCKER: $Description metadata is missing $Name."
    }
    return $Meta[$Name]
}

function Convert-StaleSecondsToGoDuration {
    param([Parameter(Mandatory = $true)][int64]$Seconds)

    if ($Seconds -le 0) {
        return '-'
    }
    $hours = [math]::Floor($Seconds / 3600)
    $remaining = $Seconds % 3600
    $minutes = [math]::Floor($remaining / 60)
    $seconds = $remaining % 60
    if ($hours -gt 0) {
        return "${hours}h${minutes}m${seconds}s"
    }
    if ($minutes -gt 0) {
        return "${minutes}m${seconds}s"
    }
    return "${seconds}s"
}

function Convert-MarkdownCell {
    param($Value)

    if ($null -eq $Value -or [string]::IsNullOrWhiteSpace([string]$Value)) {
        return '-'
    }
    return ([string]$Value).Replace("`r", ' ').Replace("`n", ' ').Replace('|', '\\|')
}

function Convert-LastSeenToMarkdown {
    param([Parameter(Mandatory = $true)]$Value)

    try {
        return ([DateTimeOffset]$Value).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ss'Z'", [Globalization.CultureInfo]::InvariantCulture)
    }
    catch {
        throw "ACCEPTANCE BLOCKER: fleet JSON last_seen is not an RFC3339 timestamp: $Value"
    }
}

function Convert-EndpointToMarkdown {
    param(
        [string]$Target,
        $Exists
    )

    if ($null -eq $Exists) {
        $verdict = 'unknown'
    }
    elseif ([bool]$Exists) {
        $verdict = 'present'
    }
    else {
        $verdict = 'absent'
    }
    if ([string]::IsNullOrWhiteSpace($Target)) {
        return $verdict
    }
    return ((Convert-MarkdownCell $Target) + ' (' + $verdict + ')')
}

function Convert-BooleanToMarkdown {
    param([Parameter(Mandatory = $true)][bool]$Value)

    if ($Value) {
        return 'yes'
    }
    return 'no'
}

function Assert-FleetProjectionParity {
    param(
        [Parameter(Mandatory = $true)]$Row,
        [Parameter(Mandatory = $true)]$Worker,
        [Parameter(Mandatory = $true)][string]$Markdown
    )

    $taskID = Assert-ObjectProperty -Object $Row -Name 'id' -Description 'fleet row'
    $current = Assert-ObjectProperty -Object $Row -Name 'current_state' -Description "$taskID fleet row"
    $state = Assert-ObjectProperty -Object $current -Name 'state' -Description "$taskID current state"
    $source = Assert-ObjectProperty -Object $current -Name 'source' -Description "$taskID current source"
    $monitor = Assert-ObjectProperty -Object $Row -Name 'monitor' -Description "$taskID fleet row"
    $health = Assert-ObjectProperty -Object $monitor -Name 'health' -Description "$taskID monitor"
    $staleSeconds = [int64](Assert-ObjectProperty -Object $monitor -Name 'stale_seconds' -Description "$taskID monitor")
    $lastSeen = Convert-LastSeenToMarkdown (Assert-ObjectProperty -Object $monitor -Name 'last_seen' -Description "$taskID monitor")
    $escalation = Assert-ObjectProperty -Object $monitor -Name 'escalation' -Description "$taskID monitor"
    $deepInspection = [bool](Assert-ObjectProperty -Object $monitor -Name 'demand_deep_inspection' -Description "$taskID monitor")
    $endpoint = Assert-ObjectProperty -Object $Row -Name 'endpoint' -Description "$taskID fleet row"
    $endpointTarget = Assert-ObjectProperty -Object $endpoint -Name 'target' -Description "$taskID endpoint"
    $null = Assert-ObjectProperty -Object $endpoint -Name 'session' -Description "$taskID endpoint"
    $null = Assert-ObjectProperty -Object $endpoint -Name 'pane_id' -Description "$taskID endpoint"
    $endpointExists = Assert-ObjectProperty -Object $endpoint -Name 'exists' -Description "$taskID endpoint"
    $kind = Assert-ObjectProperty -Object $Row -Name 'kind' -Description "$taskID fleet row"
    $project = Assert-ObjectProperty -Object $Row -Name 'project' -Description "$taskID fleet row"
    $backend = Assert-ObjectProperty -Object $Row -Name 'backend' -Description "$taskID fleet row"
    $path = Assert-ObjectProperty -Object $Row -Name 'path' -Description "$taskID fleet row"
    $peek = Assert-ObjectProperty -Object $Row -Name 'actions' -Description "$taskID fleet row"
    $peek = Assert-ObjectProperty -Object $peek -Name 'peek' -Description "$taskID fleet row"

    if ($taskID -ne $Worker.ID -or $path -ne $Worker.Worktree) {
        throw "ACCEPTANCE BLOCKER: fleet JSON task/worktree identity does not match $($Worker.ID)."
    }
    if ([string]::IsNullOrWhiteSpace([string]$endpointTarget)) {
        throw "ACCEPTANCE BLOCKER: fleet JSON does not expose an endpoint target for $taskID."
    }

    $fields = @(
        (Convert-MarkdownCell $taskID),
        ((Convert-MarkdownCell $state) + ' / ' + (Convert-MarkdownCell $source)),
        (Convert-MarkdownCell $health),
        (Convert-StaleSecondsToGoDuration $staleSeconds),
        (Convert-MarkdownCell $lastSeen),
        (Convert-MarkdownCell $escalation),
        (Convert-BooleanToMarkdown $deepInspection),
        (Convert-MarkdownCell $kind),
        (Convert-MarkdownCell $project),
        (Convert-MarkdownCell $backend),
        (Convert-EndpointToMarkdown $endpointTarget $endpointExists),
        (Convert-MarkdownCell $Row.artifact),
        (Convert-MarkdownCell $path),
        (Convert-MarkdownCell $peek)
    )
    $expectedRow = '| ' + ($fields -join ' | ') + ' |'
    if ($Markdown -notmatch [regex]::Escape($expectedRow)) {
        throw "ACCEPTANCE BLOCKER: fleet Markdown does not exactly project task/current/worktree plus endpoint, stale duration, last seen, escalation, and deep inspection for $taskID. Expected row: $expectedRow"
    }
}

function Test-CfoCleanupAvailable {
    param([Parameter(Mandatory = $true)][string]$Cfo)

    if (-not (Test-Path -LiteralPath $Cfo -PathType Leaf)) {
        return $false
    }
    $output = @(& $Cfo cleanup --help 2>&1)
    $text = $output -join [Environment]::NewLine
    return $LASTEXITCODE -eq 0 -and $text -match '(?m)usage:\s+cfo cleanup'
}

function New-MissingCleanupBlocker {
    param([Parameter(Mandatory = $true)][string]$Root)
    return "ACCEPTANCE BLOCKER: this cfo build has no cleanup command. Leaving disposable fixture intact at $Root because no direct treehouse return or worktree deletion is permitted."
}

function Assert-CfoCleanupReady {
    param(
        [Parameter(Mandatory = $true)][string]$Cfo,
        [Parameter(Mandatory = $true)][string]$Root
    )

    if (-not (Test-CfoCleanupAvailable -Cfo $Cfo)) {
        throw (New-MissingCleanupBlocker -Root $Root)
    }
}

function Initialize-DisposablePrimaryCfoHome {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$CfoHome
    )

    Assert-ContainedPath -Root $Root -Path $CfoHome -Description 'disposable CFO home'
    $state = Join-Path $CfoHome 'state'
    Assert-ContainedPath -Root $Root -Path $state -Description 'disposable CFO state directory'
    New-Item -ItemType Directory -Path $state -Force -ErrorAction Stop | Out-Null
    Set-Content -LiteralPath (Join-Path $CfoHome 'AGENTS.md') -Value '# Disposable CFO acceptance home.' -NoNewline
    Invoke-Checked -FilePath 'git' -Arguments @('init', '--initial-branch=main', $CfoHome) -Description 'initialize disposable primary CFO home' | Out-Null
    Assert-DisposablePrimaryCfoHome -Root $Root -CfoHome $CfoHome
}

function Assert-DisposablePrimaryCfoHome {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$CfoHome
    )

    Assert-ContainedPath -Root $Root -Path $CfoHome -Description 'disposable CFO home'
    $agents = Join-Path $CfoHome 'AGENTS.md'
    $state = Join-Path $CfoHome 'state'
    if (-not (Test-Path -LiteralPath $agents -PathType Leaf) -or -not (Test-Path -LiteralPath $state -PathType Container)) {
        throw "ACCEPTANCE BLOCKER: disposable CFO home is missing AGENTS.md or state directory."
    }
    $paths = @(Invoke-Checked -FilePath 'git' -Arguments @('-C', $CfoHome, 'rev-parse', '--git-dir', '--git-common-dir') -Description 'inspect disposable primary CFO Git paths' | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne '' })
    if ($paths.Count -ne 2) {
        throw "ACCEPTANCE BLOCKER: disposable CFO home Git probe returned $($paths.Count) paths, want two."
    }
    $gitDir = Get-FullPath (Join-Path $CfoHome $paths[0])
    $commonDir = Get-FullPath (Join-Path $CfoHome $paths[1])
    if (-not [string]::Equals($gitDir, $commonDir, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "ACCEPTANCE BLOCKER: disposable CFO home is a linked worktree instead of a primary checkout."
    }
}

function Remove-FixtureRoot {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$TempRoot
    )

    $fullRoot = Get-FullPath $Root
    $fullTempRoot = (Get-FullPath $TempRoot).TrimEnd([System.IO.Path]::DirectorySeparatorChar, [System.IO.Path]::AltDirectorySeparatorChar)
    $parent = Split-Path -Parent $fullRoot
    $name = Split-Path -Leaf $fullRoot
    if (-not [string]::Equals($parent, $fullTempRoot, [System.StringComparison]::OrdinalIgnoreCase) -or -not $name.StartsWith('cfo-plan3-windows-', [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to remove non-fixture path $fullRoot."
    }
    Remove-Item -LiteralPath $fullRoot -Recurse -Force
}

$repoRoot = Get-FullPath (Join-Path $PSScriptRoot '..\..')
$tempRoot = [System.IO.Path]::GetTempPath()
$fixtureRoot = Join-Path $tempRoot ('cfo-plan3-windows-' + [Guid]::NewGuid().ToString('N'))
$cfoHome = Join-Path $fixtureRoot 'home'
$origin = Join-Path $fixtureRoot 'origin.git'
$project = Join-Path $fixtureRoot 'project'
$cfo = Join-Path $fixtureRoot 'cfo.exe'
$session = 'cfo-plan3-' + [Guid]::NewGuid().ToString('N')
$marker = 'plan3-acceptance-' + [Guid]::NewGuid().ToString('N')
$workers = @()
$herdrProcess = $null
$cleanupReady = $false
$cleanupCompleted = $false
$primaryFailure = $null
$cleanupFailure = $null
$previousCfoHome = $env:CFO_HOME
$previousHerdrSession = $env:HERDR_SESSION
$previousSignalGrace = $env:CFO_SIGNAL_GRACE

if ($SelfTest -eq 'missing-cleanup') {
    $missingCfo = Join-Path $fixtureRoot 'missing-cfo.exe'
    Assert-CfoCleanupReady -Cfo $missingCfo -Root $fixtureRoot
    throw 'Acceptance self-test expected a missing cfo cleanup command.'
}
if ($SelfTest -eq 'primary-home') {
    try {
        New-Item -ItemType Directory -Path $fixtureRoot -ErrorAction Stop | Out-Null
        Initialize-DisposablePrimaryCfoHome -Root $fixtureRoot -CfoHome $cfoHome
        Write-Output 'Plan 3 primary-home self-test passed.'
    }
    finally {
        Remove-FixtureRoot -Root $fixtureRoot -TempRoot $tempRoot
    }
    return
}
if ($SelfTest -eq 'fleet-parity') {
    $worker = [pscustomobject]@{ ID = 'accept-claude'; Worktree = 'C:\fixture\worker' }
    $row = [pscustomobject]@{
        id = 'accept-claude'
        current_state = [pscustomobject]@{ state = 'working'; source = 'status' }
        monitor = [pscustomobject]@{
            health = 'stale'
            stale_seconds = 121
            last_seen = '2026-08-13T12:34:56Z'
            escalation = 2
            demand_deep_inspection = $true
        }
        endpoint = [pscustomobject]@{ target = 'fixture:pane:claude'; session = 'fixture'; pane_id = 'pane:claude'; exists = $true }
        kind = 'task'
        project = 'C:\fixture\project'
        backend = 'herdr'
        artifact = ''
        path = 'C:\fixture\worker'
        actions = [pscustomobject]@{ peek = 'cfo peek fm-accept-claude' }
    }
    $markdown = '| accept-claude | working / status | stale | 2m1s | 2026-08-13T12:34:56Z | 2 | yes | task | C:\fixture\project | herdr | fixture:pane:claude (present) | - | C:\fixture\worker | cfo peek fm-accept-claude |'
    Assert-FleetProjectionParity -Row $row -Worker $worker -Markdown $markdown
    try {
        Assert-FleetProjectionParity -Row $row -Worker $worker -Markdown ($markdown.Replace('present', 'absent'))
        throw 'Acceptance self-test expected Markdown endpoint parity to fail.'
    }
    catch {
        if ($_.Exception.Message -notmatch 'does not exactly project') {
            throw
        }
    }
    try {
        Assert-FleetProjectionParity -Row $row -Worker $worker -Markdown ($markdown.Replace('fixture:pane:claude', 'fixture:pane:wrong'))
        throw 'Acceptance self-test expected Markdown endpoint target parity to fail.'
    }
    catch {
        if ($_.Exception.Message -notmatch 'does not exactly project') {
            throw
        }
    }
    Write-Output 'Plan 3 fleet parity self-test passed.'
    return
}
if ($SelfTest -eq 'escaping-worker-path') {
    $escapingWorker = Join-Path $tempRoot 'cfo-plan3-worker-outside-fixture'
    Assert-ContainedPath -Root $fixtureRoot -Path $escapingWorker -Description 'worker worktree'
    throw 'Acceptance self-test expected an escaping worker worktree to be refused.'
}
if ($SelfTest -ne '') {
    throw "Unknown Plan 3 acceptance self-test $SelfTest."
}

try {
    New-Item -ItemType Directory -Path $fixtureRoot -ErrorAction Stop | Out-Null
    Assert-ContainedPath -Root $fixtureRoot -Path $cfoHome -Description 'CFO home'
    Assert-ContainedPath -Root $fixtureRoot -Path $origin -Description 'fixture Git origin'
    Assert-ContainedPath -Root $fixtureRoot -Path $project -Description 'fixture Git project'
    Assert-ContainedPath -Root $fixtureRoot -Path $cfo -Description 'CFO binary'
    Initialize-DisposablePrimaryCfoHome -Root $fixtureRoot -CfoHome $cfoHome

    Push-Location $repoRoot
    try {
        Invoke-Checked -FilePath 'go' -Arguments @('vet', './...') -Description 'go vet ./...' | Out-Host
        Invoke-Checked -FilePath 'go' -Arguments @('test', './...', '-count=1') -Description 'go test ./... -count=1' | Out-Host
        Invoke-Checked -FilePath 'go' -Arguments @('build', '-o', $cfo, './cmd/cfo') -Description 'go build cfo.exe' | Out-Host
    }
    finally {
        Pop-Location
    }
    Assert-CfoCleanupReady -Cfo $cfo -Root $fixtureRoot
    $cleanupReady = $true

    Invoke-Checked -FilePath 'git' -Arguments @('init', '--bare', '--initial-branch=main', $origin) -Description 'initialize disposable bare origin' | Out-Host
    Invoke-Checked -FilePath 'git' -Arguments @('clone', $origin, $project) -Description 'clone disposable project' | Out-Host
    Invoke-Checked -FilePath 'git' -Arguments @('-C', $project, 'config', 'user.email', 'plan3-acceptance@example.invalid') -Description 'configure disposable Git email' | Out-Host
    Invoke-Checked -FilePath 'git' -Arguments @('-C', $project, 'config', 'user.name', 'Plan 3 Acceptance') -Description 'configure disposable Git name' | Out-Host
    Set-Content -LiteralPath (Join-Path $project 'README.md') -Value 'Plan 3 Windows acceptance fixture.' -NoNewline
    Invoke-Checked -FilePath 'git' -Arguments @('-C', $project, 'add', 'README.md') -Description 'stage disposable project seed' | Out-Host
    Invoke-Checked -FilePath 'git' -Arguments @('-C', $project, 'commit', '-m', 'fixture seed') -Description 'commit disposable project seed' | Out-Host
    Invoke-Checked -FilePath 'git' -Arguments @('-C', $project, 'push', '-u', 'origin', 'main') -Description 'push disposable project seed' | Out-Host

    $env:CFO_HOME = $cfoHome
    $env:HERDR_SESSION = $session
    $env:CFO_SIGNAL_GRACE = '1'

    $doctor = @(& $cfo doctor 2>&1)
    $doctorText = $doctor -join [Environment]::NewLine
    foreach ($tool in @('git', 'herdr', 'treehouse', 'claude', 'codex', 'pi')) {
        if ($doctorText -notmatch "(?m)^ok\s+$tool\s+") {
            throw "ACCEPTANCE BLOCKER: cfo doctor did not report usable $tool for the real-session target.`n$doctorText"
        }
    }
    foreach ($tool in @('tasks-axi', 'quota-axi')) {
        $line = $doctor | Where-Object { $_ -match "\b$tool\b" } | Select-Object -First 1
        Write-Host "INFO: $tool is an external integration check: $line"
    }

    $herdrProcess = Start-Process -FilePath 'herdr' -ArgumentList @('server', '--session', $session) -PassThru -WindowStyle Hidden
    $deadline = [DateTime]::UtcNow.AddSeconds(10)
    do {
        Start-Sleep -Milliseconds 250
        try {
            $status = Invoke-HerdrJson -Arguments @('status', '--json', '--session', $session) -Description "herdr status --json for dedicated session $session"
            break
        }
        catch {
            if ([DateTime]::UtcNow -ge $deadline) {
                throw
            }
        }
    } while ($true)
    $null = Assert-ObjectProperty -Object $status -Name 'result' -Description 'Herdr status'
    $schema = Invoke-HerdrJson -Arguments @('api', 'schema', '--json', '--session', $session) -Description "herdr api schema --json for dedicated session $session"
    $null = Assert-ObjectProperty -Object $schema -Name 'result' -Description 'Herdr API schema'
    $sessions = Invoke-HerdrJson -Arguments @('session', 'list', '--json', '--session', $session) -Description "herdr session list --json for dedicated session $session"
    $null = Assert-ObjectProperty -Object $sessions -Name 'result' -Description 'Herdr session list'

    foreach ($harness in @('claude', 'codex', 'pi')) {
        $id = 'accept-' + $harness
        $brief = Join-Path $fixtureRoot ($id + '.brief.md')
        Assert-ContainedPath -Root $fixtureRoot -Path $brief -Description "$harness brief"
        @(
            'Delivery contract: mode=local-only',
            "Print exactly this marker: $marker",
            'Exit after printing the marker.'
        ) | Set-Content -LiteralPath $brief

        $spawn = Invoke-Checked -FilePath $cfo -Arguments @('spawn', $id, '--project', $project, '--brief', $brief, '--harness', $harness, '--mode', 'local-only') -Description "spawn $harness acceptance worker"
        $worker = [pscustomobject]@{ ID = $id; Worktree = $null; Meta = $null }
        $workers += $worker
        Write-Host ($spawn -join [Environment]::NewLine)
        $metaPath = Join-Path $cfoHome (Join-Path 'state' ($id + '.meta'))
        $meta = Read-TaskMeta -Path $metaPath
        $worktree = Assert-MetaValue -Meta $meta -Name 'worktree' -Description $id
        Assert-ContainedPath -Root $fixtureRoot -Path $worktree -Description "$id worker worktree"
        if ([string]::Equals((Get-FullPath $worktree), (Get-FullPath $project), [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "ACCEPTANCE BLOCKER: $id recorded the primary checkout as its worker worktree."
        }
        $worker.Worktree = $worktree
        $worker.Meta = $meta
        $window = Assert-MetaValue -Meta $meta -Name 'window' -Description $id
        $separator = $window.IndexOf(':')
        if ($separator -le 0 -or $separator -ge ($window.Length - 1) -or $window.Substring(0, $separator) -ne $session) {
            throw "ACCEPTANCE BLOCKER: $id window $window does not preserve explicit session-first target parsing."
        }
        $pane = $window.Substring($separator + 1)
        if ($pane -ne (Assert-MetaValue -Meta $meta -Name 'herdr_pane_id' -Description $id)) {
            throw "ACCEPTANCE BLOCKER: $id window pane does not match metadata."
        }

        $workspace = Assert-MetaValue -Meta $meta -Name 'herdr_workspace_id' -Description $id
        $tabs = Invoke-HerdrJson -Arguments @('tab', 'list', '--workspace', $workspace, '--json', '--session', $session) -Description "Herdr workspace tab list for $id"
        $tabRows = Assert-ObjectProperty -Object $tabs.result -Name 'tabs' -Description "$id workspace"
        $taskTabs = @($tabRows | Where-Object { $_.label -eq ('fm-' + $id) })
        if ($taskTabs.Count -ne 1) {
            throw "ACCEPTANCE BLOCKER: $id workspace has $($taskTabs.Count) matching task tabs, want exactly one."
        }

        $peek = @(& $cfo peek ('fm-' + $id) 200 2>&1)
        if ($LASTEXITCODE -ne 0) {
            throw "ACCEPTANCE BLOCKER: cfo peek failed for $id.`n$($peek -join [Environment]::NewLine)"
        }
        $peekText = $peek -join [Environment]::NewLine
        if ($peekText -notmatch [regex]::Escape($marker) -and $peekText -notmatch '(?i)starting|loading|working|initializ') {
            throw "ACCEPTANCE BLOCKER: cfo peek for $id has neither the marker nor a documented startup-progress response.`n$peekText"
        }

        $send = Invoke-Checked -FilePath $cfo -Arguments @('send', ('fm-' + $id), "print the acceptance marker $marker and exit") -Description "send acceptance marker to $id"
        if (($send -join [Environment]::NewLine) -notmatch ('sent fm-' + [regex]::Escape($id))) {
            throw "ACCEPTANCE BLOCKER: cfo send did not confirm delivery for $id."
        }
    }

    $watchStatus = Join-Path $cfoHome 'state\acceptance-watch.status'
    Assert-ContainedPath -Root $fixtureRoot -Path $watchStatus -Description 'fixture watch trigger'
    Set-Content -LiteralPath $watchStatus -Value 'working: fixture monitor trigger' -NoNewline
    & $cfo watch
    if ($LASTEXITCODE -ne 0) {
        throw 'ACCEPTANCE BLOCKER: cfo watch failed before monitor inspection.'
    }
    foreach ($worker in $workers) {
        $observation = Join-Path $cfoHome (Join-Path 'state\monitor\tasks' ($worker.ID + '.json'))
        if (-not (Test-Path -LiteralPath $observation -PathType Leaf)) {
            throw "ACCEPTANCE BLOCKER: cfo watch did not persist a monitor observation for $($worker.ID). Plan 3 real-session monitoring cannot claim busy protection, stale escalation, deep inspection, unknown handling, or restart persistence."
        }
    }

    $fleetJsonText = (@(& $cfo fleet-view --json 2>&1) -join [Environment]::NewLine)
    if ($LASTEXITCODE -ne 0) {
        throw "ACCEPTANCE BLOCKER: cfo fleet-view --json failed.`n$fleetJsonText"
    }
    $fleetJson = $fleetJsonText | ConvertFrom-Json -ErrorAction Stop
    $fleetMarkdown = (@(& $cfo fleet-view 2>&1) -join [Environment]::NewLine)
    if ($LASTEXITCODE -ne 0) {
        throw "ACCEPTANCE BLOCKER: cfo fleet-view failed.`n$fleetMarkdown"
    }
    foreach ($worker in $workers) {
        $row = @($fleetJson.tasks | Where-Object { $_.id -eq $worker.ID })
        if ($row.Count -ne 1) {
            throw "ACCEPTANCE BLOCKER: fleet JSON has $($row.Count) rows for $($worker.ID)."
        }
        Assert-FleetProjectionParity -Row $row[0] -Worker $worker -Markdown $fleetMarkdown
    }

    $primaryStatus = Invoke-Checked -FilePath 'git' -Arguments @('-C', $project, 'status', '--porcelain') -Description 'verify disposable primary checkout status'
    if ($primaryStatus.Count -ne 0) {
        throw "ACCEPTANCE BLOCKER: disposable primary checkout is dirty.`n$($primaryStatus -join [Environment]::NewLine)"
    }
}
catch {
    $primaryFailure = $_
}
finally {
    $cleanupFailed = -not $cleanupReady
    if ($cleanupFailed) {
        if ($workers.Count -gt 0) {
            $cleanupFailure = New-MissingCleanupBlocker -Root $fixtureRoot
            [Console]::Error.WriteLine($cleanupFailure)
        }
    }
    else {
        foreach ($worker in $workers) {
            $output = @(& $cfo cleanup $worker.ID 2>&1)
            if ($LASTEXITCODE -ne 0) {
                $cleanupFailure = "ACCEPTANCE BLOCKER: cfo cleanup failed for $($worker.ID). Leaving fixture intact.`n$($output -join [Environment]::NewLine)"
                [Console]::Error.WriteLine($cleanupFailure)
                $cleanupFailed = $true
                break
            }
            if ([string]::IsNullOrWhiteSpace($worker.Worktree)) {
                $cleanupFailure = "ACCEPTANCE BLOCKER: cfo cleanup succeeded for $($worker.ID), but its worker worktree was not recorded. Leaving fixture intact because cleanup cannot be proven."
                [Console]::Error.WriteLine($cleanupFailure)
                $cleanupFailed = $true
                break
            }
            if (Test-Path -LiteralPath $worker.Worktree) {
                $cleanupFailure = "ACCEPTANCE BLOCKER: cfo cleanup returned success but left $($worker.Worktree). Leaving fixture intact."
                [Console]::Error.WriteLine($cleanupFailure)
                $cleanupFailed = $true
                break
            }
        }
    }
    if (-not $cleanupFailed) {
        $primaryStatus = @(& git -C $project status --porcelain 2>&1)
        if ($LASTEXITCODE -ne 0) {
            $cleanupFailure = "ACCEPTANCE BLOCKER: could not verify disposable primary checkout after cleanup. Leaving fixture intact.`n$($primaryStatus -join [Environment]::NewLine)"
            [Console]::Error.WriteLine($cleanupFailure)
        }
        elseif ($primaryStatus.Count -ne 0) {
            $cleanupFailure = "ACCEPTANCE BLOCKER: disposable primary checkout is dirty after cleanup. Leaving fixture intact.`n$($primaryStatus -join [Environment]::NewLine)"
            [Console]::Error.WriteLine($cleanupFailure)
        }
        else {
            $cleanupCompleted = $true
        }
    }

    if ($null -ne $herdrProcess -and -not $herdrProcess.HasExited) {
        Stop-Process -Id $herdrProcess.Id -Force
    }
    if ($cleanupCompleted) {
        Remove-FixtureRoot -Root $fixtureRoot -TempRoot $tempRoot
    }
    else {
        [Console]::Error.WriteLine("Disposable fixture preserved for manual recovery: $fixtureRoot")
    }
    $env:CFO_HOME = $previousCfoHome
    $env:HERDR_SESSION = $previousHerdrSession
    $env:CFO_SIGNAL_GRACE = $previousSignalGrace
}

if ($null -ne $primaryFailure) {
    throw $primaryFailure
}
if ($null -ne $cleanupFailure) {
    throw $cleanupFailure
}
