package codegoblins

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestInstallLeftOutAcceptanceScriptSelfTests runs the install note's fixture
// harness over a log and doctor lines it never measured: a tool the install
// named as one it could not install is an environment result that carries the
// log's reason and never fails the job, a tool it did not name still fails, a
// broken tool it named is the same environment result, a log with no closing
// note names nothing, a skill the note names is the environment's, a note
// names a tool even when the log's WARN line is missing, and the summary and
// the warning annotation carry the tool and the reason. Every self-test
// asserts what its name says and throws otherwise, so a case that prints its
// passed line exited zero having checked its results. The hosted-runner proof
// itself runs where the install workflow's home checks read the real log.
func TestInstallLeftOutAcceptanceScriptSelfTests(t *testing.T) {
	// Arrange
	script := filepath.Join("tests", "acceptance", "install_left_out.ps1")
	for _, name := range []string{
		"named-tool-is-environment",
		"unnamed-tool-fails",
		"every-named-tool-is-environment",
		"broken-tool-is-environment",
		"log-without-the-note-decides-nothing",
		"skill-named-in-the-note-is-environment",
		"named-with-no-log-line-is-environment",
		"summary-names-the-tool-and-the-reason",
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
			passed := "install left-out self-test " + name + " passed."
			if !strings.Contains(string(output), passed) {
				t.Fatalf("the self-test did not report %q:\n%s", passed, output)
			}
		})
	}
}
