# Reads the install's own word about what it could not install, and tells that
# apart from what goblins doctor reports: the log's closing note, "These could
# not be installed, so only what needs them is left out: <tools>. The log says
# why.", names every tool whose fetch failed, with the log's line that says
# why. A MISSING or broken tool in that note is an environment result on a
# runner that could not reach its release, never a failure of the change; one
# the note did not name is the install's failure. The install workflow's home
# checks dot-source this file for that rule, and the one-line job's skill checks
# read the same note, so a skill the install named is the same result.
#
#   . .\tests\acceptance\install_left_out.ps1
#   .\tests\acceptance\install_left_out.ps1 -SelfTest <name>
#
# -SelfTest runs the verdict, the note and its reason, and the summary over
# logs and doctor lines made up here, so a fixture proves each outcome without
# a runner or a failed install.
param(
    [string]$SelfTest = ''
)

# Get-LeftOutTools is the install's closing note as a list: the tools it named
# as ones it could not install.
function Get-LeftOutTools([string]$Text) {
    $note = [regex]::Match($Text, '(?m)^Note: These could not be installed, so only what needs them is left out: (.+?)\. The log says why\.\s*$')
    if (-not $note.Success) { return @() }
    return @($note.Groups[1].Value -split ',' | ForEach-Object { $_.Trim() } | Where-Object { $_ })
}

# Get-LeftOutReason answers for $Name only when the note names it, and then
# with the log's own last word about it, which the note's "The log says why"
# points to: the WARN line it wrote for the tool, or the PREREQ line that said
# what the tool needs. A skill is named "<skill> skill" in the note and
# "<skill>" in the log's lines. A note that names it with no such line is
# still the install's own word for it.
function Get-LeftOutReason([string]$Text, [string]$Name) {
    if (@(Get-LeftOutTools $Text) -notcontains $Name) { return '' }
    $bare = $Name -replace '\s+skill$', ''
    $found = @([regex]::Matches($Text, '(?m)^(?:WARN|PREREQ)\s+' + [regex]::Escape($bare) + '\s+(.+?)\s*$'))
    if ($found.Count -eq 0) { return "the install named it in its closing note, which says the log holds why" }
    return $found[$found.Count - 1].Groups[1].Value.Trim()
}

# Get-DoctorVerdict splits doctor's MISSING and broken lines: the ones the
# install named as left out are environment results that carry the log's
# reason, and the ones it did not name are failures, as the line reads.
function Get-DoctorVerdict {
    param([string[]]$Doctor = @(), [string]$Log = '')
    $environment = @()
    $failures = @()
    foreach ($line in @($Doctor | Where-Object { $_ -match '^(MISSING|broken)\s' })) {
        $parts = @($line -split '\s+', 3)
        $name = $parts[1]
        $reason = Get-LeftOutReason -Text $Log -Name $name
        if (-not $reason) {
            $failures += $line
            continue
        }
        $environment += "the install could not install $name, so goblins doctor reports it $($parts[0]): $reason"
    }
    [pscustomobject]@{ Environment = $environment; Failures = $failures }
}

# Write-EnvironmentResult makes an environment result loud and green: a warning
# annotation carrying the tool and the log's reason, and the job summary under
# its own heading, so neither the run nor the record reads as a failure of the
# change. No line, no section.
function Write-EnvironmentResult {
    param([string[]]$Lines = @(), [string]$Summary = '')
    $lines = @($Lines | Where-Object { $_ })
    foreach ($line in $lines) { Write-Output "::warning title=Environment result::$line" }
    if ($lines.Count -eq 0 -or -not $Summary) { return }
    $section = @('### Install tools', '', '**Environment result: the install named these as ones it could not install, so this is no verdict on the change.**', '')
    foreach ($line in $lines) { $section += "- $line" }
    $section += ''
    Add-Content -Path $Summary -Value $section
}

# A self-test is the fixture the acceptance criteria ask for: a log and doctor
# lines made up here, through the same functions the run uses.
if ($SelfTest -ne '') {
    function Assert-SelfTest([bool]$Condition, [string]$Message) {
        if (-not $Condition) { throw "install left-out self-test ${SelfTest}: $Message" }
    }

    $note = 'Note: These could not be installed, so only what needs them is left out: chrome-devtools-axi, lavish-axi. The log says why.'
    $log = @(
        'install  chrome-devtools-axi    https://github.com/fpres/chrome-devtools-axi.tgz',
        'npm error code ECONNRESET',
        'WARN     chrome-devtools-axi    install failed: exited with code 1',
        'install  lavish-axi             https://github.com/fpres/lavish-axi.tgz',
        'WARN     lavish-axi             install failed: exited with code 1',
        'skill    gh-axi                 npx skills@1 add kunchenguid/gh-axi',
        'WARN     gh-axi                 skill install exited with code 1',
        $note
    ) -join "`n"
    $claudeLog = @(
        'install  claude                 https://claude.ai/install.ps1',
        'WARN     claude                 install failed: installer exited with code 1',
        'Note: These could not be installed, so only what needs them is left out: claude. The log says why.'
    ) -join "`n"
    $ghSkillLog = @(
        'skill    gh-axi                 npx skills@1 add kunchenguid/gh-axi',
        'WARN     gh-axi                 skill install exited with code 1',
        'Note: These could not be installed, so only what needs them is left out: gh-axi skill. The log says why.'
    ) -join "`n"
    $missingChrome = 'MISSING  chrome-devtools-axi not found on PATH (install: npm install -g https://github.com/fpres/chrome-devtools-axi.tgz)'
    $missingLavish = 'MISSING  lavish-axi not found on PATH (install: npm install -g https://github.com/fpres/lavish-axi.tgz)'
    $brokenClaude = 'broken   claude     harness spawn probe: claude --version exited with code 1'
    $doctorSaid = @('ok       git        2.56.0', 'crawler: ready: goblin-crawl')

    switch ($SelfTest) {
        'named-tool-is-environment' {
            $verdict = Get-DoctorVerdict -Doctor (@($doctorSaid) + $missingChrome) -Log $log
            Assert-SelfTest ($verdict.Environment.Count -eq 1) "want one environment result, got: $($verdict.Environment -join '; ')"
            Assert-SelfTest ($verdict.Failures.Count -eq 0) "a tool the install named must not fail the job, but got: $($verdict.Failures -join '; ')"
            Assert-SelfTest ($verdict.Environment[0] -like '*chrome-devtools-axi*') "the environment result must name the tool: $($verdict.Environment[0])"
            Assert-SelfTest ($verdict.Environment[0] -like '*install failed: exited with code 1*') "the environment result must carry the log's reason: $($verdict.Environment[0])"
        }
        'unnamed-tool-fails' {
            $verdict = Get-DoctorVerdict -Doctor @($missingLavish) -Log $log.Replace('chrome-devtools-axi, lavish-axi', 'chrome-devtools-axi')
            Assert-SelfTest ($verdict.Environment.Count -eq 0) "a tool the install did not name is no environment result: $($verdict.Environment -join '; ')"
            Assert-SelfTest ($verdict.Failures.Count -eq 1) "want the unnamed tool to fail, got: $($verdict.Failures -join '; ')"
            Assert-SelfTest ($verdict.Failures[0] -like '*lavish-axi*') "the failure must name the tool: $($verdict.Failures[0])"
        }
        'every-named-tool-is-environment' {
            $verdict = Get-DoctorVerdict -Doctor @($missingChrome, $missingLavish) -Log $log
            Assert-SelfTest ($verdict.Environment.Count -eq 2) "want both named tools as environment results, got: $($verdict.Environment -join '; ')"
            Assert-SelfTest ($verdict.Failures.Count -eq 0) "no named tool may fail the job, but got: $($verdict.Failures -join '; ')"
            Assert-SelfTest ($verdict.Environment[1] -like '*lavish-axi*') "the second result must name its tool: $($verdict.Environment[1])"
        }
        'broken-tool-is-environment' {
            $verdict = Get-DoctorVerdict -Doctor @($brokenClaude) -Log $claudeLog
            Assert-SelfTest ($verdict.Environment.Count -eq 1) "a broken tool the install named is the environment's, got: $($verdict.Environment -join '; ')"
            Assert-SelfTest ($verdict.Failures.Count -eq 0) "a named broken tool must not fail the job, but got: $($verdict.Failures -join '; ')"
            Assert-SelfTest ($verdict.Environment[0] -like '*claude*installer exited with code 1*') "the environment result must name the tool and the log's reason: $($verdict.Environment[0])"
        }
        'log-without-the-note-decides-nothing' {
            $silent = ($log -split "`n" | Where-Object { $_ -notlike 'Note: *' }) -join "`n"
            $verdict = Get-DoctorVerdict -Doctor @($missingChrome) -Log $silent
            Assert-SelfTest ($verdict.Environment.Count -eq 0) "a log with no note names nothing, so nothing is the environment's: $($verdict.Environment -join '; ')"
            Assert-SelfTest ($verdict.Failures.Count -eq 1) "want the missing tool to fail when the note does not name it, got: $($verdict.Failures -join '; ')"
        }
        'skill-named-in-the-note-is-environment' {
            Assert-SelfTest ((Get-LeftOutTools $ghSkillLog) -contains 'gh-axi skill') "the note must read as the gh-axi skill, got: $((Get-LeftOutTools $ghSkillLog) -join ', ')"
            $reason = Get-LeftOutReason -Text $ghSkillLog -Name 'gh-axi skill'
            Assert-SelfTest ($reason -like '*skill install exited with code 1*') "the reason must be the log's line for the skill, got '$reason'"
            $unnamed = Get-LeftOutReason -Text $log -Name 'gh-axi skill'
            Assert-SelfTest (-not $unnamed) "a skill the note did not name has no reason, got '$unnamed'"
        }
        'named-with-no-log-line-is-environment' {
            $bare = "Note: These could not be installed, so only what needs them is left out: tasks-axi. The log says why."
            $reason = Get-LeftOutReason -Text $bare -Name 'tasks-axi'
            Assert-SelfTest ($reason -like '*closing note*') "a note that names the tool is the install's word for it, got '$reason'"
            $verdict = Get-DoctorVerdict -Doctor @('MISSING  tasks-axi not found on PATH (install: npm install -g tasks-axi)') -Log $bare
            Assert-SelfTest ($verdict.Environment.Count -eq 1 -and $verdict.Failures.Count -eq 0) "want the named tool as an environment result, got $($verdict.Environment -join '; ') and $($verdict.Failures -join '; ')"
        }
        'summary-names-the-tool-and-the-reason' {
            $verdict = Get-DoctorVerdict -Doctor @($missingChrome) -Log $log
            $path = Join-Path ([System.IO.Path]::GetTempPath()) "install-left-out-$([guid]::NewGuid()).md"
            try {
                $said = @(Write-EnvironmentResult -Lines $verdict.Environment -Summary $path)
                $summary = Get-Content -Raw -Path $path
                Assert-SelfTest ($summary.Contains('chrome-devtools-axi')) 'the summary must name the tool'
                Assert-SelfTest ($summary.Contains('install failed: exited with code 1')) "the summary must carry the log's reason: $summary"
                Assert-SelfTest ($summary.Contains('Environment result')) 'the summary must carry the environment heading'
                $warned = @($said | Where-Object { $_ -like '::warning*chrome-devtools-axi*' })
                Assert-SelfTest ($warned.Count -eq 1) "the warning annotation must name the tool, got: $($said -join '; ')"
            } finally {
                Remove-Item -Force -ErrorAction SilentlyContinue $path
            }
        }
        default { throw "Unknown install left-out self-test $SelfTest." }
    }
    Write-Output "install left-out self-test $SelfTest passed."
    exit 0
}
