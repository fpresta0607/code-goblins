package codegoblins

import (
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
// and the summary that must tell the three outcomes apart. The hosted-runner
// proof itself runs where the install workflow calls the script. Every
// self-test asserts what its name says and throws otherwise, so a case that
// prints its passed line exited zero having checked its results.
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
// check runs tests/acceptance/dictation.ps1, which owns the environment
// result, rather than throwing on doctor's dictation line itself, and a
// second install reads the outcome that script recorded instead of demanding
// the model was already in place.
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

	// Assert
	for job, step := range checks {
		if !strings.Contains(step, script) {
			t.Errorf("the %s job's home check does not run %s, so its dictation premise is the runner's to fail:\n%s", job, script, step)
		}
		if strings.Contains(step, "goblins doctor does not report dictation ready") {
			t.Errorf("the %s job's home check still throws on doctor's dictation line, want tests/acceptance/dictation.ps1 to decide:\n%s", job, step)
		}
	}
	if !strings.Contains(again, "INSTALL_DICTATION") {
		t.Errorf("the second install does not read the dictation outcome the check recorded, so a runner that could not download the model fails it here:\n%s", again)
	}
}
