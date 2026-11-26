package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// The afk command's test of its --run wiring starts no shell of the machine:
// on 2026-11-07 (#2142) CI's first Windows PowerShell sat past the check's
// 30 s wait and the test failed by chance. Here the machine has no shell the
// check could start, so a test that went to the machine's shells would be
// refused as Windows PowerShell or Git Bash not installed.
func TestAFKLogTakesACommandWhereTheMachineHasNoShell(t *testing.T) {
	t.Setenv("SystemRoot", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	for name, tc := range map[string]struct{ file, shell string }{
		"a PowerShell script": {"refresh.ps1", "powershell"},
		"a Git Bash script":   {"refresh.sh", "bash"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			runtime, _ := afkRuntime(t)
			var sent afk.Entry
			runtime.logAFK = func(_ home.Home, entry afk.Entry) error { sent = entry; return nil }
			file := filepath.Join(t.TempDir(), tc.file)
			if err := os.WriteFile(file, []byte("gh auth refresh -h github.com -s user\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			exit, _, stderr := afkCommand(runtime, "log", "--kind", "his", "--what", "Let the fleet read what GitHub bills", "--evidence", "his own sign-in", "--run", file)

			// Assert
			if exit != 0 || sent.Shell != tc.shell {
				t.Errorf("exit=%d stderr=%q sent=%+v, want his step taken with the %s shell", exit, stderr, sent, tc.shell)
			}
		})
	}
}

// goblins afk log checks a command with its shell where the runtime names no
// check of its own, as production's does. The machine here has no Windows
// PowerShell, so the real check refuses the command as one it cannot check,
// nothing is logged, and no shell is started.
func TestAFKLogChecksACommandWithItsShellWhereTheRuntimeNamesNoCheck(t *testing.T) {
	// Arrange
	t.Setenv("SystemRoot", t.TempDir())
	runtime, _ := afkRuntime(t)
	runtime.checkRunCommand = nil
	sent := 0
	runtime.logAFK = func(home.Home, afk.Entry) error { sent++; return nil }
	file := filepath.Join(t.TempDir(), "refresh.ps1")
	if err := os.WriteFile(file, []byte("gh auth refresh -h github.com -s user\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	exit, _, stderr := afkCommand(runtime, "log", "--kind", "his", "--what", "Let the fleet read what GitHub bills", "--evidence", "his own sign-in", "--run", file)

	// Assert
	if exit != 2 || sent != 0 || !strings.Contains(stderr, file+": Windows PowerShell 5.1 is not installed") {
		t.Errorf("exit=%d sent=%d stderr=%q, want the command refused by its shell's check before anything is logged", exit, sent, stderr)
	}
}
