package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// standInShellVariable names the file a stand-in shell writes when it starts.
const standInShellVariable = "GOBLINS_TEST_STAND_IN_SHELL"

// runStandInShell runs this binary as the Windows PowerShell or Git Bash a
// test put where the run check looks for its shells: it says it started and
// never answers, as CI's powershell.exe sat past the check's 30 s wait on
// 2026-11-07.
func runStandInShell() (int, bool) {
	started := os.Getenv(standInShellVariable)
	name := filepath.Base(os.Args[0])
	if started == "" || (!strings.EqualFold(name, "powershell.exe") && !strings.EqualFold(name, "bash.exe")) {
		return 0, false
	}
	if err := os.WriteFile(started, []byte(name+"\n"), 0o600); err != nil {
		return 2, true
	}
	time.Sleep(time.Minute)
	return 0, true
}

// The afk command's test of its --run wiring starts no shell of the machine:
// on 2026-11-07 (#2142) CI's first Windows PowerShell sat past the check's
// 30 s wait and the test failed by chance. Here every shell the check could
// start never answers, so a test that started one would sit the whole wait
// and fail as CI did.
func TestAFKLogTakesACommandWhileNoShellOfTheMachineCanAnswer(t *testing.T) {
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	system := t.TempDir()
	powershell := filepath.Join(system, "System32", "WindowsPowerShell", "v1.0")
	git := t.TempDir()
	for _, dir := range []string{powershell, filepath.Join(git, "cmd"), filepath.Join(git, "bin")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	copyFile(t, program, filepath.Join(powershell, "powershell.exe"))
	copyFile(t, program, filepath.Join(git, "bin", "bash.exe"))
	if err := os.WriteFile(filepath.Join(git, "cmd", "git.exe"), nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SystemRoot", system)
	t.Setenv("PATH", filepath.Join(git, "cmd")+string(os.PathListSeparator)+os.Getenv("PATH"))
	for name, tc := range map[string]struct{ file, shell string }{
		"a PowerShell script": {"refresh.ps1", "powershell"},
		"a Git Bash script":   {"refresh.sh", "bash"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			started := filepath.Join(t.TempDir(), "shell-started")
			t.Setenv(standInShellVariable, started)
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
			if _, err := os.Stat(started); err == nil {
				t.Errorf("the test started a shell of the machine, %s, which can sit past the check's wait", started)
			}
		})
	}
}
