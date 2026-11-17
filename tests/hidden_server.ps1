# hidden_server.ps1 - starts a Python server a job needs, such as a release
# server or the stand-in model, in a hidden window, and waits until it
# answers, saying how long that took. Its window is hidden, so
# hidden_server.py writes what it prints to <Log>.out and <Log>.err, and where
# it was every 5 seconds until it listened to <Log>.stack, and a wait that
# fails says why with all three: the code it exited with, or, when it still
# runs at the deadline, its pid and its last request's error. A server that
# did not answer by the deadline is ended by that pid.
#
#   . .\tests\hidden_server.ps1
#   $server = Start-HiddenServer -Name <name> -ArgumentList <python's arguments> -Url <url> -Log <path> [-Seconds 30]
function Start-HiddenServer(
    [Parameter(Mandatory = $true)][string]$Name,
    [Parameter(Mandatory = $true)][string[]]$ArgumentList,
    [Parameter(Mandatory = $true)][string]$Url,
    [Parameter(Mandatory = $true)][string]$Log,
    [int]$Seconds = 30
) {
    $stackSeconds = 5
    # An earlier run's files at this log are not this server's.
    Remove-Item -LiteralPath "$Log.out", "$Log.err", "$Log.stack" -ErrorAction SilentlyContinue
    $clock = [Diagnostics.Stopwatch]::StartNew()
    $server = Start-Process -FilePath python -ArgumentList (@((Join-Path $PSScriptRoot "hidden_server.py"), $Log, $stackSeconds) + $ArgumentList) -WindowStyle Hidden -PassThru
    # Windows PowerShell reads a process's exit code through the handle it
    # took while the process ran.
    $null = $server.Handle
    $output = {
        # hidden_server.py makes <Log>.out first, so a server without one was
        # still starting Python.
        $ran = if (Test-Path -LiteralPath "$Log.out") { "" } else { "Python had not yet run hidden_server.py: it made no $Log.out`n" }
        "${ran}its output:`n$(Get-Content -Raw -LiteralPath "$Log.out" -ErrorAction SilentlyContinue)`nits errors:`n$(Get-Content -Raw -LiteralPath "$Log.err" -ErrorAction SilentlyContinue)`nwhere it was every $stackSeconds seconds until it listened:`n$(Get-Content -Raw -LiteralPath "$Log.stack" -ErrorAction SilentlyContinue)"
    }
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ($true) {
        try {
            Invoke-WebRequest $Url -UseBasicParsing -TimeoutSec 5 | Out-Null
            Write-Host "the $Name answered $Url after $([Math]::Round($clock.Elapsed.TotalSeconds, 1)) seconds"
            return $server
        }
        catch { $failure = $_.Exception.Message }
        if ($server.HasExited) {
            throw "the $Name exited with code $($server.ExitCode) before it answered $Url.`n$(& $output)"
        }
        if ((Get-Date) -gt $deadline) {
            $message = "the $Name did not answer within $Seconds seconds and was ended: it still ran as pid $($server.Id), and its last request to $Url failed: $failure`n$(& $output)"
            Stop-Process -Id $server.Id -ErrorAction SilentlyContinue
            throw $message
        }
        Start-Sleep -Milliseconds 250
    }
}
