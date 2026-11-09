[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$Binary, [Parameter(Mandatory=$true)][string]$HarnessBinary)
$ErrorActionPreference = 'Stop'
if ($env:CEO_BOARD_REAL -ne '1') { throw 'Set CEO_BOARD_REAL=1 to create the isolated native board fixture.' }
Remove-Item Env:CEO_HOME,Env:CEO_STATE_OVERRIDE -ErrorAction SilentlyContinue
$root = Join-Path ([IO.Path]::GetTempPath()) ('ceo-board-windows-' + [guid]::NewGuid().ToString('N'))
$fixtureHome = Join-Path $root 'home'
$project = Join-Path $root 'project'
$session = 'ceo-board-test-' + [guid]::NewGuid().ToString('N').Substring(0,12)
foreach ($dir in @($root, "$root\bin", $fixtureHome, "$fixtureHome\state", "$fixtureHome\data", $project)) { New-Item -ItemType Directory -Path $dir | Out-Null }
$env:CEO_HOME = $fixtureHome
$env:CEO_STATE_OVERRIDE = "$fixtureHome\state"
$env:CEO_PROJECTS_ROOT = $root
$env:HERDR_SESSION = $session
$env:HERDR_CONFIG_PATH = Join-Path $root 'herdr.toml'
Remove-Item Env:CEO_TASK_ID,Env:CEO_SPAWN_GEN,Env:CEO_PARENT_SESSION_ID,Env:CEO_PARENT_HARNESS,Env:CEO_ROOT_SESSION_ID -ErrorAction SilentlyContinue
$env:CEO_ROLE = 'ceo'
function Write-UTF8([string]$Path, [string]$Content) { [IO.File]::WriteAllText($Path, $Content, [Text.UTF8Encoding]::new($false)) }
function Checked([string]$Command, [string[]]$Arguments) {
    $output = & $Command @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Command failed: $LASTEXITCODE" }
    return $output
}
Write-UTF8 $env:HERDR_CONFIG_PATH ((Checked 'herdr' @('--default-config')) -join "`n")
$ceo = (Resolve-Path -LiteralPath $Binary).Path
$harness = (Resolve-Path -LiteralPath $HarnessBinary).Path
# Build, validate and scan both supplied executables before this opt-in test.
# A second path or copy is not security clearance for a flagged artifact.
Checked 'git' @('-C',$project,'init','-q','--initial-branch=main') | Out-Null
Checked 'git' @('-C',$project,'config','user.name','Local acceptance fixture') | Out-Null
Checked 'git' @('-C',$project,'config','user.email','fixture@example.invalid') | Out-Null
Write-UTF8 "$project\supervisor.ts" "export function summarize(state: string): string {`n  return state;`n}`n"
Checked 'git' @('-C',$project,'add','.') | Out-Null
Checked 'git' @('-C',$project,'commit','-qm','Initialize fixture supervisor') | Out-Null
Checked 'git' @('-C',$project,'update-ref','refs/remotes/origin/main','HEAD') | Out-Null
Checked 'git' @('-C',$project,'symbolic-ref','refs/remotes/origin/HEAD','refs/remotes/origin/main') | Out-Null
Checked 'git' @('-C',$project,'switch','-qc','fixture/native-board') | Out-Null
Write-UTF8 "$project\supervisor.ts" "export function summarize(state: string): string {`n  const phase = state.trim();`n  return phase || 'Evidence unavailable';`n}`n"
Write-UTF8 "$project\board.css" ":root {`n  --accent: #00e59b;`n  --surface: #03050a;`n}`n"
Checked 'git' @('-C',$project,'add','.') | Out-Null
Checked 'git' @('-C',$project,'commit','-qm','Show evidence freshness in the native board') | Out-Null
foreach ($kind in @('codex','claude','pi')) {
    Checked $ceo @('hooks','install',$kind,'--config-dir',"$root\$kind") | Out-Host
    Checked $ceo @('hooks','install',$kind,'--config-dir',"$root\$kind") | Out-Host
}
$server = Start-Process -FilePath 'herdr' -ArgumentList @('--session',$session,'server') -WindowStyle Hidden -RedirectStandardOutput "$root\herdr.out" -RedirectStandardError "$root\herdr.err" -PassThru
$deadline = (Get-Date).AddSeconds(15)
do {
    Start-Sleep -Milliseconds 150
    $status = & herdr --session $session status server --json 2>$null | ConvertFrom-Json
} until ($status.result.running -or (Get-Date) -gt $deadline)
$workspace = (Checked 'herdr' @('--session',$session,'workspace','create','--cwd',$project,'--label','ceo','--no-focus') | ConvertFrom-Json).result
$tab = (Checked 'herdr' @('--session',$session,'tab','create','--workspace',$workspace.workspace.workspace_id,'--cwd',$project,'--label','gb-board-fixture','--no-focus') | ConvertFrom-Json).result
$pane = $tab.root_pane.pane_id
if (!$pane) { throw 'Herdr did not return a fixture pane.' }
Write-UTF8 "$fixtureHome\state\board-fixture.meta" (@(
    "worktree=$project", "project=$project", 'harness=codex', 'kind=ship', 'mode=local-only', 'yolo=no',
    'spawn_gen=fixture-1', 'model=example (no model calls)', 'effort=max', 'backend=herdr',
    "herdr_session=$session", "herdr_workspace_id=$($workspace.workspace.workspace_id)", "herdr_tab_id=$($tab.tab.tab_id)", "herdr_pane_id=$pane"
) -join "`n")
Write-UTF8 "$fixtureHome\state\board-fixture.status" ((Get-Date).ToUniversalTime().ToString('o') + ' working: Build review panel' + "`n")
Write-UTF8 "$fixtureHome\data\backlog.md" "## Queued`n- [ ] board-fixture - Build review panel (repo: example)`n- [ ] follow-up - Polish settings (repo: example)`n"
$config = @{ root=$root; home=$fixtureHome; project=$project; session=$session; pane=$pane; hook="$root\codex\ceo-native-hook.ps1"; binary=$ceo; herdr_pid=$server.Id; parentHarness='codex' }
Write-UTF8 "$root\fixture.json" ($config | ConvertTo-Json)
$ceoTab = (Checked 'herdr' @('--session',$session,'tab','create','--workspace',$workspace.workspace.workspace_id,'--cwd',$project,'--label','example-ceo','--no-focus') | ConvertFrom-Json).result
$ceoConfig = $config.Clone()
$ceoConfig.pane = $ceoTab.root_pane.pane_id
$ceoConfig.role = 'ceo'
$ceoConfig.nativeID = 'board-ceo'
Write-UTF8 "$root\ceo.json" ($ceoConfig | ConvertTo-Json)
$ceoLine = "Clear-Host; & '" + $harness.Replace("'","''") + "' '" + "$root\ceo.json".Replace("'","''") + "'"
Checked 'herdr' @('--session',$session,'pane','run',$ceoConfig.pane,$ceoLine) | Out-Null
$deadline = (Get-Date).AddSeconds(20)
while (!(Test-Path "$root\ceo-harness.pid") -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 100 }
if (!(Test-Path "$root\ceo-harness.pid")) { throw "Example CEO did not start. Inspect $root" }
$ceoProcess = Get-Process -Id ([int](Get-Content "$root\ceo-harness.pid"))
# The example CEO registers itself from its own SessionStart hook, exactly as
# a real primary CEO does; nothing here writes primary.json for it.
$deadline = (Get-Date).AddSeconds(20)
while (!(Test-Path "$fixtureHome\state\primary.json") -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 100 }
$registered = Get-Content -LiteralPath "$fixtureHome\state\primary.json" -Raw -ErrorAction SilentlyContinue | ConvertFrom-Json
if (!$registered -or $registered.process.pid -ne $ceoProcess.Id -or $registered.target.Pane -ne $ceoConfig.pane) { throw "The example CEO did not register itself. Inspect $root" }
$line = "Clear-Host; & '" + $harness.Replace("'","''") + "' '" + "$root\fixture.json".Replace("'","''") + "'"
Checked 'herdr' @('--session',$session,'pane','run',$pane,$line) | Out-Null
$deadline = (Get-Date).AddSeconds(15)
while (!(Test-Path "$root\harness.pid") -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 100 }
if (!(Test-Path "$root\harness.pid")) { throw "Fixture harness did not start. Inspect $root" }
$board = Start-Process -FilePath $ceo -ArgumentList @('serve','--example','--listen','127.0.0.1:0') -WindowStyle Hidden -RedirectStandardOutput "$root\board.out" -RedirectStandardError "$root\board.err" -PassThru
$deadline = (Get-Date).AddSeconds(15)
do {
    Start-Sleep -Milliseconds 100
    $log = Get-Content -LiteralPath "$root\board.out" -Raw
} until ($log -match 'http://127.0.0.1:\d+' -or (Get-Date) -gt $deadline)
if ($log -notmatch 'http://127.0.0.1:\d+') { throw "Board did not start. Inspect $root" }
$config.url = $Matches[0]
$config.board_pid = $board.Id
$config.harness_pid = [int](Get-Content "$root\harness.pid")
$config.ceo_harness_pid = $ceoProcess.Id
$config.ceo_pane = $ceoConfig.pane
Write-UTF8 "$root\fixture.json" ($config | ConvertTo-Json)
Write-Output ($config | ConvertTo-Json)
Write-Output 'Fixture remains running for browser/restart/crash verification. Stop only the PIDs and named test session recorded above.'
