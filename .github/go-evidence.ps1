param(
    [Parameter(Mandatory)][string]$Events,
    [Parameter(Mandatory)][string]$Output,
    [Parameter(Mandatory)][string]$Toolchain,
    [Parameter(Mandatory)][string]$AllPackages,
    [Parameter(Mandatory)][string[]]$Packages,
    [Parameter(Mandatory)][string]$Shard,
    [Parameter(Mandatory)][string]$Head,
    [string]$RunTests = '',
    [string]$SkipTests = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Read-Git([string[]]$Arguments) {
    $gitOutput = @(& git @Arguments)
    if ($LASTEXITCODE -ne 0) { throw 'Cannot establish the actual checkout inputs.' }
    return ($gitOutput -join "`n").Trim()
}

foreach ($variable in 'CODEX_THREAD_ID', 'CFO_HOME', 'CFO_STATE_OVERRIDE', 'NO_MISTAKES_GATE') {
    if ([Environment]::GetEnvironmentVariable($variable)) { throw "The test environment still carries $variable." }
}
if (-not $env:GITHUB_REPOSITORY -or [long]$env:GITHUB_RUN_ID -le 0 -or [int]$env:GITHUB_RUN_ATTEMPT -le 0) {
    throw 'Missing workflow run identity.'
}
$checkout = Read-Git -Arguments @('rev-parse', 'HEAD')
$mergeParents = (Read-Git -Arguments @('show', '-s', '--format=%P', 'HEAD')).Split(' ')
if ($mergeParents.Count -ne 2 -or $mergeParents[1] -cne $Head) { throw 'The checkout is not the named head tested against main.' }
if (Read-Git -Arguments @('status', '--porcelain', '--untracked-files=all')) { throw 'The tested checkout is not clean.' }
$inputs = [ordered]@{}
foreach ($name in 'go.mod', 'go.sum', 'config/verify.json', '.no-mistakes.yaml', '.github/workflows/go.yml', '.github/go-evidence.ps1') {
    $inputs[$name] = Read-Git -Arguments @('rev-parse', "HEAD:$name")
}
$goEnvironment = Get-Content -LiteralPath $Toolchain -Raw | ConvertFrom-Json
$environmentNames = @('CGO_ENABLED', 'GOAMD64', 'GOARCH', 'GOARM64', 'GOEXPERIMENT', 'GOFLAGS', 'GOOS', 'GOVERSION', 'GOWORK')
$actualNames = @($goEnvironment.PSObject.Properties.Name | Sort-Object)
if (($actualNames -join ',') -cne ($environmentNames -join ',')) { throw 'The Go build environment is incomplete.' }
$fingerprint = ''
foreach ($name in $environmentNames) {
    if ($goEnvironment.$name -isnot [string]) { throw 'A Go build setting is unreadable.' }
    $fingerprint += $name + [char]0 + $goEnvironment.$name + [char]0
}
$hasher = [Security.Cryptography.SHA256]::Create()
try { $environmentHash = -join ($hasher.ComputeHash([Text.Encoding]::UTF8.GetBytes($fingerprint)) | ForEach-Object { $_.ToString('x2') }) }
finally { $hasher.Dispose() }
[string[]]$all = Get-Content -LiteralPath $AllPackages -Raw | ConvertFrom-Json
if ($all.Count -eq 0 -or @($all | Select-Object -Unique).Count -ne $all.Count -or @($Packages | Select-Object -Unique).Count -ne $Packages.Count) {
    throw 'The package scope is empty or ambiguous.'
}
foreach ($packageName in $Packages) {
    if ($all -cnotcontains $packageName) { throw 'A requested package is outside the recorded module.' }
}
$results = @{}
$skipped = @{}
Get-Content -LiteralPath $Events | ForEach-Object {
    $testEvent = $_ | ConvertFrom-Json
    $packageProperty = $testEvent.PSObject.Properties['Package']
    if ($null -eq $packageProperty) { return }
    $packageName = [string]$testEvent.Package
    $testProperty = $testEvent.PSObject.Properties['Test']
    if ($null -ne $testProperty -and $testEvent.Test) {
        if ($testEvent.Action -eq 'skip') {
            if ($Packages -cnotcontains $packageName) { throw 'A skipped test belongs to an unexpected package.' }
            $skipped[$packageName] = @($skipped[$packageName]) + [string]$testEvent.Test
        }
        if ($testEvent.Action -eq 'fail') { throw 'A test did not pass.' }
        return
    }
    if ($testEvent.Action -notin @('pass', 'fail', 'skip')) { return }
    if ($Packages -cnotcontains $packageName -or $results.ContainsKey($packageName)) { throw 'The package results are unexpected or repeated.' }
    if ($testEvent.Action -eq 'fail') { throw 'A package did not pass.' }
    $elapsed = 0.0
    if ($null -ne $testEvent.PSObject.Properties['Elapsed']) { $elapsed = [double]$testEvent.Elapsed }
    $results[$packageName] = [ordered]@{
        package = $packageName
        status = $(if ($testEvent.Action -eq 'pass') { 'passed' } else { 'no_tests' })
        seconds = $elapsed
    }
}
if ($results.Count -ne $Packages.Count) { throw 'At least one requested package did not finish.' }
$recorded = @($Packages | Sort-Object | ForEach-Object {
    $result = $results[$_]
    $result['skipped'] = @($skipped[$_] | Where-Object { $null -ne $_ } | Sort-Object -Unique)
    $result
})
$proof = [ordered]@{
    version = 1
    repository = $env:GITHUB_REPOSITORY
    run_id = [long]$env:GITHUB_RUN_ID
    attempt = [int]$env:GITHUB_RUN_ATTEMPT
    shard = $Shard
    head = $Head
    main = $mergeParents[0]
    checkout = $checkout
    tree = Read-Git -Arguments @('rev-parse', 'HEAD^{tree}')
    inputs = $inputs
    toolchain = "$($goEnvironment.GOVERSION) $($goEnvironment.GOOS)/$($goEnvironment.GOARCH)"
    environment = $environmentHash
    cgo = $goEnvironment.CGO_ENABLED
    run_tests = $RunTests
    skip_tests = $SkipTests
    vet = ($Shard -ceq 'rest')
    all_packages = @($all | Sort-Object)
    results = $recorded
}
$json = ($proof | ConvertTo-Json -Depth 6) + [Environment]::NewLine
[IO.File]::WriteAllText($Output, $json, [Text.UTF8Encoding]::new($false))
