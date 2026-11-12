package codegoblins

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestOneGoblinSessionScriptSelfTests runs the install jobs' first session
// over its own fixture harness, with a stand-in goblins command that leaves
// in a temporary home what the real one leaves: the spawn starts demo-goblin
// from a brief the board's scheduler cannot also start, since a brief in the
// home's data folder is one it starts by itself and the second start fails
// one of the two; and a spawn that fails ends the session with its exit code,
// so the job reports the failure where it happens and not as a goblin a later
// step cannot find. Every self-test asserts what its name says and throws
// otherwise, so a case that prints its passed line exited zero having checked
// its results. The hosted-runner proof itself runs in the install workflow.
func TestOneGoblinSessionScriptSelfTests(t *testing.T) {
	// Arrange
	script := filepath.Join("tests", "uninstall", "one_goblin_session.ps1")
	for _, name := range []string{
		"spawn-starts-from-a-brief-the-board-does-not-queue",
		"failed-spawn-ends-the-session",
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
			passed := "one goblin session self-test " + name + " passed."
			if !strings.Contains(string(output), passed) {
				t.Fatalf("the self-test did not report %q:\n%s", passed, output)
			}
		})
	}
}
