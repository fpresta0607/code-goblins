package codegoblins

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestNoVisibleWindowsScriptSelfTests runs the acceptance script's own fixture
// harness over rows it never measured: a base row that ends early or shows no
// window, a branch row that shows a window or leaves its command unrun, a
// detached row that shows windows, leaves its command unrun or leaves a log
// line that does not say how it ended, and the summary that holds every row.
// The desktop proof itself runs only on the hosted runner of
// .github/workflows/windows.yml, where GOBLINS_WINDOW_PROOF names the default
// terminal. Every self-test asserts what its name says and throws otherwise,
// so a case that prints its passed line exited zero having checked its rows.
func TestNoVisibleWindowsScriptSelfTests(t *testing.T) {
	// Arrange
	script := filepath.Join("tests", "acceptance", "no_visible_windows.ps1")
	for _, name := range []string{
		"base-command-ends-early",
		"base-window-ends-early",
		"base-no-window",
		"branch-shows-window",
		"branch-command-unrun",
		"detached-shows-window",
		"detached-command-unrun",
		"detached-log-mismatch",
		"summary-holds-every-row",
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
			passed := "no_visible_windows self-test " + name + " passed."
			if !strings.Contains(string(output), passed) {
				t.Fatalf("the self-test did not report %q:\n%s", passed, output)
			}
		})
	}
}
