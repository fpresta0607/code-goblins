# Proves on a hosted runner's desktop, never on a person's, how long each
# careless start of goblins gate turn shows a window, with the build of main and
# with this commit's, and that goblins gate turn --detach shows none at all. The
# default terminal is whatever the runner is set to: the workflow runs it
# once with the console host and once with Windows Terminal.
#
# main's careless starts are the proof's own premise: it must show a window
# and run its command for the whole watch, or no row measures anything. A
# runner where that premise does not hold is an environment result, never a
# failure of the change. This commit's rows stay strict. -SelfTest runs the
# verdict and the summary over rows made up here, so a fixture proves those
# rules without a desktop.
param(
    [string]$BaseGoblins = '',
    [string]$Goblins = '',
    [string]$Terminal = '',
    [string]$Work = '',
    [string]$SelfTest = ''
)
$ErrorActionPreference = 'Stop'

# Start-Exactly starts program with exactly the command line arguments,
# which Windows PowerShell would otherwise rewrite: it drops a bare -- it
# passes to a native program. Its output is this script's, which cmd's start
# hands on to what it starts, so it is never read through a pipe, which
# would wait for the whole run.
function Start-Exactly([string]$Program, [string]$Arguments) {
    $info = New-Object Diagnostics.ProcessStartInfo $Program, $Arguments
    $info.UseShellExecute = $false
    $process = [Diagnostics.Process]::Start($info)
    $process.WaitForExit()
}

# Watch-Start runs a start and watches the desktop for $watchFor seconds: the
# windows it opened, how long after the start the last one was still in
# front, how long the last one was there at all, minimized or not, and how
# long the run's command, ping, was seen running. What it started is ended by
# its pids.
function Watch-Start([string]$Name, [string]$Build, [scriptblock]$Start) {
    $before = [Desktop]::Shown()
    $existing = @{}
    foreach ($process in Get-Process) { $existing[$process.Id] = $true }
    $started = Get-Date
    & $Start
    $opened = @{}
    $front = -1
    $any = -1
    $command = -1
    $runs = @{}
    while (((Get-Date) - $started).TotalSeconds -lt $watchFor) {
        $at = [int]((Get-Date) - $started).TotalMilliseconds
        foreach ($entry in ([Desktop]::Shown()).GetEnumerator()) {
            if ($before.ContainsKey($entry.Key)) { continue }
            $opened[$entry.Key] = $true
            $any = $at
            if (-not $entry.Value) { $front = $at }
        }
        if (Get-Process -Name PING -ErrorAction SilentlyContinue | Where-Object { -not $existing[$_.Id] }) { $command = $at }
        # Each goblins the start ran, held by its handle so its exit code can be
        # read once it has ended.
        foreach ($process in Get-Process -Name goblins -ErrorAction SilentlyContinue) {
            if (-not $existing[$process.Id] -and -not $runs.ContainsKey($process.Id)) {
                $null = $process.Handle
                $runs[$process.Id] = $process
            }
        }
        Start-Sleep -Milliseconds 100
    }
    foreach ($run in $runs.Values) {
        $ended = if ($run.HasExited) { "ended with exit $($run.ExitCode)" } else { 'runs' }
        "[$Terminal / $Build / $Name] goblins pid $($run.Id) $ended" | Out-Host
    }
    foreach ($process in Get-Process) {
        if (-not $existing[$process.Id] -and $process.ProcessName -in 'goblins', 'PING', 'cmd', 'bash', 'conhost', 'OpenConsole') {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        }
    }
    Start-Sleep -Seconds 1
    [pscustomobject]@{ Terminal = $Terminal; Build = $Build; Start = $Name; Windows = $opened.Count; InFrontUntilMs = $front; ShownUntilMs = $any; CommandUntilMs = $command }
}

# When-Of renders the time a row was last seen, as the summary's table does.
function When-Of([int]$Milliseconds) {
    if ($Milliseconds -lt 0) { return 'never' }
    return "$Milliseconds ms"
}

# Get-Verdict reads the measured rows and says what each means: a failure of
# the change, or an environment result where the runner cannot host the
# proof's premise. main's careless start must show a window and run its
# command for the whole watch; a row that does not is the environment's and
# not the change's. This commit's careless start must run its command and
# show no window beyond a moment, and the detached run must show none and
# start its command, which stay failures.
function Get-Verdict {
    param(
        [Parameter(Mandatory = $true)][object[]]$Results,
        [Parameter(Mandatory = $true)][int]$WatchFor,
        [string]$DetachedEnd = ''
    )
    $short = $WatchFor * 1000 - 1000
    $failures = @()
    $environment = @()
    foreach ($row in $Results) {
        $isCareless = $row.Start -ne 'goblins gate turn --detach'
        if ($row.Build -eq 'main') {
            if ($row.CommandUntilMs -lt $short) {
                $environment += "main's $($row.Start) did not run its command through the $($WatchFor * 1000) ms watch (last seen: $(When-Of $row.CommandUntilMs)), so this runner cannot host the proof: the row measures no real run"
            }
            if ($row.ShownUntilMs -lt $short) {
                $environment += "main's $($row.Start) did not show a window through the $($WatchFor * 1000) ms watch (last seen: $(When-Of $row.ShownUntilMs)), so this desktop does not show what the proof needs"
            }
        } elseif ($isCareless) {
            if ($row.CommandUntilMs -lt $short) {
                $failures += "this branch's $($row.Start) did not run its command through the $($WatchFor * 1000) ms watch (last seen: $(When-Of $row.CommandUntilMs)), so it measures no real run"
            }
            if ($row.ShownUntilMs -gt 1000) {
                $failures += "this branch's $($row.Start) still showed a window $($row.ShownUntilMs) ms after it started"
            }
        } elseif ($row.Windows -ne 0 -or $row.CommandUntilMs -lt 0) {
            $failures += "goblins gate turn --detach showed $($row.Windows) windows and ran its command until $($row.CommandUntilMs) ms"
        }
    }
    if ($DetachedEnd -notlike 'goblins gate turn: the detached run ended with exit 0 after *') {
        $failures += "the detached run's log ends with '$DetachedEnd', not how it ended"
    }
    return [pscustomobject]@{ Failures = $failures; Environment = $environment }
}

# Write-Summary appends the run's summary: the full table of every row, the
# detached run's last line, and, when the runner could not host the proof,
# the environment result with exactly which premise failed and what it means.
function Write-Summary {
    param(
        [Parameter(Mandatory = $true)][object[]]$Results,
        [Parameter(Mandatory = $true)][string]$Terminal,
        [string]$DetachedEnd = '',
        [Parameter(Mandatory = $true)]$Verdict,
        [Parameter(Mandatory = $true)][string]$Path
    )
    $summary = @("### $Terminal as the default terminal", '', '| Build | Start | New windows | In front until | Shown until | Its command ran until |', '|---|---|---|---|---|---|')
    foreach ($row in $Results) {
        $front = When-Of $row.InFrontUntilMs
        $shown = When-Of $row.ShownUntilMs
        $ran = When-Of $row.CommandUntilMs
        $summary += "| $($row.Build) | $($row.Start) | $($row.Windows) | $front | $shown | $ran |"
    }
    $summary += '', "Detached run's last line: ``$DetachedEnd``", ''
    if ($Verdict.Environment.Count -gt 0) {
        $summary += '**Environment result: this runner cannot host the proof, so the rows for main are no verdict on the change.**', ''
        foreach ($line in $Verdict.Environment) { $summary += "- $line" }
        $summary += ''
    }
    $summary | Add-Content -Path $Path
}

# A self-test is the fixture the acceptance criteria ask for: rows made up
# here, never measured, through the same verdict and summary the run uses.
if ($SelfTest -ne '') {
    $watchFor = 10
    $full = $watchFor * 1000
    $detached = 'goblins gate turn --detach'
    $ended = 'goblins gate turn: the detached run ended with exit 0 after 3s'

    function Row([string]$Build, [string]$Start, [int]$Windows, [int]$ShownUntilMs, [int]$CommandUntilMs) {
        [pscustomobject]@{
            Terminal       = 'a fixture'
            Build          = $Build
            Start          = $Start
            Windows        = $Windows
            InFrontUntilMs = $ShownUntilMs
            ShownUntilMs   = $ShownUntilMs
            CommandUntilMs = $CommandUntilMs
        }
    }

    function Assert-SelfTest([bool]$Condition, [string]$Message) {
        if (-not $Condition) { throw "no_visible_windows self-test ${SelfTest}: $Message" }
    }

    $mainGood = @(
        (Row 'main' 'Start-Process at the normal window style' 1 $full $full)
        (Row 'main' 'cmd /c start' 1 $full $full)
    )
    $branchGood = @(
        (Row 'this branch' 'Start-Process at the normal window style' 0 -1 $full)
        (Row 'this branch' 'cmd /c start' 0 -1 $full)
    )
    $detachedGood = (Row 'this branch' $detached 0 -1 3000)

    switch ($SelfTest) {
        'base-command-ends-early' {
            $rows = @((Row 'main' 'Start-Process at the normal window style' 1 $full 4000)) + $branchGood + $detachedGood
            $verdict = Get-Verdict -Results $rows -WatchFor $watchFor -DetachedEnd $ended
            Assert-SelfTest ($verdict.Failures.Count -eq 0) "no failure, but found: $($verdict.Failures -join '; ')"
            Assert-SelfTest ($verdict.Environment.Count -eq 1) "one environment result, but found: $($verdict.Environment -join '; ')"
            Assert-SelfTest ($verdict.Environment[0] -like '*Start-Process at the normal window style*') "the environment result names the row: $($verdict.Environment[0])"
            Assert-SelfTest ($verdict.Environment[0] -like '*(last seen: 4000 ms)*') "the environment result carries the numbers: $($verdict.Environment[0])"
        }
        'base-window-ends-early' {
            $mainRow = Row 'main' 'Start-Process at the normal window style' 1 4000 $full
            $rows = @($mainRow) + $branchGood + $detachedGood
            $verdict = Get-Verdict -Results $rows -WatchFor $watchFor -DetachedEnd $ended
            Assert-SelfTest ($verdict.Failures.Count -eq 0) "no failure, but found: $($verdict.Failures -join '; ')"
            Assert-SelfTest ($verdict.Environment.Count -eq 1) "one environment result, but found: $($verdict.Environment -join '; ')"
            Assert-SelfTest ($verdict.Environment[0] -like '*Start-Process at the normal window style*') "the environment result names the row: $($verdict.Environment[0])"
            Assert-SelfTest ($verdict.Environment[0] -like '*did not show a window through the 10000 ms watch*') "the environment result names the closing window: $($verdict.Environment[0])"
            Assert-SelfTest ($verdict.Environment[0] -like '*(last seen: 4000 ms)*') "the environment result carries the numbers: $($verdict.Environment[0])"
            $path = Join-Path ([System.IO.Path]::GetTempPath()) "no_visible_windows-$([guid]::NewGuid()).md"
            try {
                Write-Summary -Results $rows -Terminal 'a fixture' -DetachedEnd $ended -Verdict $verdict -Path $path
                $summary = Get-Content -Raw -Path $path
                Assert-SelfTest ($summary.Contains('Environment result')) 'the summary carries the environment heading'
                Assert-SelfTest ($summary.Contains($verdict.Environment[0])) 'the summary carries the environment result'
                Assert-SelfTest ($summary.Contains('| main | Start-Process at the normal window style | 1 | 4000 ms | 4000 ms | 10000 ms |')) "the summary still holds the row's numbers: $summary"
            } finally {
                Remove-Item -Force -ErrorAction SilentlyContinue $path
            }
        }
        'base-no-window' {
            $rows = @((Row 'main' 'Start-Process at the normal window style' 0 -1 $full)) + $branchGood + $detachedGood
            $verdict = Get-Verdict -Results $rows -WatchFor $watchFor -DetachedEnd $ended
            Assert-SelfTest ($verdict.Failures.Count -eq 0) "no failure, but found: $($verdict.Failures -join '; ')"
            Assert-SelfTest ($verdict.Environment.Count -eq 1) "one environment result, but found: $($verdict.Environment -join '; ')"
            Assert-SelfTest ($verdict.Environment[0] -like '*did not show a window through the 10000 ms watch*') "the environment result names the missing window: $($verdict.Environment[0])"
        }
        'branch-shows-window' {
            $rows = $mainGood + @((Row 'this branch' 'Start-Process at the normal window style' 1 4000 $full)) + $detachedGood
            $verdict = Get-Verdict -Results $rows -WatchFor $watchFor -DetachedEnd $ended
            Assert-SelfTest ($verdict.Environment.Count -eq 0) "no environment result, but found: $($verdict.Environment -join '; ')"
            Assert-SelfTest ($verdict.Failures.Count -eq 1) "one failure, but found: $($verdict.Failures -join '; ')"
            Assert-SelfTest ($verdict.Failures[0] -like "*this branch's Start-Process at the normal window style still showed a window 4000 ms*") "the failure names the start and how long: $($verdict.Failures[0])"
        }
        'branch-command-unrun' {
            $rows = $mainGood + @((Row 'this branch' 'Start-Process at the normal window style' 0 -1 -1)) + $detachedGood
            $verdict = Get-Verdict -Results $rows -WatchFor $watchFor -DetachedEnd $ended
            Assert-SelfTest ($verdict.Environment.Count -eq 0) "no environment result, but found: $($verdict.Environment -join '; ')"
            Assert-SelfTest ($verdict.Failures.Count -eq 1) "one failure, but found: $($verdict.Failures -join '; ')"
            Assert-SelfTest ($verdict.Failures[0] -like '*did not run its command through*') "the failure names the unrun command: $($verdict.Failures[0])"
        }
        'detached-shows-window' {
            $rows = $mainGood + $branchGood + (Row 'this branch' $detached 2 -1 3000)
            $verdict = Get-Verdict -Results $rows -WatchFor $watchFor -DetachedEnd $ended
            Assert-SelfTest ($verdict.Failures.Count -eq 1) "one failure, but found: $($verdict.Failures -join '; ')"
            Assert-SelfTest ($verdict.Failures[0] -like '*showed 2 windows*') "the failure names the detached run's windows: $($verdict.Failures[0])"
        }
        'detached-command-unrun' {
            $rows = $mainGood + $branchGood + (Row 'this branch' $detached 0 -1 -1)
            $verdict = Get-Verdict -Results $rows -WatchFor $watchFor -DetachedEnd $ended
            Assert-SelfTest ($verdict.Failures.Count -eq 1) "one failure, but found: $($verdict.Failures -join '; ')"
            Assert-SelfTest ($verdict.Failures[0] -like '*ran its command until -1 ms*') "the failure names the unrun command: $($verdict.Failures[0])"
        }
        'detached-log-mismatch' {
            $rows = $mainGood + $branchGood + (Row 'this branch' $detached 0 -1 3000)
            $detachedEnd = (Get-Content (Join-Path ([System.IO.Path]::GetTempPath()) "no_visible_windows-$([guid]::NewGuid()).log") -ErrorAction SilentlyContinue | Select-Object -Last 1)
            $verdict = Get-Verdict -Results $rows -WatchFor $watchFor -DetachedEnd $detachedEnd
            Assert-SelfTest ($verdict.Environment.Count -eq 0) "no environment result, but found: $($verdict.Environment -join '; ')"
            Assert-SelfTest ($verdict.Failures.Count -eq 1) "one failure, but found: $($verdict.Failures -join '; ')"
            Assert-SelfTest ($verdict.Failures[0] -like "*the detached run's log ends with ''*") "the failure names the missing log line: $($verdict.Failures[0])"
            $path = Join-Path ([System.IO.Path]::GetTempPath()) "no_visible_windows-$([guid]::NewGuid()).md"
            try {
                Write-Summary -Results $rows -Terminal 'a fixture' -DetachedEnd $detachedEnd -Verdict $verdict -Path $path
                $summary = Get-Content -Raw -Path $path
                foreach ($row in $rows) {
                    Assert-SelfTest ($summary.Contains("| $($row.Build) | $($row.Start) |")) "the summary holds the row $($row.Build) / $($row.Start)"
                }
                Assert-SelfTest ($summary.Contains("Detached run's last line:")) 'the summary holds the detached run line'
            } finally {
                Remove-Item -Force -ErrorAction SilentlyContinue $path
            }
        }
        'summary-holds-every-row' {
            $rows = $mainGood + $branchGood + $detachedGood
            $verdict = Get-Verdict -Results $rows -WatchFor $watchFor -DetachedEnd $ended
            $path = Join-Path ([System.IO.Path]::GetTempPath()) "no_visible_windows-$([guid]::NewGuid()).md"
            try {
                Write-Summary -Results $rows -Terminal 'a fixture' -DetachedEnd $ended -Verdict $verdict -Path $path
                $summary = Get-Content -Raw -Path $path
                foreach ($row in $rows) {
                    Assert-SelfTest ($summary.Contains("| $($row.Build) | $($row.Start) |")) "the summary holds the row $($row.Build) / $($row.Start)"
                }
                Assert-SelfTest ($summary.Contains("Detached run's last line:")) 'the summary holds the detached run line'
            } finally {
                Remove-Item -Force -ErrorAction SilentlyContinue $path
            }
        }
        default { throw "Unknown no_visible_windows self-test $SelfTest." }
    }
    Write-Output "no_visible_windows self-test $SelfTest passed."
    exit 0
}

if (-not $BaseGoblins -or -not $Goblins -or -not $Terminal -or -not $Work) {
    throw 'no_visible_windows.ps1 needs -BaseGoblins, -Goblins, -Terminal and -Work.'
}
New-Item -ItemType Directory -Force $Work | Out-Null

Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
public static class Desktop {
    public delegate bool EnumProc(IntPtr window, IntPtr parameter);
    [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc callback, IntPtr parameter);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr window);
    [DllImport("user32.dll")] public static extern bool IsIconic(IntPtr window);
    // The visible top-level windows, each as its handle and whether it is
    // minimized.
    public static Dictionary<long, bool> Shown() {
        var shown = new Dictionary<long, bool>();
        EnumWindows((window, parameter) => {
            if (IsWindowVisible(window)) shown[window.ToInt64()] = IsIconic(window);
            return true;
        }, IntPtr.Zero);
        return shown;
    }
}
'@

# The runs share a scratch home and their own line, never a person's.
$env:GOBLINS_HOME = Join-Path $Work 'home'
$env:GOBLINS_STATE_OVERRIDE = Join-Path $Work 'home\state'
$env:GOBLINS_PROJECTS_ROOT = Join-Path $Work 'projects'
New-Item -ItemType Directory -Force $env:GOBLINS_HOME, $env:GOBLINS_STATE_OVERRIDE, $env:GOBLINS_PROJECTS_ROOT | Out-Null
# The baseline predates the rename and reads the same settings by their old
# names, so it shares the scratch home too.
. (Join-Path $PSScriptRoot '..\pre_rename_build.ps1')
foreach ($name in 'HOME', 'STATE_OVERRIDE', 'PROJECTS_ROOT') {
    Set-Item -LiteralPath "env:$PreRenameSettingPrefix$name" -Value (Get-Item -LiteralPath "env:CEO_$name").Value
}

$watchFor = 10
$bash = 'C:\Program Files\Git\bin\bash.exe'
# The command runs far past the watch, so every row that sees it run measures
# the watch itself, never how long this command happened to live.
$turn = 'gate turn -- ping -n 600 127.0.0.1'

$results = @()
foreach ($build in @(@{ Name = 'main'; Goblins = $BaseGoblins }, @{ Name = 'this branch'; Goblins = $Goblins })) {
    $program = $build.Goblins
    $results += Watch-Start 'Win32_Process Create with no ShowWindow' $build.Name {
        Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{ CommandLine = "`"$program`" $turn" } | Out-Null
    }
    $results += Watch-Start 'Start-Process at the normal window style' $build.Name {
        Start-Process -FilePath $program -ArgumentList $turn
    }
    # Its output goes to files, as a careless start's sometimes does.
    $said = Join-Path $Work "said-$($build.Name.Replace(' ', '-'))"
    $results += Watch-Start 'Start-Process with its output in files' $build.Name {
        Start-Process -FilePath $program -ArgumentList $turn -RedirectStandardOutput "$said.out" -RedirectStandardError "$said.err"
    }
    $results += Watch-Start 'cmd /c start' $build.Name {
        Start-Exactly 'cmd.exe' "/c start `"`" `"$program`" $turn"
    }
    # Git for Windows' start, which hands its arguments to cmd's start.
    $results += Watch-Start 'a new console from Git Bash' $build.Name {
        Start-Exactly $bash "-c `"start '$($program.Replace('\', '/'))' $turn`""
    }
}
$log = Join-Path $Work 'detached.log'
$results += Watch-Start 'goblins gate turn --detach' 'this branch' {
    Start-Exactly $Goblins "gate turn --detach `"$log`" -- ping -n 3 127.0.0.1"
}
$detachedEnd = (Get-Content $log -ErrorAction SilentlyContinue | Select-Object -Last 1)

$results | Format-Table -AutoSize | Out-String -Width 200
"detached run's last line: $detachedEnd"

$verdict = Get-Verdict -Results $results -WatchFor $watchFor -DetachedEnd $detachedEnd
if ($env:GITHUB_STEP_SUMMARY) {
    Write-Summary -Results $results -Terminal $Terminal -DetachedEnd $detachedEnd -Verdict $verdict -Path $env:GITHUB_STEP_SUMMARY
}
# An environment result is loud and green: the summary names it, and a
# warning annotation carries the same words onto the run. The change's own
# rows still fail.
foreach ($line in $verdict.Environment) {
    Write-Output "::warning title=Environment result::$line"
}
if ($verdict.Failures.Count -gt 0) {
    $verdict.Failures | ForEach-Object { Write-Error $_ -ErrorAction Continue }
    exit 1
}
if ($verdict.Environment.Count -gt 0) {
    Write-Output 'no_visible_windows: this runner cannot host the proof, so the rows for main are an environment result, not a failure of the change.'
}
