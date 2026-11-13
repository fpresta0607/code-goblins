package doctor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/standin"
)

// startSleeper runs program as a sleeper with args and keeps it until the
// test ends.
func startSleeper(t *testing.T, program string, args ...string) int {
	t.Helper()
	sleeper := exec.Command(program, args...)
	// The variable makes this test binary wait, and the rule the tests'
	// stand-in program, whichever of the two program is.
	sleeper.Env = append(append(os.Environ(), sleeperVariable+"=1"), standin.Env("", standin.Rule{Any: true, Hold: true})...)
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	standin.Hold(t, sleeper.Process.Pid)
	return sleeper.Process.Pid
}

// A process runs from a folder when its program is in it, or, as node runs a
// harness's script, when one of its arguments names a file in it.
func TestRunningFromFindsAProgramInTheFolderAndAScriptItRuns(t *testing.T) {
	// Arrange
	installed := filepath.Join(t.TempDir(), "node_modules", "@openai", "codex")
	scripted := filepath.Join(t.TempDir(), "node_modules", "@earendil-works", "pi-coding-agent")
	for _, folder := range []string{installed, scripted} {
		if err := os.MkdirAll(folder, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	standin.RemoveAtCleanup(t, installed)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(installed, "vendor", "codex.exe")
	if err := os.MkdirAll(filepath.Dir(program), 0o700); err != nil {
		t.Fatal(err)
	}
	standin.Put(t, program)
	fromFolder := startSleeper(t, program)
	withScript := startSleeper(t, self, filepath.Join(scripted, "dist", "cli.js"))

	// Act
	inInstalled, installedErr := RunningFrom(installed)
	inScripted, scriptedErr := RunningFrom(scripted)
	inNeither, neitherErr := RunningFrom(t.TempDir())

	// Assert
	if err := errors.Join(installedErr, scriptedErr, neitherErr); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(inInstalled, fromFolder) || slices.Contains(inInstalled, withScript) {
		t.Fatalf("running from %s = %v, want %d alone", installed, inInstalled, fromFolder)
	}
	if !slices.Contains(inScripted, withScript) || slices.Contains(inScripted, fromFolder) {
		t.Fatalf("running from %s = %v, want %d alone", scripted, inScripted, withScript)
	}
	if len(inNeither) != 0 {
		t.Fatalf("an empty folder has %v running from it", inNeither)
	}
}
