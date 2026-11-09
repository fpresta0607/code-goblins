package codegoblins

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestDictationAcceptanceScriptSelfTests runs the acceptance script's own
// fixture harness over results it never measured: a setup that succeeded, a
// home that was already ready, a download the runner could not make (an
// environment result that must not fail the job), a checksum mismatch (a
// product failure that must name the part), doctor ready with the wrong
// number of verified parts, a setup that ended zero without a ready doctor,
// the summary that must tell the three outcomes apart, and the reason the
// summary carries for a failing setup: its own last line, not the shell's
// formatted error block. The hosted-runner proof itself runs where the
// install workflow calls the script. Every self-test asserts what its name
// says and throws otherwise, so a case that prints its passed line exited
// zero having checked its results.
func TestDictationAcceptanceScriptSelfTests(t *testing.T) {
	// Arrange
	script := filepath.Join("tests", "acceptance", "dictation.ps1")
	for _, name := range []string{
		"setup-succeeds",
		"already-ready",
		"download-fails",
		"checksum-mismatch",
		"ready-wrong-parts",
		"setup-exit-zero-but-not-ready",
		"summary-distinguishes",
		"setup-reason-is-what-the-setup-said",
	} {
		t.Run(name, func(t *testing.T) {
			command := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script, "-SelfTest", name)
			command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

			// Act
			output, err := command.CombinedOutput()

			// Assert
			if err != nil {
				t.Fatalf("the self-test failed: %v\n%s", err, output)
			}
			passed := "dictation self-test " + name + " passed."
			if !strings.Contains(string(output), passed) {
				t.Fatalf("the self-test did not report %q:\n%s", passed, output)
			}
		})
	}
}

// TestInstallWorkflowRunsTheDictationAcceptanceScript keeps the install
// workflow honest about the one premise a runner may not host: each home
// check runs tests/acceptance/dictation.ps1 as the command whose exit code
// decides the step, and decides dictation in no statement of its own, so a
// reinstated throw on doctor's dictation line fails here; and a second
// install reads the outcome that script recorded, its strict already-set-up
// assertion guarding only the case where the check did not record a runner
// that could not download the model.
func TestInstallWorkflowRunsTheDictationAcceptanceScript(t *testing.T) {
	// Arrange
	workflow := filepath.Join(".github", "workflows", "install.yml")
	const script = `tests\acceptance\dictation.ps1`

	// Act
	checks := map[string]string{
		"one-line": workflowStep(t, workflow, "one-line", "Check the installed home"),
		"dev":      workflowStep(t, workflow, "dev", "Check the per-user home"),
	}
	again := workflowStep(t, workflow, "one-line", "Install again")
	facts := map[string]dictationStepFacts{}
	for job, step := range checks {
		facts[job] = parseDictationStep(t, step, script)
	}
	second := parseDictationStep(t, again, script)

	// Assert
	for job, found := range facts {
		if found.Invocations != 1 {
			t.Errorf("the %s job's home check runs %s in %d commands, want it once as the command the step's verdict rests on", job, script, found.Invocations)
		}
		if found.UngatedInvocations != 0 {
			t.Errorf("the %s job's home check ignores %s's exit code, so a real product failure passes the step", job, script)
		}
		if found.DictationStatements != 0 {
			t.Errorf("the %s job's home check decides dictation in %d statements of its own, want %s to own that rule and a failure there to fail the step", job, found.DictationStatements, script)
		}
	}
	if second.OutcomeGuards == 0 {
		t.Errorf("the second install reads no dictation outcome from the check, so a runner that could not download the model fails it:\n%s", again)
	}
	if second.DictationThrows == 0 {
		t.Errorf("the second install does not assert it found dictation already set up, so an install that downloads it again passes:\n%s", again)
	}
	if second.UnguardedThrows != 0 {
		t.Errorf("the second install asserts dictation outside the branch that reads the outcome the check recorded, so an environment result fails it:\n%s", again)
	}
}

// dictationStepFacts is what the PowerShell parser, the workflow's own shell,
// finds in one step's script: the commands that run the acceptance script and
// whether a failed one fails the step, the statements that decide dictation
// for themselves, and the second install's strict assertion with whether it
// sits in the branch where the check recorded no environment result.
type dictationStepFacts struct {
	ParseErrors         []string `json:"parseErrors"`
	Invocations         int      `json:"invocations"`
	UngatedInvocations  int      `json:"ungatedInvocations"`
	DictationStatements int      `json:"dictationStatements"`
	DictationThrows     int      `json:"dictationThrows"`
	UnguardedThrows     int      `json:"unguardedThrows"`
	OutcomeGuards       int      `json:"outcomeGuards"`
}

// parseDictationStep parses one step's script and reports those facts, so a
// comment, a string or an unreachable line cannot stand in for the command
// the step runs.
func parseDictationStep(t *testing.T, step, script string) dictationStepFacts {
	t.Helper()
	file := filepath.Join(t.TempDir(), "step.ps1")
	if err := os.WriteFile(file, []byte(step), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", dictationStepFactsScript)
	command.Env = append(os.Environ(), "DICTATION_STEP_FILE="+file, "DICTATION_STEP_SCRIPT="+script)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("reading the step's script = %v:\n%s", err, output)
	}
	var facts dictationStepFacts
	if err := json.Unmarshal(output, &facts); err != nil {
		t.Fatalf("reading the step's facts = %v:\n%s", err, output)
	}
	if len(facts.ParseErrors) > 0 {
		t.Fatalf("the step's script does not parse: %s", strings.Join(facts.ParseErrors, "; "))
	}
	return facts
}

// dictationStepFactsScript is the inspector the parser runs over the step: the
// script's invocations and their exit-code gate, the statements that mention
// dictation besides those invocations, and which of the second install's
// assertions the recorded outcome leaves in place.
const dictationStepFactsScript = `
$tokens = $null
$errors = $null
$ast = [System.Management.Automation.Language.Parser]::ParseFile($env:DICTATION_STEP_FILE, [ref]$tokens, [ref]$errors)
$script = $env:DICTATION_STEP_SCRIPT -replace '^[.][/\\]', ''
$path = '([.][/\\])?' + [regex]::Escape($script)
$facts = [ordered]@{
    parseErrors         = @($errors | ForEach-Object { $_.Message })
    invocations         = 0
    ungatedInvocations  = 0
    dictationStatements = 0
    dictationThrows     = 0
    unguardedThrows     = 0
    outcomeGuards       = 0
}
if ($errors.Count -eq 0) {
    $invocations = @($ast.FindAll({ param($node) $node -is [System.Management.Automation.Language.CommandAst] -and ($node.GetCommandName() -replace '^[.][/\\]', '') -eq $script }, $true))
    $facts.invocations = $invocations.Count
    foreach ($invocation in $invocations) {
        $statement = $invocation.Parent
        $next = $null
        if ($statement.Parent -and $statement.Parent.PSObject.Properties['Statements']) {
            $statements = @($statement.Parent.Statements)
            $at = $statements.IndexOf($statement)
            if ($at -ge 0 -and $at + 1 -lt $statements.Count) { $next = $statements[$at + 1] }
        }
        $gated = $false
        if ($next -is [System.Management.Automation.Language.IfStatementAst]) {
            $fails = @($next.Clauses[0].Item2.FindAll({ param($node) $node -is [System.Management.Automation.Language.ExitStatementAst] -or $node -is [System.Management.Automation.Language.ThrowStatementAst] }, $true))
            $gated = $next.Clauses[0].Item1.Extent.Text -match 'LASTEXITCODE' -and $fails.Count -gt 0
        }
        if (-not $gated) { $facts.ungatedInvocations++ }
    }
    $guards = @($ast.FindAll({ param($node) $node -is [System.Management.Automation.Language.IfStatementAst] -and $node.Clauses[0].Item1 -and $node.Clauses[0].Item1.Extent.Text -match 'INSTALL_DICTATION' }, $true))
    $facts.outcomeGuards = $guards.Count
    $deciders = @($ast.FindAll({ param($node) $node -is [System.Management.Automation.Language.IfStatementAst] -or $node -is [System.Management.Automation.Language.ThrowStatementAst] }, $true))
    foreach ($decider in $deciders) {
        if (($decider.Extent.Text -replace $path, '') -notmatch '(?i)dictation') { continue }
        $facts.dictationStatements++
        if ($decider -isnot [System.Management.Automation.Language.ThrowStatementAst]) { continue }
        $facts.dictationThrows++
        $guarded = $false
        $ancestor = $decider.Parent
        while ($ancestor -and -not $guarded) {
            foreach ($guard in $guards) { if ($ancestor -eq $guard.ElseClause) { $guarded = $true } }
            $ancestor = $ancestor.Parent
        }
        if (-not $guarded) { $facts.unguardedThrows++ }
    }
}
$facts | ConvertTo-Json -Compress
`
