param(
    [Parameter(Mandatory = $true)][string]$Cfo,
    [Parameter(Mandatory = $true)][string]$Work,
    [Parameter(Mandatory = $true)][string]$Phase
)
$ErrorActionPreference = 'Stop'
New-Item -ItemType Directory -Force $Work | Out-Null

Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;
public static class Desk {
    public delegate bool EnumProc(IntPtr hwnd, IntPtr lp);
    [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb, IntPtr lp);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
    [DllImport("user32.dll")] public static extern bool IsIconic(IntPtr h);
    [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] public static extern int GetClassName(IntPtr h, StringBuilder s, int n);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] public static extern int GetWindowText(IntPtr h, StringBuilder s, int n);
    public static List<string> Visible() {
        var found = new List<string>();
        EnumWindows((h, lp) => {
            if (!IsWindowVisible(h)) return true;
            uint pid; GetWindowThreadProcessId(h, out pid);
            var c = new StringBuilder(256); GetClassName(h, c, 256);
            var t = new StringBuilder(256); GetWindowText(h, t, 256);
            found.Add(h.ToInt64() + "|" + pid + "|" + c + "|" + t + "|" + (IsIconic(h) ? "minimized" : "normal"));
            return true;
        }, IntPtr.Zero);
        return found;
    }
}
'@

$probe = Join-Path $Work 'probe.exe'
if (-not (Test-Path $probe)) {
    Add-Type -OutputType ConsoleApplication -OutputAssembly $probe -TypeDefinition @'
using System;
using System.IO;
using System.Runtime.InteropServices;
using System.Threading;
public static class Probe {
    [DllImport("kernel32.dll")] static extern IntPtr GetConsoleWindow();
    [DllImport("kernel32.dll")] static extern bool FreeConsole();
    [DllImport("kernel32.dll")] static extern uint GetConsoleProcessList(uint[] list, uint count);
    [DllImport("user32.dll")] static extern bool ShowWindow(IntPtr h, int cmd);
    [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr h);
    [DllImport("kernel32.dll")] static extern IntPtr GetStdHandle(int n);
    [DllImport("kernel32.dll")] static extern bool GetConsoleMode(IntPtr h, out uint m);
    [DllImport("kernel32.dll")] static extern bool SetConsoleMode(IntPtr h, uint m);
    public static void Main(string[] args) {
        string mode = args[0];
        IntPtr w = GetConsoleWindow();
        var list = new uint[16];
        uint count = GetConsoleProcessList(list, 16);
        string report = "count=" + count + " window=" + w + " visible=" + IsWindowVisible(w);
        if (mode == "hide" || mode == "hidefree") ShowWindow(w, 0);
        if (mode == "minhide") { ShowWindow(w, 7); ShowWindow(w, 0); }
        if (mode == "vt") {
            IntPtr o = GetStdHandle(-11); uint m; GetConsoleMode(o, out m); SetConsoleMode(o, m | 4);
            Console.Out.Write("\x1b[2t"); Console.Out.Flush();
        }
        if (mode == "free" || mode == "hidefree") FreeConsole();
        report += " after: window=" + GetConsoleWindow() + " visible=" + IsWindowVisible(GetConsoleWindow());
        File.WriteAllText(args[1], report);
        Thread.Sleep(8000);
    }
}
'@
}

function Watch-Windows([string]$Name, [scriptblock]$Start, [int]$Seconds = 8) {
    $before = @{}
    foreach ($line in [Desk]::Visible()) { $before[$line.Split('|')[0]] = $true }
    $started = Get-Date
    & $Start
    $seen = @{}
    $deadline = $started.AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        $at = ((Get-Date) - $started).TotalMilliseconds
        foreach ($line in [Desk]::Visible()) {
            $parts = $line.Split('|')
            if ($before[$parts[0]]) { continue }
            if (-not $seen[$parts[0]]) { $seen[$parts[0]] = @{ First = $at; Last = $at; Samples = 0; Line = $line; States = @{} } }
            $seen[$parts[0]].Last = $at
            $seen[$parts[0]].Samples++
            $seen[$parts[0]].States[$parts[4]] = $true
        }
        Start-Sleep -Milliseconds 100
    }
    if ($seen.Count -eq 0) { "[$Phase] $Name : no new visible window" }
    foreach ($entry in $seen.Values) {
        $parts = $entry.Line.Split('|')
        $owner = (Get-Process -Id $parts[1] -ErrorAction SilentlyContinue).ProcessName
        "[$Phase] $Name : window class=$($parts[2]) title='$($parts[3])' owner=$owner first=$([int]$entry.First)ms last=$([int]$entry.Last)ms samples=$($entry.Samples) states=$($entry.States.Keys -join ',')"
    }
}

"[$Phase] session=$([Diagnostics.Process]::GetCurrentProcess().SessionId) interactive=$([Environment]::UserInteractive) explorer=$((Get-Process explorer -ErrorAction SilentlyContinue).Count) visible-windows=$(([Desk]::Visible()).Count)"
Get-ItemProperty 'HKCU:\Console\%%Startup' -ErrorAction SilentlyContinue | Out-String

foreach ($mode in 'none', 'hide', 'free', 'hidefree', 'minhide', 'vt') {
    $report = Join-Path $Work "probe-$Phase-$mode.txt"
    Watch-Windows "probe $mode" { Start-Process -FilePath $probe -ArgumentList $mode, $report }
    "[$Phase] probe $mode reported: $(Get-Content $report -ErrorAction SilentlyContinue)"
    Get-Process probe -ErrorAction SilentlyContinue | Stop-Process -Force
}

$env:CFO_HOME = Join-Path $Work 'home'
$env:CFO_STATE_OVERRIDE = Join-Path $Work 'home\state'
$env:CFO_PROJECTS_ROOT = Join-Path $Work 'projects'
New-Item -ItemType Directory -Force $env:CFO_HOME, $env:CFO_STATE_OVERRIDE, $env:CFO_PROJECTS_ROOT | Out-Null
$turn = "gate turn -- ping -n 12 127.0.0.1"
$bash = 'C:\Program Files\Git\bin\bash.exe'
$cfoUnix = '/' + $Cfo.Replace(':', '').Replace('\', '/')
$starts = [ordered]@{
    'Win32_Process Create' = { Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{ CommandLine = "`"$Cfo`" $turn" } | Out-Null }
    'Start-Process normal' = { Start-Process -FilePath $Cfo -ArgumentList $turn.Split(' ') }
    'cmd /c start' = { cmd /c start "`"`"" "`"$Cfo`"" gate turn -- ping -n 12 127.0.0.1 }
    'Git Bash start' = { & $bash -c "start '$cfoUnix' gate turn -- ping -n 12 127.0.0.1" }
    'Git Bash setsid' = {
        $info = New-Object Diagnostics.ProcessStartInfo $bash, "-c `"setsid '$cfoUnix' gate turn -- ping -n 12 127.0.0.1 > /dev/null 2>&1 &`""
        $info.UseShellExecute = $false; $info.CreateNoWindow = $true
        [Diagnostics.Process]::Start($info) | Out-Null
    }
}
foreach ($name in $starts.Keys) {
    Watch-Windows $name $starts[$name] 10
    Get-Process cfo, PING -ErrorAction SilentlyContinue | Stop-Process -Force
    Start-Sleep -Seconds 1
}
