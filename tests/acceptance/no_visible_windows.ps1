# Proves on a hosted runner's desktop, never on a person's, how long each
# careless start of cfo gate turn shows a window, with the build of main and
# with this commit's, and that cfo gate turn --detach shows none at all. The
# default terminal is whatever the runner is set to: the workflow runs it
# once with the console host and once with Windows Terminal.
param(
    [Parameter(Mandatory = $true)][string]$BaseCfo,
    [Parameter(Mandatory = $true)][string]$Cfo,
    [Parameter(Mandatory = $true)][string]$Terminal,
    [Parameter(Mandatory = $true)][string]$Work
)
$ErrorActionPreference = 'Stop'
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
$env:CFO_HOME = Join-Path $Work 'home'
$env:CFO_STATE_OVERRIDE = Join-Path $Work 'home\state'
$env:CFO_PROJECTS_ROOT = Join-Path $Work 'projects'
New-Item -ItemType Directory -Force $env:CFO_HOME, $env:CFO_STATE_OVERRIDE, $env:CFO_PROJECTS_ROOT | Out-Null

$watchFor = 10
$bash = 'C:\Program Files\Git\bin\bash.exe'
$turn = 'gate turn -- ping -n 30 127.0.0.1'

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
    $told = $false
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
        # Each cfo the start ran, held by its handle so its exit code can be
        # read once it has ended.
        foreach ($process in Get-Process -Name cfo -ErrorAction SilentlyContinue) {
            if (-not $existing[$process.Id] -and -not $runs.ContainsKey($process.Id)) {
                $null = $process.Handle
                $runs[$process.Id] = $process
            }
        }
        # Three seconds in, what the run's line and its cfo say, for a run
        # whose command did not start.
        if ($at -ge 3000 -and -not $told) {
            $told = $true
            $alive = (Get-Process -Name cfo -ErrorAction SilentlyContinue | Where-Object { -not $existing[$_.Id] } | ForEach-Object { $_.Id }) -join ', '
            "[$Terminal / $Build / $Name] cfo running: $alive" | Out-Host
            & $Cfo gate turns 2>&1 | ForEach-Object { "  $_" } | Out-Host
        }
        Start-Sleep -Milliseconds 100
    }
    foreach ($run in $runs.Values) {
        $ended = if ($run.HasExited) { "ended with exit $($run.ExitCode)" } else { 'runs' }
        "[$Terminal / $Build / $Name] cfo pid $($run.Id) $ended" | Out-Host
    }
    foreach ($process in Get-Process) {
        if (-not $existing[$process.Id] -and $process.ProcessName -in 'cfo', 'PING', 'cmd', 'bash', 'conhost', 'OpenConsole') {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        }
    }
    Start-Sleep -Seconds 1
    [pscustomobject]@{ Terminal = $Terminal; Build = $Build; Start = $Name; Windows = $opened.Count; InFrontUntilMs = $front; ShownUntilMs = $any; CommandUntilMs = $command }
}

$results = @()
foreach ($build in @(@{ Name = 'main'; Cfo = $BaseCfo }, @{ Name = 'this branch'; Cfo = $Cfo })) {
    $program = $build.Cfo
    $results += Watch-Start 'Win32_Process Create with no ShowWindow' $build.Name {
        Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{ CommandLine = "`"$program`" $turn" } | Out-Null
    }
    $results += Watch-Start 'Start-Process at the normal window style' $build.Name {
        Start-Process -FilePath $program -ArgumentList $turn
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
$results += Watch-Start 'cfo gate turn --detach' 'this branch' {
    Start-Exactly $Cfo "gate turn --detach `"$log`" -- ping -n 3 127.0.0.1"
}
$detachedEnd = (Get-Content $log -ErrorAction SilentlyContinue | Select-Object -Last 1)

$results | Format-Table -AutoSize | Out-String -Width 200
# Why a run's command did not start, as its execution record says.
foreach ($store in @($env:CFO_VERIFY_DIR, (Join-Path $env:LOCALAPPDATA 'cfoerify')) | Where-Object { $_ -and (Test-Path $_) }) {
    Get-ChildItem -Recurse -File -Path $store | Select-String -Pattern '"failure"' | ForEach-Object { "record $($_.Path): $($_.Line.Trim())" }
}
"detached run's last line: $detachedEnd"

if ($env:GITHUB_STEP_SUMMARY) {
    $summary = @("### $Terminal as the default terminal", '', '| Build | Start | New windows | In front until | Shown until | Its command ran until |', '|---|---|---|---|---|---|')
    foreach ($row in $results) {
        $front = if ($row.InFrontUntilMs -lt 0) { 'never' } else { "$($row.InFrontUntilMs) ms" }
        $shown = if ($row.ShownUntilMs -lt 0) { 'never' } else { "$($row.ShownUntilMs) ms" }
        $ran = if ($row.CommandUntilMs -lt 0) { 'never' } else { "$($row.CommandUntilMs) ms" }
        $summary += "| $($row.Build) | $($row.Start) | $($row.Windows) | $front | $shown | $ran |"
    }
    $summary += '', "Detached run's last line: ``$detachedEnd``", ''
    $summary | Add-Content -Path $env:GITHUB_STEP_SUMMARY
}

# Every careless start reached its command and ran it to the end of the
# watch, so each measures a real run. main's showed a window for the whole
# watch, the premise without which this proof would prove nothing. The
# branch's show nothing beyond a moment, and its detached run nothing at all.
$failures = @()
foreach ($row in $results) {
    $isCareless = $row.Start -ne 'cfo gate turn --detach'
    if ($isCareless -and $row.CommandUntilMs -lt ($watchFor * 1000 - 1000)) {
        $failures += "$($row.Build)'s $($row.Start) did not keep its command running for the watch, so it measures no real run"
    }
    if ($row.Build -eq 'main' -and $row.ShownUntilMs -lt ($watchFor * 1000 - 1000)) {
        $failures += "main's $($row.Start) showed no window for the whole watch, so this desktop does not show what the proof needs"
    }
    if ($row.Build -eq 'this branch' -and $isCareless -and $row.ShownUntilMs -gt 1000) {
        $failures += "this branch's $($row.Start) still showed a window $($row.ShownUntilMs) ms after it started"
    }
    if (-not $isCareless -and ($row.Windows -ne 0 -or $row.CommandUntilMs -lt 0)) {
        $failures += "cfo gate turn --detach showed $($row.Windows) windows and ran its command until $($row.CommandUntilMs) ms"
    }
}
if ($detachedEnd -notlike 'cfo gate turn: the detached run ended with exit 0 after *') {
    $failures += "the detached run's log ends with '$detachedEnd', not how it ended"
}
if ($failures.Count -gt 0) {
    $failures | ForEach-Object { Write-Error $_ -ErrorAction Continue }
    exit 1
}
