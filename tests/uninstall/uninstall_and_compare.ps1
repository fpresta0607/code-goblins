# uninstall_and_compare.ps1 - uninstall Code Goblins on a clean runner as a
# person would, then compare the profile with the snapshot taken before the
# install. The only differences allowed are what the uninstall's own plan
# named as kept, at the person's choice: the folder it keeps their work and
# fleet data in, and the tools it leaves.
#
#   .\tests\uninstall\uninstall_and_compare.ps1 -Leftovers <leftovers.exe> -Project <checkout> [-RemoveAll] [-FromInstalledApps]
#
# -RemoveAll chooses to remove the work, the fleet data and the tools too, so
# nothing at all may differ. -FromInstalledApps runs the uninstall Windows'
# Installed apps runs, from the entry the install made.
param(
    [Parameter(Mandatory = $true)][string]$Leftovers,
    [Parameter(Mandatory = $true)][string]$Project,
    [switch]$RemoveAll,
    [switch]$FromInstalledApps
)
$ErrorActionPreference = "Stop"
$cfoHome = Join-Path $env:LOCALAPPDATA "CodeGoblins"
$goblins = Join-Path $cfoHome "bin\goblins.exe"
# From a neutral folder, in a new terminal's environment, as a person would.
Set-Location $env:RUNNER_TEMP
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")
$env:CFO_HOME = [Environment]::GetEnvironmentVariable("CFO_HOME", "User")

# The plan first, which takes nothing away: no thing there before it may be
# gone after it. The board, which runs on meanwhile, keeps writing its own
# state, so what was added or changed proves nothing here.
& $Leftovers snapshot (Join-Path $env:RUNNER_TEMP "leftovers-before-plan.json") --content (Join-Path $Project ".git") | Out-Null
$planText = & { $ErrorActionPreference = "Continue"; & $goblins uninstall --dry-run --json }
if ($LASTEXITCODE -ne 0) { throw "goblins uninstall --dry-run exited with code $LASTEXITCODE" }
$planFile = Join-Path $env:RUNNER_TEMP "leftovers-plan.json"
[IO.File]::WriteAllText($planFile, ($planText -join "`n"))
& $goblins uninstall --dry-run | Write-Host
& $Leftovers snapshot (Join-Path $env:RUNNER_TEMP "leftovers-after-plan.json") --content (Join-Path $Project ".git") | Out-Null
& $Leftovers compare (Join-Path $env:RUNNER_TEMP "leftovers-before-plan.json") (Join-Path $env:RUNNER_TEMP "leftovers-after-plan.json") --report (Join-Path $env:RUNNER_TEMP "leftovers-plan-report.json") | Out-Null
$taken = @((Get-Content -Raw -LiteralPath (Join-Path $env:RUNNER_TEMP "leftovers-plan-report.json") | ConvertFrom-Json).remaining | Where-Object { $_.change -eq "gone" -and $_.kind -ne "process" -and -not $_.identity.StartsWith($cfoHome, [StringComparison]::OrdinalIgnoreCase) })
if ($taken.Count -gt 0) { throw "the dry run took away: $(($taken | ForEach-Object { "$($_.kind) $($_.identity)" }) -join '; ')" }

$plan = $planText -join "`n" | ConvertFrom-Json
if (-not @($plan.items | Where-Object { $_.action -eq "stop" -and $_.what -eq "demo-goblin's terminal" })) {
    throw "the plan stops no goblin terminal, so the uninstall met no running goblin: $($plan.items.what -join '; ')"
}

$arguments = @("uninstall", "--yes")
if ($RemoveAll) { $arguments += @("--remove-work", "--remove-tools") }
if ($FromInstalledApps) {
    $entry = Get-ItemProperty -LiteralPath "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\CodeGoblins"
    if (-not $entry.QuietUninstallString -or -not $entry.UninstallString) { throw "the install made no entry in Installed apps" }
    Write-Host "Installed apps runs: $($entry.QuietUninstallString)"
    $run = Start-Process -FilePath (Join-Path $env:SystemRoot "System32\cmd.exe") -ArgumentList ('/d /s /c "' + $entry.QuietUninstallString + '"') -NoNewWindow -Wait -PassThru
    $global:LASTEXITCODE = $run.ExitCode
}
else {
    & { $ErrorActionPreference = "Continue"; & $goblins @arguments }
}
if ($LASTEXITCODE -ne 0) { throw "goblins uninstall exited with code $LASTEXITCODE" }

# What a person sees at once.
if (Test-Path -LiteralPath $cfoHome) { throw "the uninstall left $cfoHome" }
if ([Environment]::GetEnvironmentVariable("CFO_HOME", "User")) { throw "CFO_HOME survived the uninstall" }
if (Test-Path -LiteralPath "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\CodeGoblins") { throw "Code Goblins is still in Installed apps" }
$kept = @()
if ($plan.kept -and @($plan.items | Where-Object { $_.action -eq "keep" })) {
    if ($RemoveAll) { throw "the plan kept work although the person chose to remove it" }
    $kept += $plan.kept
    $note = Get-ChildItem -LiteralPath (Join-Path $plan.kept "work") -Recurse -Filter README.txt | Select-Object -First 1
    if (-not $note) { throw "the kept folder $($plan.kept) holds no goblin's work" }
    & git -C $Project bundle verify --quiet (Join-Path $note.DirectoryName "commits.bundle")
    if ($LASTEXITCODE -ne 0) { throw "the kept commits do not verify" }
    if (-not (Test-Path -LiteralPath (Join-Path $note.DirectoryName "files\uncommitted.txt"))) { throw "the kept work lacks the file the goblin had not committed" }
    if (-not (Test-Path -LiteralPath (Join-Path $plan.kept "fleet\data"))) { throw "the kept folder holds no fleet data" }
}
$kept += @($plan.items | Where-Object { $_.action -eq "leave" } | ForEach-Object { $_.where })
if ($RemoveAll -and @($plan.items | Where-Object { $_.action -eq "leave" -and $_.group -eq "tools" })) { throw "the plan left tools although the person chose to remove them" }

# The uninstall's own program goes from the folder it moved to once it ends,
# within seconds, as it does on a person's PC.
$deadline = (Get-Date).AddSeconds(30)
while (@(Get-ChildItem -LiteralPath $env:LOCALAPPDATA -Filter ".CodeGoblins-uninstall-*" -Force -ErrorAction SilentlyContinue).Count -gt 0 -and (Get-Date) -lt $deadline) {
    Start-Sleep -Milliseconds 500
}

# Then everything else: the profile as it was before the install, apart
# from what the plan named as kept.
& $Leftovers snapshot (Join-Path $env:RUNNER_TEMP "leftovers-after.json") --content (Join-Path $Project ".git")
if ($LASTEXITCODE -ne 0) { throw "the snapshot after the uninstall failed" }
$compare = @("compare", (Join-Path $env:RUNNER_TEMP "leftovers-before.json"), (Join-Path $env:RUNNER_TEMP "leftovers-after.json"), "--report", (Join-Path $env:RUNNER_TEMP "leftovers-report.json"))
foreach ($path in $kept) { $compare += @("--kept", $path) }
& $Leftovers @compare
exit $LASTEXITCODE
