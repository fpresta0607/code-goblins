[CmdletBinding()]
param()

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

function Test-CfoCleanupAvailable {
    param([Parameter(Mandatory = $true)][string]$Cfo)

    if (-not (Test-Path -LiteralPath $Cfo -PathType Leaf)) {
        return $false
    }
    $output = @(& $Cfo cleanup --help 2>&1)
    $text = $output -join [Environment]::NewLine
    return $LASTEXITCODE -eq 0 -and $text -match '(?m)usage:\s+cfo cleanup'
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
$cleanupCompleted = $false

try {
    New-Item -ItemType Directory -Path $fixtureRoot -ErrorAction Stop | Out-Null
    Assert-ContainedPath -Root $fixtureRoot -Path $cfoHome -Description 'CFO home'
    Assert-ContainedPath -Root $fixtureRoot -Path $origin -Description 'fixture Git origin'
    Assert-ContainedPath -Root $fixtureRoot -Path $project -Description 'fixture Git project'
    Assert-ContainedPath -Root $fixtureRoot -Path $cfo -Description 'CFO binary'

    Push-Location $repoRoot
    try {
        Invoke-Checked -FilePath 'go' -Arguments @('vet', './...') -Description 'go vet ./...' | Out-Host
        Invoke-Checked -FilePath 'go' -Arguments @('test', './...', '-count=1') -Description 'go test ./... -count=1' | Out-Host
        Invoke-Checked -FilePath 'go' -Arguments @('build', '-o', $cfo, './cmd/cfo') -Description 'go build cfo.exe' | Out-Host
    }
    finally {
        Pop-Location
    }

    Invoke-Checked -FilePath 'git' -Arguments @('init', '--bare', '--initial-branch=main', $origin) -Description 'initialize disposable bare origin' | Out-Host
    Invoke-Checked -FilePath 'git' -Arguments @('clone', $origin, $project) -Description 'clone disposable project' | Out-Host
    Invoke-Checked -FilePath 'git' -Arguments @('-C', $project, 'config', 'user.email', 'plan3-acceptance@example.invalid') -Description 'configure disposable Git email' | Out-Host
    Invoke-Checked -FilePath 'git' -Arguments @('-C', $project, 'config', 'user.name', 'Plan 3 Acceptance') -Description 'configure disposable Git name' | Out-Host
    Set-Content -LiteralPath (Join-Path $project 'README.md') -Value 'Plan 3 Windows acceptance fixture.' -NoNewline
    Invoke-Checked -FilePath 'git' -Arguments @('-C', $project, 'add', 'README.md') -Description 'stage disposable project seed' | Out-Host
    Invoke-Checked -FilePath 'git' -Arguments @('-C', $project, 'commit', '-m', 'fixture seed') -Description 'commit disposable project seed' | Out-Host
    Invoke-Checked -FilePath 'git' -Arguments @('-C', $project, 'push', '-u', 'origin', 'main') -Description 'push disposable project seed' | Out-Host

    $previousCfoHome = $env:CFO_HOME
    $previousHerdrSession = $env:HERDR_SESSION
    $env:CFO_HOME = $cfoHome
    $env:HERDR_SESSION = $session

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
        Write-Host ($spawn -join [Environment]::NewLine)
        $metaPath = Join-Path $cfoHome (Join-Path 'state' ($id + '.meta'))
        $meta = Read-TaskMeta -Path $metaPath
        $worktree = Assert-MetaValue -Meta $meta -Name 'worktree' -Description $id
        Assert-ContainedPath -Root $fixtureRoot -Path $worktree -Description "$id worker worktree"
        if ([string]::Equals((Get-FullPath $worktree), (Get-FullPath $project), [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "ACCEPTANCE BLOCKER: $id recorded the primary checkout as its worker worktree."
        }
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
        $workers += [pscustomobject]@{ ID = $id; Worktree = $worktree; Meta = $meta }
    }

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
        foreach ($value in @($worker.ID, $row[0].current_state.state, $row[0].monitor.health, $row[0].monitor.escalation, $worker.Worktree)) {
            if ($fleetMarkdown -notmatch [regex]::Escape([string]$value)) {
                throw "ACCEPTANCE BLOCKER: fleet Markdown does not preserve JSON value $value for $($worker.ID)."
            }
        }
    }

    $primaryStatus = Invoke-Checked -FilePath 'git' -Arguments @('-C', $project, 'status', '--porcelain') -Description 'verify disposable primary checkout status'
    if ($primaryStatus.Count -ne 0) {
        throw "ACCEPTANCE BLOCKER: disposable primary checkout is dirty.`n$($primaryStatus -join [Environment]::NewLine)"
    }
}
finally {
    $cleanupAvailable = Test-CfoCleanupAvailable -Cfo $cfo
    if (-not $cleanupAvailable) {
        [Console]::Error.WriteLine("ACCEPTANCE BLOCKER: this cfo build has no cleanup command. Leaving disposable fixture intact at $fixtureRoot because no direct treehouse return or worktree deletion is permitted.")
    }
    else {
        $cleanupFailed = $false
        foreach ($worker in $workers) {
            $output = @(& $cfo cleanup $worker.ID 2>&1)
            if ($LASTEXITCODE -ne 0) {
                [Console]::Error.WriteLine("ACCEPTANCE BLOCKER: cfo cleanup failed for $($worker.ID). Leaving fixture intact.`n$($output -join [Environment]::NewLine)")
                $cleanupFailed = $true
                break
            }
            if (Test-Path -LiteralPath $worker.Worktree) {
                [Console]::Error.WriteLine("ACCEPTANCE BLOCKER: cfo cleanup returned success but left $($worker.Worktree). Leaving fixture intact.")
                $cleanupFailed = $true
                break
            }
        }
        if (-not $cleanupFailed) {
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
}
