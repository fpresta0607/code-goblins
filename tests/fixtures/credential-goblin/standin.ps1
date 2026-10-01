# A stand-in goblin for the credential canary proof: it runs in a native
# terminal under cfo host the way a Claude Code goblin does, files one
# credential request for each of its names for its own task from that
# terminal, and then answers what it is told the way Claude Code's screen does:
# "bypass permissions on" while it waits, a spinner row while it takes a line.
# Its transcript is everything it was told, and what it found when it
# re-sourced auth.ps1: whether the first name is set and whether its value's
# SHA-256 matches the one the proof expects. It never prints, logs or passes on
# a value itself.
param(
    [Parameter(Mandatory=$true)][string]$Cfo,
    [Parameter(Mandatory=$true)][string]$Project,
    [Parameter(Mandatory=$true)][string]$Task,
    [Parameter(Mandatory=$true)][string]$Names,
    [Parameter(Mandatory=$true)][string]$Transcript,
    [Parameter(Mandatory=$true)][string]$ExpectedSha256
)
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
function Note([string]$Line) { Add-Content -LiteralPath $Transcript -Value $Line -Encoding UTF8 }
$requested = $Names -split ','
$Name = $requested[0]

Note "stand-in goblin $Task started"
# The host records itself just after it starts this program, so each request
# is retried until the terminal can prove whose it is.
foreach ($asked in $requested) {
    $filed = $false
    for ($attempt = 1; $attempt -le 15 -and -not $filed; $attempt++) {
        $output = & $Cfo auth request --project $Project --task $Task --why 'Canary proof for the credential card' $asked 2>&1 | Out-String
        $filed = $LASTEXITCODE -eq 0
        Note ("cfo auth request $asked (attempt $attempt): " + $output.Trim())
        if (-not $filed) { Start-Sleep -Seconds 2 }
    }
}

$working = [string][char]0x273D + ' Reading the notice' + [string][char]0x2026
Write-Host 'bypass permissions on'
while ($true) {
    $line = [Console]::In.ReadLine()
    if ($null -eq $line) { break }
    Note ('told: ' + $line)
    Write-Host $working
    if ($line -match 're-source (.+auth\.ps1)\s*$') {
        # An error could quote the script's line, so only its failure is noted.
        $sourced = $true
        try { . $Matches[1] } catch { $sourced = $false }
        if ($sourced) {
            $value = [Environment]::GetEnvironmentVariable($Name)
            $sha = ''
            if ($value) {
                $bytes = [Security.Cryptography.SHA256]::Create().ComputeHash([Text.Encoding]::UTF8.GetBytes($value))
                $sha = -join ($bytes | ForEach-Object { $_.ToString('x2') })
            }
            Remove-Variable value
            Note ("re-sourced auth.ps1: $Name set " + [bool]$sha + ', matches the expected SHA-256 ' + ($sha -eq $ExpectedSha256))
        } else {
            Note 're-sourcing auth.ps1 failed'
        }
    }
    Start-Sleep -Seconds 3
    Write-Host 'bypass permissions on'
}
