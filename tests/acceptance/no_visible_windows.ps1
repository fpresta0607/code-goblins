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

# Measure runs a start and watches the desktop for $watchFor seconds: the
# windows it opened, how long after the start the last one was still in
# front, and how long the last one was there at all, minimized or not. The
# processes it started are ended by their pids.
function Measure([string]$Name, [string]$Build, [scriptblock]$Start) {
    $before = [Desktop]::Shown()
    $existing = @{}
    foreach ($process in Get-Process) { $existing[$process.Id] = $true }
    $started = Get-Date
    & $Start
    $opened = @{}
    $front = -1
    $any = -1
    while (((Get-Date) - $started).TotalSeconds -lt $watchFor) {
        $at = [int]((Get-Date) - $started).TotalMilliseconds
        foreach ($entry in ([Desktop]::Shown()).GetEnumerator()) {
            if ($before.ContainsKey($entry.Key)) { continue }
            $opened[$entry.Key] = $true
            $any = $at
            if (-not $entry.Value) { $front = $at }
        }
        Start-Sleep -Milliseconds 100
    }
    foreach ($process in Get-Process) {
        if (-not $existing[$process.Id] -and $process.ProcessName -in 'cfo', 'PING', 'cmd', 'bash', 'conhost', 'OpenConsole') {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        }
    }
    Start-Sleep -Seconds 1
    [pscustomobject]@{ Terminal = $Terminal; Build = $Build; Start = $Name; Windows = $opened.Count; InFrontUntilMs = $front; ShownUntilMs = $any }
}

$results = @()
foreach ($build in @(@{ Name = 'main'; Cfo = $BaseCfo }, @{ Name = 'this branch'; Cfo = $Cfo })) {
    $program = $build.Cfo
    $unix = '/' + $program.Replace(':', '').Replace('\', '/')
    $turn = @('gate', 'turn', '--', 'ping', '-n', '30', '127.0.0.1')
    $results += Measure 'Win32_Process Create with no ShowWindow' $build.Name {
        Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{ CommandLine = "`"$program`" $($turn -join ' ')" } | Out-Null
    }
    $results += Measure 'Start-Process at the normal window style' $build.Name {
        Start-Process -FilePath $program -ArgumentList $turn
    }
    $results += Measure 'cmd /c start' $build.Name {
        cmd /c start "`"`"" "`"$program`"" @turn
    }
    $results += Measure 'a new console from Git Bash' $build.Name {
        & $bash -c "start '$unix' $($turn -join ' ')"
    }
}
$log = Join-Path $Work 'detached.log'
$results += Measure 'cfo gate turn --detach' 'this branch' {
    & $Cfo gate turn --detach $log -- ping -n 3 127.0.0.1 | Out-Host
    if ($LASTEXITCODE -ne 0) { throw "cfo gate turn --detach exited $LASTEXITCODE" }
}
$detachedEnd = (Get-Content $log -ErrorAction SilentlyContinue | Select-Object -Last 1)

$results | Format-Table -AutoSize | Out-String -Width 200
"detached run's last line: $detachedEnd"

if ($env:GITHUB_STEP_SUMMARY) {
    $summary = @("### $Terminal as the default terminal", '', '| Build | Start | New windows | In front until | Shown until |', '|---|---|---|---|---|')
    foreach ($row in $results) {
        $front = if ($row.InFrontUntilMs -lt 0) { 'never' } else { "$($row.InFrontUntilMs) ms" }
        $shown = if ($row.ShownUntilMs -lt 0) { 'never' } else { "$($row.ShownUntilMs) ms" }
        $summary += "| $($row.Build) | $($row.Start) | $($row.Windows) | $front | $shown |"
    }
    $summary += '', "Detached run's last line: ``$detachedEnd``", ''
    $summary | Add-Content -Path $env:GITHUB_STEP_SUMMARY
}

# The branch's careless starts show nothing beyond a moment, and its
# detached run nothing at all. main's careless starts showing a window is the
# premise: without it this proof would prove nothing.
$failures = @()
foreach ($row in $results) {
    if ($row.Build -eq 'main' -and $row.ShownUntilMs -lt ($watchFor * 1000 - 1000)) {
        $failures += "main's $($row.Start) showed no window for the whole watch, so this desktop does not show what the proof needs"
    }
    if ($row.Build -eq 'this branch' -and $row.Start -ne 'cfo gate turn --detach' -and $row.ShownUntilMs -gt 1000) {
        $failures += "this branch's $($row.Start) still showed a window $($row.ShownUntilMs) ms after it started"
    }
    if ($row.Start -eq 'cfo gate turn --detach' -and $row.Windows -ne 0) {
        $failures += "cfo gate turn --detach showed $($row.Windows) windows"
    }
}
if ($detachedEnd -notlike 'cfo gate turn: the detached run ended with exit 0 after *') {
    $failures += "the detached run's log ends with '$detachedEnd', not how it ended"
}
if ($failures.Count -gt 0) {
    $failures | ForEach-Object { Write-Error $_ -ErrorAction Continue }
    exit 1
}
