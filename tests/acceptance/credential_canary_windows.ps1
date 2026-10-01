# The credential canary proof. A stand-in goblin of a throwaway project asks
# for a throwaway credential from its own terminal; the proof enters a random
# canary through the real card in a headless browser, then searches every
# place a value must never reach and prints hit counts only. The canary lives
# in this process's memory and in one temporary file the browser driver
# deletes the moment it reads it: it is never printed, never an argument and
# never logged.
#
# It builds its own CFO home with its own board on a free port, so it touches
# nothing of the live fleet except one throwaway entry in Windows Credential
# Manager, which it deletes at the end and says so. Every process it starts it
# stops by its pid.
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File tests\acceptance\credential_canary_windows.ps1 -Binary .\cfo.exe [-Transcript <agent session .jsonl> ...]
#
# -Transcript names the transcripts of the agent sessions that ran the proof,
# such as the Claude Code session of the goblin or the CFO, which are searched
# too. It exits 0 only when every check holds.
[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)][string]$Binary,
    [string[]]$Transcript = @()
)
$ErrorActionPreference = 'Stop'
foreach ($name in @('CFO_HOME','CFO_STATE_OVERRIDE','CFO_TASK_ID','CFO_SPAWN_GEN','CFO_ROLE','CFO_HOST_ID','CFO_PARENT_SESSION_ID','CFO_ROOT_SESSION_ID','CFO_PARENT_HARNESS','CFO_CREDENTIAL_DIR')) {
    Remove-Item "Env:$name" -ErrorAction SilentlyContinue
}
$cfo = (Resolve-Path -LiteralPath $Binary).Path
$repository = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..')).Path
$utf8 = [Text.UTF8Encoding]::new($false)
function Write-UTF8([string]$Path, [string]$Content) { [IO.File]::WriteAllText($Path, $Content, $utf8) }
function Hex([byte[]]$Bytes) { -join ($Bytes | ForEach-Object { $_.ToString('x2') }) }
function Random-Hex([int]$Count) { $bytes = [byte[]]::new($Count); [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes); Hex $bytes }
function Sha256([string]$Text) { Hex ([Security.Cryptography.SHA256]::Create().ComputeHash($utf8.GetBytes($Text))) }
# Native runs a program and answers what it printed. Windows PowerShell turns
# each line a program writes to stderr into an error record once stderr is
# redirected, which Stop would throw on, so a run is judged by its exit code.
function Native([string]$Command, [string[]]$Arguments) {
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try { return (& $Command @Arguments 2>&1 | Out-String) } finally { $ErrorActionPreference = $previous }
}
function Checked([string]$Command, [string[]]$Arguments) {
    $output = Native $Command $Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Command $($Arguments -join ' ') failed ($LASTEXITCODE): $output" }
    return $output
}

$suffix = Random-Hex 4
$root = Join-Path ([IO.Path]::GetTempPath()) ('cfo-canary-' + $suffix)
$cfoHome = Join-Path $root 'home'
$state = Join-Path $cfoHome 'state'
$data = Join-Path $cfoHome 'data'
$logs = Join-Path $root 'logs'
# The throwaway scope is the project folder's name: short enough that it never
# reads as random text to the request's own value check.
$project = Join-Path $root ('cgproof' + $suffix)
$scope = Split-Path $project -Leaf
$name = 'CG_PROOF_TOKEN'
$expiring = 'CG_PROOF_EXPIRING'
$task = 'canary-goblin'
$canary = 'canary' + (Random-Hex 24)
$refusalCanary = 'refused' + (Random-Hex 24)
$canarySha = Sha256 $canary
$standinTranscript = Join-Path $root 'standin-transcript.log'
$results = [ordered]@{}
$failures = [Collections.Generic.List[string]]::new()
function Check([string]$What, [bool]$Holds) {
    $results[$What] = $Holds
    if (-not $Holds) { $failures.Add($What) }
}
$serve = $null
$hostProcess = $null
$port = 0
$board = ''
$canaryFile = ''

function Start-Board {
    $process = Start-Process -FilePath $cfo -ArgumentList @('serve','--listen',"127.0.0.1:$port",'--example') -WindowStyle Hidden -RedirectStandardOutput (Join-Path $logs ("serve-" + (Get-Date).Ticks + ".out")) -RedirectStandardError (Join-Path $logs ("serve-" + (Get-Date).Ticks + ".err")) -PassThru
    $deadline = (Get-Date).AddSeconds(45)
    do {
        Start-Sleep -Milliseconds 300
        try { $null = Invoke-WebRequest -UseBasicParsing -Uri "$board/api/snapshot" -TimeoutSec 5; return $process } catch { }
    } until ((Get-Date) -gt $deadline)
    throw "the scratch board did not answer on $board"
}
function Snapshot { Invoke-RestMethod -Uri "$board/api/snapshot" -TimeoutSec 15 }
function Request-For([string]$Name) {
    $found = @((Snapshot).credentials | Where-Object { $_.names -contains $Name })
    if ($found.Count -eq 0) { return $null }
    return $found[0]
}
# Probe posts body to the board with curl, which lets it set any header; the
# body goes through a temporary file it deletes, never the command line.
function Probe([string]$Path, [string]$Body, [hashtable]$Headers) {
    $file = [IO.Path]::GetTempFileName()
    try {
        Write-UTF8 $file $Body
        $arguments = @('-s','-o','NUL','-w','%{http_code}','-X','POST',"$board$Path",'-H','Content-Type: application/json','--data-binary',"@$file")
        foreach ($header in $Headers.GetEnumerator()) { $arguments += @('-H', "$($header.Key): $($header.Value)") }
        return [int](& curl.exe @arguments)
    } finally {
        Remove-Item -LiteralPath $file -Force -ErrorAction SilentlyContinue
    }
}
function Save-Body($Request, [string]$Name, [string]$Value) {
    return (@{ id = $Request.id; generation = $Request.generation; values = @{ $Name = $Value } } | ConvertTo-Json -Compress)
}
# Hits counts the files under each root whose bytes hold text.
function Hits([string[]]$Roots, [string]$Text) {
    $needle = $utf8.GetBytes($Text)
    $found = [Collections.Generic.List[string]]::new()
    foreach ($root in $Roots) {
        if (-not (Test-Path -LiteralPath $root)) { continue }
        foreach ($file in Get-ChildItem -LiteralPath $root -Recurse -File -Force -ErrorAction SilentlyContinue) {
            try { $bytes = [IO.File]::ReadAllBytes($file.FullName) } catch { continue }
            $text = [Text.Encoding]::GetEncoding(28591).GetString($bytes)
            if ($text.Contains([Text.Encoding]::GetEncoding(28591).GetString($needle))) { $found.Add($file.FullName) }
        }
    }
    return ,$found
}
function Owner-Only([string]$Path) {
    $me = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $rules = @((Get-Acl -LiteralPath $Path).Access)
    return $rules.Count -gt 0 -and -not (@($rules | Where-Object { $_.IdentityReference.Translate([Security.Principal.SecurityIdentifier]) -ne $me }).Count)
}

Add-Type -Namespace CanaryProof -Name Vault -MemberDefinition @'
[DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
public static extern bool CredDeleteW(string target, int type, int flags);
'@

try {
    foreach ($dir in @($root, $cfoHome, $state, $data, $logs, $project, (Join-Path $data "projects\$scope"))) { New-Item -ItemType Directory -Force -Path $dir | Out-Null }
    # The scratch home is a primary home, as one cfo install sets up outside a
    # checkout is, so cfo cleanup works in it.
    Write-UTF8 (Join-Path $cfoHome 'AGENTS.md') "# Canary proof home`n"
    Write-UTF8 (Join-Path $cfoHome '.cfo-home') ''
    $env:CFO_HOME = $cfoHome
    $env:CFO_STATE_OVERRIDE = $state
    Write-UTF8 (Join-Path $data "projects\$scope\auth.json") ('{"project":"' + $scope + '","services":[{"name":"proof","method":"env","env":["' + $name + '"],"optional":true}],"formats":[{"names":["CG_PROOF_*"],"prefixes":["canary"]}]}')

    # The throwaway project is a checkout with the stand-in goblin's worktree.
    Checked 'git' @('-C',$project,'init','-q','--initial-branch=main') | Out-Null
    Checked 'git' @('-C',$project,'config','user.name','Canary proof') | Out-Null
    Checked 'git' @('-C',$project,'config','user.email','canary@example.invalid') | Out-Null
    Write-UTF8 (Join-Path $project '.gitignore') ".worktrees/`n"
    Checked 'git' @('-C',$project,'add','.') | Out-Null
    Checked 'git' @('-C',$project,'commit','-qm','Start the throwaway project') | Out-Null
    $worktree = Join-Path $project ".worktrees\gb-$task"
    Checked 'git' @('-C',$project,'worktree','add','-q','-b','standin',$worktree) | Out-Null
    $taskTmp = Join-Path $state "tasktmp\$task"
    New-Item -ItemType Directory -Force -Path $taskTmp | Out-Null
    Write-UTF8 (Join-Path $state "$task.meta") ((@(
        "worktree=$worktree", "project=$project", 'harness=claude', 'kind=ship', 'mode=direct-PR', 'yolo=no',
        "tasktmp=$taskTmp", 'spawn_gen=canary-1', 'backend=native', 'title=Canary stand-in goblin'
    ) -join "`n") + "`n")

    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $listener.Start(); $port = $listener.LocalEndpoint.Port; $listener.Stop()
    $board = "http://127.0.0.1:$port"
    $serve = Start-Board
    Write-Host "scratch board $board, home $cfoHome, throwaway scope $scope"

    # The stand-in goblin asks from its own terminal, as any goblin does.
    $standin = Join-Path $repository 'tests\fixtures\credential-goblin\standin.ps1'
    $hostProcess = Start-Process -FilePath $cfo -ArgumentList @('host','--state',$state,'--id',$task,'--dir',$worktree,'--','powershell.exe','-NoLogo','-NoProfile','-ExecutionPolicy','Bypass','-File',$standin,'-Cfo',$cfo,'-Project',$project,'-Task',$task,'-Names',"$name,$expiring",'-Transcript',$standinTranscript,'-ExpectedSha256',$canarySha) -WindowStyle Hidden -PassThru
    $deadline = (Get-Date).AddSeconds(60)
    while (-not ((Request-For $name) -and (Request-For $expiring)) -and (Get-Date) -lt $deadline) { Start-Sleep -Seconds 1 }
    $request = Request-For $name
    Check 'the stand-in goblin filed its requests from its own terminal' ([bool]$request -and [bool](Request-For $expiring))
    if (-not $request) { throw 'no request reached the board' }
    Check 'the request names the throwaway repository and scope' ($request.project -eq $scope -and $request.repository -eq $project)
    $token = (Snapshot).instance
    $loopback = @{ Origin = $board; 'X-CFO-Token' = $token }

    # Refusals, each carrying a second canary that must reach nowhere.
    $refused = Save-Body $request $name $refusalCanary
    Check 'a save through a forwarding proxy is refused (403)' ((Probe '/api/credentials/save' $refused ($loopback + @{ 'X-Forwarded-For' = '100.101.102.103' })) -eq 403)
    Check 'a save with Tailscale identity headers is refused (403)' ((Probe '/api/credentials/save' $refused ($loopback + @{ 'Tailscale-User-Login' = 'overlord@example.com' })) -eq 403)
    Check 'a save under a non-loopback Host is refused (403)' ((Probe '/api/credentials/save' $refused ($loopback + @{ Host = "goblins.example.ts.net:$port" })) -eq 403)
    Check 'a save without the board token is refused (403)' ((Probe '/api/credentials/save' $refused @{ Origin = $board }) -eq 403)
    Check 'a save for a name the request does not ask for is refused (400)' ((Probe '/api/credentials/save' (Save-Body $request 'CG_PROOF_OTHER' $refusalCanary) $loopback) -eq 400)

    # An expired request: the board is stopped, the second request's expiry
    # moved into the past in its database, and the board started again.
    $expiringRequest = Request-For $expiring
    Stop-Process -Id $serve.Id -Force; $serve.WaitForExit(15000) | Out-Null; $serve = $null
    $database = Join-Path $state '.supervisor.json'
    $past = (Get-Date).ToUniversalTime().AddHours(-1).ToString('yyyy-MM-ddTHH:mm:ssZ')
    $json = [IO.File]::ReadAllText($database)
    $edited = [regex]::Replace($json, '("id":"' + [regex]::Escape($expiringRequest.id) + '".*?"expires_at":")[^"]+"', '${1}' + $past + '"')
    Check 'the expiring request was moved into the past' ($edited -ne $json)
    Write-UTF8 $database $edited
    $serve = Start-Board
    # A board that restarted gives its page a new token.
    $loopback = @{ Origin = $board; 'X-CFO-Token' = (Snapshot).instance }
    Check 'a save for an expired request is refused (409)' ((Probe '/api/credentials/save' (Save-Body $expiringRequest $expiring $refusalCanary) $loopback) -eq 409)
    Start-Sleep -Seconds 4
    Check 'the expired request closed as expired' ((Request-For $expiring).state -eq 'expired')

    # The real save, through the card, in a headless browser.
    $canaryFile = [IO.Path]::GetTempFileName()
    Write-UTF8 $canaryFile $canary
    $driver = & node (Join-Path $repository 'tests\acceptance\credential_card.mjs') --url $board --request $request.id --name $name --canary-file $canaryFile | Out-String
    Remove-Item -LiteralPath $canaryFile -Force -ErrorAction SilentlyContinue
    $card = $driver | ConvertFrom-Json
    Check 'the card saved the canary' ([bool]$card.saved)
    Check 'the browser profile holds no canary' ($card.profile_hits -eq 0)
    Check 'a replay of the save is refused (409)' ((Probe '/api/credentials/save' $refused $loopback) -eq 409)

    $deadline = (Get-Date).AddSeconds(90)
    while (-not ((Test-Path $standinTranscript) -and (Get-Content -LiteralPath $standinTranscript -Raw) -match 'matches the expected SHA-256') -and (Get-Date) -lt $deadline) { Start-Sleep -Seconds 1 }
    $told = if (Test-Path $standinTranscript) { Get-Content -LiteralPath $standinTranscript -Raw } else { '' }
    Check 'the stand-in goblin received its re-source notice' ($told -match 'told: .*credentials refreshed: re-source .+auth\.ps1')
    if (-not $results['the stand-in goblin received its re-source notice']) { Write-Host ('the stand-in goblin was told: ' + ((($told -split "`n") | Where-Object { $_ -like 'told:*' }) -join ' | ')) }
    Check 'the stand-in goblin re-sourced the exact value' ($told -match "re-sourced auth\.ps1: $name set True, matches the expected SHA-256 True")
    $listing = Checked $cfo @('auth','list','--project',$scope)
    Check 'cfo auth list shows the name in Windows Credential Manager' ($listing -match [regex]::Escape("$scope/$name") -and $listing -match 'Windows Credential Manager')
    $saved = Request-For $name
    Check 'the request closed as saved and recorded the goblin it told' ($saved.state -eq 'saved' -and @($saved.told) -contains $task)

    # Where the value must never be.
    $snapshotText = (Invoke-WebRequest -UseBasicParsing -Uri "$board/api/snapshot").Content
    $stream = Join-Path $root 'stream.txt'
    & curl.exe -s -N --max-time 4 -o $stream "$board/api/events" | Out-Null
    $script = Join-Path $taskTmp 'auth.ps1'
    foreach ($value in @(@{ Label = 'canary'; Text = $canary }, @{ Label = 'refused canary'; Text = $refusalCanary })) {
        $inState = Hits @($state) $value.Text
        $elsewhere = @($inState | Where-Object { $_ -ne $script })
        Write-Host ("{0}: state/ {1} hit(s) outside the goblin's auth.ps1" -f $value.Label, $elsewhere.Count)
        Check "no $($value.Label) in state/ outside the goblin's auth.ps1" ($elsewhere.Count -eq 0)
        $other = Hits @($data, $logs, $project, $standinTranscript, $stream) $value.Text
        Write-Host ("{0}: data/, logs, the project, the stand-in's transcript and the event stream: {1} hit(s)" -f $value.Label, $other.Count)
        Check "no $($value.Label) in data/, logs, the project, the stand-in's transcript or the event stream" ($other.Count -eq 0)
        Check "no $($value.Label) in the board snapshot" (-not $snapshotText.Contains($value.Text))
        $transcripts = Hits $Transcript $value.Text
        Write-Host ("{0}: agent session transcripts ({1} searched): {2} hit(s)" -f $value.Label, $Transcript.Count, $transcripts.Count)
        Check "no $($value.Label) in the agent session transcripts" ($transcripts.Count -eq 0)
    }
    $delivered = @(Hits @($state) $canary | Where-Object { $_ -eq $script }).Count
    Write-Host "canary: the stand-in goblin's auth.ps1, its one expected delivery: $delivered hit(s)"
    Check "the canary is in the stand-in goblin's auth.ps1 exactly once, owner-only" ($delivered -eq 1 -and (Owner-Only $script))
    Check 'the refused canary never reached the goblin' (-not ((Get-Content -LiteralPath $script -Raw).Contains($refusalCanary)))

    # Cleanup removes the goblin's auth.ps1 with the task, once the goblin's
    # terminal has ended.
    Stop-Process -Id $hostProcess.Id -Force -ErrorAction SilentlyContinue
    $hostProcess.WaitForExit(15000) | Out-Null
    $cleanup = Native $cfo @('cleanup', $task)
    Check 'cfo cleanup took the stand-in goblin''s task' ($LASTEXITCODE -eq 0)
    if ($LASTEXITCODE -ne 0) { Write-Host "cfo cleanup said: $($cleanup.Trim())" }
    $afterCleanup = Hits @($state) $canary
    Write-Host "canary: state/ after cfo cleanup: $($afterCleanup.Count) hit(s)"
    Check 'cfo cleanup removed the auth.ps1, and state/ holds no canary' ($afterCleanup.Count -eq 0 -and -not (Test-Path -LiteralPath $script))
} catch {
    # A step that could not run is a failed check, so the summary still says
    # what held before it.
    Check ('the proof ran to its end: ' + $_.Exception.Message.Split("`n")[0]) $false
} finally {
    # Cleanup runs whatever failed above, so nothing in it may stop it.
    $ErrorActionPreference = 'Continue'
    if ($canaryFile) { Remove-Item -LiteralPath $canaryFile -Force -ErrorAction SilentlyContinue }
    if ($hostProcess -and -not $hostProcess.HasExited) { Stop-Process -Id $hostProcess.Id -Force -ErrorAction SilentlyContinue }
    if ($serve -and -not $serve.HasExited) { Stop-Process -Id $serve.Id -Force -ErrorAction SilentlyContinue }
    # A credential the proof never stored is gone already: CredDeleteW then
    # fails with ERROR_NOT_FOUND.
    $deleted = [CanaryProof.Vault]::CredDeleteW("cfo:$scope/$name", 1, 0)
    $neverStored = -not $deleted -and [Runtime.InteropServices.Marshal]::GetLastWin32Error() -eq 1168
    $gone = ($deleted -or $neverStored) -and (Native $cfo @('auth', 'list', '--project', $scope)) -match 'no credentials stored'
    Write-Host ("the throwaway credential cfo:$scope/$name is gone from Windows Credential Manager: $gone" + $(if ($deleted) { ' (deleted)' } elseif ($neverStored) { ' (it was never stored)' } else { '' }))
    $results['the throwaway credential is gone from Windows Credential Manager'] = $gone
    if (-not $gone) { $failures.Add('the throwaway credential is gone from Windows Credential Manager') }
    Start-Sleep -Seconds 1
    Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue
}
foreach ($result in $results.GetEnumerator()) { Write-Host ("{0}  {1}" -f ($(if ($result.Value) { 'PASS' } else { 'FAIL' })), $result.Key) }
if ($failures.Count) { Write-Host "$($failures.Count) check(s) failed"; exit 1 }
Write-Host 'every check passed'
exit 0
