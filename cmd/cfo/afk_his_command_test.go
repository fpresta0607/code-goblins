package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestAFKLogSendsACommandTheOverlordRunsWithItsShell(t *testing.T) {
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
			if exit != 0 || sent.Kind != afk.KindHis || sent.Shell != tc.shell || sent.Command != "gh auth refresh -h github.com -s user\n" {
				t.Errorf("exit=%d stderr=%q sent=%+v, want his step with its command and the %s shell", exit, stderr, sent, tc.shell)
			}
		})
	}
}

func TestAFKLogRefusesACommandThatCannotBeRunByHim(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		file := filepath.Join(dir, name)
		if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return file
	}
	for name, tc := range map[string]struct {
		args []string
		said string
	}{
		"on a deploy":            {[]string{"--kind", "deploy", "--what", "acme", "--evidence", "read", "--run", write("a.ps1", "x")}, "--run goes with --kind his"},
		"a file of another kind": {[]string{"--kind", "his", "--what", "Do it", "--evidence", "his own", "--run", write("a.cmd", "x")}, "--run takes a .ps1 file"},
		"an empty file":          {[]string{"--kind", "his", "--what", "Do it", "--evidence", "his own", "--run", write("empty.ps1", " \n")}, "is empty"},
		"a file that is missing": {[]string{"--kind", "his", "--what", "Do it", "--evidence", "his own", "--run", filepath.Join(dir, "none.ps1")}, "--run"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			runtime, _ := afkRuntime(t)
			sent := 0
			runtime.logAFK = func(home.Home, afk.Entry) error { sent++; return nil }

			// Act
			exit, _, stderr := afkCommand(runtime, append([]string{"log"}, tc.args...)...)

			// Assert
			if exit == 0 || sent != 0 || !strings.Contains(stderr, tc.said) {
				t.Errorf("exit=%d sent=%d stderr=%q, want it refused with %q before anything is logged", exit, sent, stderr, tc.said)
			}
		})
	}
}

// A his line that is a decision of his takes the --option and --recommend of
// cfo question, so its card offers those choices in place of Done and Later.
func TestAFKLogSendsTheChoicesOfADecisionOfHis(t *testing.T) {
	// Arrange
	runtime, _ := afkRuntime(t)
	var sent afk.Entry
	runtime.logAFK = func(_ home.Home, entry afk.Entry) error { sent = entry; return nil }
	choices := []string{"Approve the repair", "Dry-run it again first", "Leave it as it is"}

	// Act
	exit, _, stderr := afkCommand(runtime, "log", "--kind", "his", "--what", "Approve the production repair of Georgetown", "--evidence", "a production repair is his", "--option", choices[0], "--option", choices[1], "--option", choices[2], "--recommend", choices[1])

	// Assert
	if exit != 0 || sent.Kind != afk.KindHis || !slices.Equal(sent.Options, choices) || sent.Recommendation != choices[1] {
		t.Errorf("exit=%d stderr=%q sent=%+v, want his decision with its three choices and its recommendation", exit, stderr, sent)
	}
}

func TestAFKLogRefusesChoicesItsCardCouldNotOffer(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "a.ps1")
	if err := os.WriteFile(script, []byte("gh auth refresh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	his := []string{"--kind", "his", "--what", "Approve it", "--evidence", "his", "--link", "https://example.com"}
	for name, tc := range map[string]struct {
		args []string
		said string
	}{
		"on a deploy":                     {[]string{"--kind", "deploy", "--what", "acme", "--evidence", "read", "--option", "Ship", "--option", "Wait"}, "--option and --recommend go with --kind left, or with --kind his"},
		"with a command he runs":          {append(slices.Clone(his), "--run", script, "--option", "Run", "--option", "Wait"), "never both"},
		"one choice":                      {append(slices.Clone(his), "--option", "Approve"), "two or more --option"},
		"a recommendation and no choices": {append(slices.Clone(his), "--recommend", "Approve"), "two or more --option"},
		"a bare letter":                   {append(slices.Clone(his), "--option", "a", "--option", "b"), "only a letter or number"},
		"an answer":                       {append(slices.Clone(his), "--option", "Approve", "--option", "Wait", "--answer", "Approve"), "--answer go with --kind left"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			runtime, _ := afkRuntime(t)
			sent := 0
			runtime.logAFK = func(home.Home, afk.Entry) error { sent++; return nil }

			// Act
			exit, _, stderr := afkCommand(runtime, append([]string{"log"}, tc.args...)...)

			// Assert
			if exit == 0 || sent != 0 || !strings.Contains(stderr, tc.said) {
				t.Errorf("exit=%d sent=%d stderr=%q, want it refused with %q before anything is logged", exit, sent, stderr, tc.said)
			}
		})
	}
}
