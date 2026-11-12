# hidden_server.ps1 - starts a Python server a job needs, such as a release
# server or the stand-in model, in a hidden window, and waits until it
# answers. Its window is hidden, so hidden_server.py writes what it prints to
# <Log>.out and <Log>.err, and a wait that fails says why with both: the code
# it exited with, or, when it still runs at the deadline, its pid and its last
# request's error. A server that did not answer by the deadline is ended by
# that pid.
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
    $server = Start-Process -FilePath python -ArgumentList (@((Join-Path $PSScriptRoot "hidden_server.py"), $Log) + $ArgumentList) -WindowStyle Hidden -PassThru
    # Windows PowerShell reads a process's exit code through the handle it
    # took while the process ran.
    $null = $server.Handle
    $output = { "its output:`n$(Get-Content -Raw -LiteralPath "$Log.out" -ErrorAction SilentlyContinue)`nits errors:`n$(Get-Content -Raw -LiteralPath "$Log.err" -ErrorAction SilentlyContinue)" }
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ($true) {
        try { Invoke-WebRequest $Url -UseBasicParsing -TimeoutSec 5 | Out-Null; return $server }
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
