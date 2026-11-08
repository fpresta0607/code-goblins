# Proves the install workflow's dictation check on a hosted runner, where the
# 482 MB speech model download is the runner's to make and not the product's.
# It reads goblins doctor, and when dictation is not set up it runs
# `cfo dictation setup` once. A runner that cannot download the model is an
# environment result, never a failure of the change, in the shape
# tests/acceptance/no_visible_windows.ps1 gives its own premise. A real
# product failure still fails: dictation ready with the wrong number of
# verified parts in caches\voice, or a setup that fails on anything but a
# transport error. The step summary names which outcome happened.
#
#   .\tests\acceptance\dictation.ps1 -Goblins <goblins.exe> -Cfo <cfo.exe> -CfoHome <CFO_HOME> -Summary <GITHUB_STEP_SUMMARY>
#
# -SelfTest runs the verdict and the summary over results made up here, so a
# fixture proves each outcome without a runner.
param(
    [string]$Goblins = '',
    [string]$Cfo = '',
    [string]$CfoHome = '',
    [string]$Summary = '',
    [string]$ResultFile = '',
    [string]$SelfTest = ''
)
$ErrorActionPreference = 'Stop'

# Get-SetupReason is the one line of cfo dictation setup's output that says
# why it failed, its last non-empty line.
function Get-SetupReason([string]$Output) {
    $lines = @($Output -split "`r?`n" | Where-Object { $_.Trim() -ne '' })
    if ($lines.Count -eq 0) { return 'cfo dictation setup said nothing' }
    return $lines[-1].Trim()
}

# Get-VerifiedParts is the engine and model folders in caches\voice that hold
# a verified.json, which is what doctor counts as a part.
function Get-VerifiedParts([string]$VoiceDir) {
    if (-not (Test-Path -LiteralPath $VoiceDir)) { return @() }
    return @(Get-ChildItem -LiteralPath $VoiceDir -Directory | Where-Object { Test-Path -LiteralPath (Join-Path $_.FullName "verified.json") })
}

# Get-DictationVerdict reads one measured result and says what it means: the
# install set dictation up, the runner could not download it (an environment
# result), or a product failure. A missing part count is a failure when
# doctor is ready and when the setup said it succeeded. Only a download that
# could not be made is the environment's; any other setup failure, a checksum
# mismatch included, is the product's.
function Get-DictationVerdict {
    param([Parameter(Mandatory = $true)]$Result)
    $failures = @()
    $environment = @()
    $outcome = ''
    $headline = ''
    if ($Result.DoctorReady) {
        if ($Result.PartsCount -eq 2) {
            $outcome = 'ready'
            if ($Result.SetupRan) { $headline = 'The install set dictation up.' }
            else { $headline = 'Dictation was already set up.' }
        } else {
            $outcome = 'failure'
            $headline = 'Dictation failed.'
            $failures += "doctor reports dictation ready but caches\voice holds $($Result.PartsCount) verified parts, want the engine and the model: $($Result.PartsNames)"
        }
    } elseif (-not $Result.SetupRan) {
        $outcome = 'failure'
        $headline = 'Dictation failed.'
        $failures += "dictation is not set up and cfo dictation setup was not run: $($Result.DoctorLine)"
    } elseif ($Result.SetupExit -eq 0) {
        $outcome = 'failure'
        $headline = 'Dictation failed.'
        $failures += "cfo dictation setup ended 0 but doctor still does not report dictation ready: $($Result.DoctorLine)"
        if ($Result.PartsCount -ne 2) {
            $failures += "caches\voice holds $($Result.PartsCount) verified parts, want the engine and the model: $($Result.PartsNames)"
        }
    } elseif ($Result.SetupOutput -match 'could not be downloaded') {
        $outcome = 'environment'
        $headline = '**Environment result: the runner could not download dictation.**'
        $environment += "cfo dictation setup could not download the model: $(Get-SetupReason $Result.SetupOutput)"
    } else {
        $outcome = 'failure'
        $headline = 'Dictation failed.'
        $failures += "cfo dictation setup failed on something other than a download: $(Get-SetupReason $Result.SetupOutput)"
    }
    [pscustomobject]@{ Outcome = $outcome; Headline = $headline; Failures = $failures; Environment = $environment; Result = $Result }
}

# Write-DictationSummary appends the run's summary: which of the three
# outcomes happened, the doctor line, whether the setup ran, and, when the
# runner could not host the download, the environment result with its reason.
function Write-DictationSummary {
    param(
        [Parameter(Mandatory = $true)]$Verdict,
        [Parameter(Mandatory = $true)][string]$Path
    )
    $result = $Verdict.Result
    $lines = @('### Dictation', '', $Verdict.Headline, '')
    $lines += "- doctor: $($result.DoctorLine)"
    if ($result.SetupRan) {
        $lines += "- cfo dictation setup ran once and exited $($result.SetupExit)"
    } else {
        $lines += "- cfo dictation setup was not needed"
    }
    $lines += "- caches\voice holds $($result.PartsCount) verified parts: $($result.PartsNames)"
    if ($Verdict.Environment.Count -gt 0) {
        $lines += ''
        $lines += '**Environment result: this runner cannot host the download, so this is no verdict on the change.**'
        foreach ($line in $Verdict.Environment) { $lines += "- $line" }
    }
    if ($Verdict.Failures.Count -gt 0) {
        $lines += ''
        foreach ($line in $Verdict.Failures) { $lines += "- $line" }
    }
    $lines += ''
    $lines | Add-Content -Path $Path
}

# Measure-Dictation reads goblins doctor, and when dictation is not set up
# runs cfo dictation setup once and reads doctor again, so a setup that
# worked is asserted exactly as a home that already had the parts.
function Measure-Dictation {
    param(
        [Parameter(Mandatory = $true)][string]$Goblins,
        [Parameter(Mandatory = $true)][string]$Cfo,
        [Parameter(Mandatory = $true)][string]$CfoHome
    )
    $doctor = @(& $Goblins doctor)
    $global:LASTEXITCODE = 0
    $line = (@($doctor | Where-Object { $_ -match '^dictation:' }) -join ' ').Trim()
    $ready = $line -match '^dictation: ready: '
    $setupRan = $false
    $setupExit = 0
    $setupOutput = ''
    if (-not $ready) {
        $setupRan = $true
        # cfo dictation setup writes its progress to stdout and its failure
        # to stderr; both are read, and a failure is data here, not a
        # terminating error.
        $previous = $ErrorActionPreference
        if ($null -ne (Get-Variable -Name PSNativeCommandUseErrorActionPreference -ErrorAction SilentlyContinue)) {
            $PSNativeCommandUseErrorActionPreference = $false
        }
        $ErrorActionPreference = 'Continue'
        try {
            $setupOutput = (& $Cfo dictation setup 2>&1 | Out-String)
            $setupExit = $LASTEXITCODE
        } finally {
            $ErrorActionPreference = $previous
        }
        if ($setupExit -eq 0) {
            $doctor = @(& $Goblins doctor)
            $global:LASTEXITCODE = 0
            $line = (@($doctor | Where-Object { $_ -match '^dictation:' }) -join ' ').Trim()
            $ready = $line -match '^dictation: ready: '
        }
    }
    $parts = Get-VerifiedParts (Join-Path $CfoHome "caches\voice")
    [pscustomobject]@{
        DoctorLine   = $line
        DoctorReady  = $ready
        SetupRan     = $setupRan
        SetupExit    = $setupExit
        SetupOutput  = $setupOutput
        PartsCount   = $parts.Count
        PartsNames   = (@($parts | ForEach-Object { $_.Name }) -join ', ')
    }
}

# A self-test is the fixture the acceptance criteria ask for: results made up
# here, never measured, through the same verdict and summary the run uses.
if ($SelfTest -ne '') {
    function Assert-SelfTest([bool]$Condition, [string]$Message) {
        if (-not $Condition) { throw "dictation self-test ${SelfTest}: $Message" }
    }

    function Result([bool]$DoctorReady, [bool]$SetupRan, [int]$SetupExit, [string]$SetupOutput, [int]$PartsCount, [string]$PartsNames) {
        $doctorLine = 'dictation: not set up: parakeet-tdt-0.6b-v2 int8-2025-08-16 missing, 460 MB, from C:\home\caches\voice'
        if ($DoctorReady) {
            $doctorLine = 'dictation: ready: parakeet-tdt-0.6b-v2 int8-2025-08-16 on sherpa-onnx 1.11.0, in C:\home\caches\voice'
        }
        [pscustomobject]@{
            DoctorLine  = $doctorLine
            DoctorReady = $DoctorReady
            SetupRan    = $SetupRan
            SetupExit   = $SetupExit
            SetupOutput = $SetupOutput
            PartsCount  = $PartsCount
            PartsNames  = $PartsNames
        }
    }

    $downloaded = 'dictation: parakeet-tdt-0.6b-v2 int8-2025-08-16 could not be downloaded: Get "https://example.test/model.tar.bz2": dial tcp: connection reset. Dictation needs it once: connect to the internet and dictate again'
    $mismatch = 'dictation: the download of parakeet-tdt-0.6b-v2 int8-2025-08-16 does not match its checksum (its SHA-256 is abcdef, the setting names 012345), so nothing was kept'
    $parts = 'engine 1, model int8-2025-08-16'

    switch ($SelfTest) {
        'setup-succeeds' {
            $verdict = Get-DictationVerdict (Result $true $true 0 '' 2 $parts)
            Assert-SelfTest ($verdict.Outcome -eq 'ready') "want ready, got $($verdict.Outcome)"
            Assert-SelfTest ($verdict.Failures.Count -eq 0) "want no failure, got: $($verdict.Failures -join '; ')"
            Assert-SelfTest ($verdict.Environment.Count -eq 0) "want no environment result, got: $($verdict.Environment -join '; ')"
            Assert-SelfTest ($verdict.Headline -eq 'The install set dictation up.') "want the install-set-up headline, got '$($verdict.Headline)'"
        }
        'already-ready' {
            $verdict = Get-DictationVerdict (Result $true $false 0 '' 2 $parts)
            Assert-SelfTest ($verdict.Outcome -eq 'ready') "want ready, got $($verdict.Outcome)"
            Assert-SelfTest ($verdict.Failures.Count -eq 0) "want no failure, got: $($verdict.Failures -join '; ')"
            Assert-SelfTest ($verdict.Headline -eq 'Dictation was already set up.') "want the already-set-up headline, got '$($verdict.Headline)'"
        }
        'download-fails' {
            $verdict = Get-DictationVerdict (Result $false $true 1 $downloaded 0 '')
            Assert-SelfTest ($verdict.Outcome -eq 'environment') "want the environment outcome, got $($verdict.Outcome)"
            Assert-SelfTest ($verdict.Failures.Count -eq 0) "a download failure must not fail the job, but got: $($verdict.Failures -join '; ')"
            Assert-SelfTest ($verdict.Environment.Count -eq 1) "want one environment result, got: $($verdict.Environment -join '; ')"
            Assert-SelfTest ($verdict.Environment[0] -like '*could not be downloaded*') "the environment result must name the download: $($verdict.Environment[0])"
            Assert-SelfTest ($verdict.Environment[0] -like '*parakeet-tdt-0.6b-v2*') "the environment result must name the part: $($verdict.Environment[0])"
        }
        'checksum-mismatch' {
            $verdict = Get-DictationVerdict (Result $false $true 1 $mismatch 0 '')
            Assert-SelfTest ($verdict.Outcome -eq 'failure') "a checksum mismatch must fail, got $($verdict.Outcome)"
            Assert-SelfTest ($verdict.Environment.Count -eq 0) "a checksum mismatch is not the environment's: $($verdict.Environment -join '; ')"
            Assert-SelfTest ($verdict.Failures.Count -eq 1) "want one failure, got: $($verdict.Failures -join '; ')"
            Assert-SelfTest ($verdict.Failures[0] -like '*does not match its checksum*') "the failure must name the mismatch: $($verdict.Failures[0])"
            Assert-SelfTest ($verdict.Failures[0] -like '*parakeet-tdt-0.6b-v2*') "the failure must name the part: $($verdict.Failures[0])"
        }
        'ready-wrong-parts' {
            $verdict = Get-DictationVerdict (Result $true $false 0 '' 1 'engine 1')
            Assert-SelfTest ($verdict.Outcome -eq 'failure') "a wrong part count must fail, got $($verdict.Outcome)"
            Assert-SelfTest ($verdict.Failures.Count -eq 1) "want one failure, got: $($verdict.Failures -join '; ')"
            Assert-SelfTest ($verdict.Failures[0] -like '*holds 1 verified parts*') "the failure must name the count: $($verdict.Failures[0])"
        }
        'setup-exit-zero-but-not-ready' {
            $verdict = Get-DictationVerdict (Result $false $true 0 '' 1 'engine 1')
            Assert-SelfTest ($verdict.Outcome -eq 'failure') "a setup that ended 0 without a ready doctor must fail, got $($verdict.Outcome)"
            Assert-SelfTest ($verdict.Failures.Count -eq 2) "want the doctor and the part count named, got: $($verdict.Failures -join '; ')"
        }
        'summary-distinguishes' {
            $ready = Get-DictationVerdict (Result $true $true 0 '' 2 $parts)
            $environment = Get-DictationVerdict (Result $false $true 1 $downloaded 0 '')
            $failure = Get-DictationVerdict (Result $false $true 1 $mismatch 0 '')
            $path = Join-Path ([System.IO.Path]::GetTempPath()) "dictation-$([guid]::NewGuid()).md"
            try {
                Write-DictationSummary -Verdict $ready -Path $path
                Write-DictationSummary -Verdict $environment -Path $path
                Write-DictationSummary -Verdict $failure -Path $path
                $summary = Get-Content -Raw -Path $path
                Assert-SelfTest ($summary.Contains('The install set dictation up.')) 'the summary must say the install set dictation up'
                Assert-SelfTest ($summary.Contains('Environment result')) 'the summary must carry the environment heading'
                Assert-SelfTest ($summary.Contains('could not be downloaded')) 'the summary must carry the environment reason'
                Assert-SelfTest ($summary.Contains('Dictation failed.')) 'the summary must carry the failure heading'
                Assert-SelfTest ($summary.Contains('does not match its checksum')) 'the summary must carry the failure reason'
                Assert-SelfTest ($summary.Contains('caches\voice holds')) 'the summary must carry the part count'
            } finally {
                Remove-Item -Force -ErrorAction SilentlyContinue $path
            }
        }
        default { throw "Unknown dictation self-test $SelfTest." }
    }
    Write-Output "dictation self-test $SelfTest passed."
    exit 0
}

foreach ($needed in @(@{ name = 'Goblins'; value = $Goblins }, @{ name = 'Cfo'; value = $Cfo }, @{ name = 'CfoHome'; value = $CfoHome })) {
    if (-not $needed.value) { throw "dictation.ps1 needs -$($needed.name)." }
}

$result = Measure-Dictation -Goblins $Goblins -Cfo $Cfo -CfoHome $CfoHome
$verdict = Get-DictationVerdict $result
Write-Host "$($verdict.Headline) $($result.DoctorLine)"
if ($Summary) { Write-DictationSummary -Verdict $verdict -Path $Summary }
if ($ResultFile) { [IO.File]::WriteAllText($ResultFile, ($verdict | ConvertTo-Json -Depth 6)) }
if ($env:GITHUB_ENV) { "INSTALL_DICTATION=$($verdict.Outcome)" | Add-Content -Path $env:GITHUB_ENV }
# An environment result is loud and green: the summary names it, and a
# warning annotation carries the same words onto the run. A failure fails.
foreach ($line in $verdict.Environment) {
    Write-Output "::warning title=Environment result::$line"
}
if ($verdict.Failures.Count -gt 0) {
    $verdict.Failures | ForEach-Object { Write-Error $_ -ErrorAction Continue }
    exit 1
}
if ($verdict.Environment.Count -gt 0) {
    Write-Output "dictation: this runner could not download the model, so readiness is an environment result, not a failure of the change."
}
exit 0
