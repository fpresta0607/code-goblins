package installtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dictationNote is the one sentence the install says when dictation could
// not be set up.
const dictationNote = "Note: Dictation could not be set up now, so it finishes setting itself up the first time you dictate; the log says why."

// homeCfo puts the stand-in where cfo install puts cfo.exe in the per-user
// home, which the stand-in's own install does not.
func homeCfo(t *testing.T) func(local string) {
	t.Helper()
	program := standIn(t)
	return func(local string) {
		bin := filepath.Join(local, "CodeGoblins", "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "cfo.exe"), program, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// The install sets dictation up with the build the home now holds, after
// that build has set the home up, and says so in its one plain line.
func TestOneLineInstallSetsDictationUpWithTheHomesBuild(t *testing.T) {
	// Arrange
	releases, _ := serveNoMistakesReleases(t, zipped(t, "no-mistakes.exe", standIn(t)), 0, publishedSums)

	// Act
	run := runInstallWithNoMistakes(t, WindowsPowerShell(), releases, noMistakesSetup{seed: homeCfo(t)})

	// Assert
	installed := strings.Index(run.record, "cfo install\r\n")
	dictation := strings.Index(run.record, "cfo dictation setup\r\n")
	if installed < 0 || dictation < installed {
		t.Fatalf("want cfo install, then cfo dictation setup:\n%s\n%s", run.record, run.output)
	}
	if !strings.Contains(run.output, "\n      Setting up dictation\r\n") {
		t.Errorf("the install did not say it sets dictation up:\n%s", run.output)
	}
	if strings.Contains(run.output, dictationNote) || strings.Contains(run.output, "Failed:") {
		t.Errorf("a dictation that was set up was reported as not:\n%s", run.output)
	}
	if !strings.Contains(run.output, "Done: Code Goblins is installed") {
		t.Errorf("the install did not end well:\n%s", run.output)
	}
}

// A dictation that cannot be set up never fails the install: it says so in
// one sentence and the install ends well, and the first dictation sets it up
// as it always could.
func TestOneLineInstallEndsWellWhenDictationCannotBeSetUp(t *testing.T) {
	// Arrange
	releases, _ := serveNoMistakesReleases(t, zipped(t, "no-mistakes.exe", standIn(t)), 0, publishedSums)

	// Act
	run := runInstallWithNoMistakes(t, WindowsPowerShell(), releases, noMistakesSetup{seed: homeCfo(t), env: []string{standInFailVariable + "=dictation setup"}})

	// Assert
	if !strings.Contains(run.record, "cfo dictation setup\r\n") {
		t.Fatalf("the install did not try to set dictation up:\n%s\n%s", run.record, run.output)
	}
	if !strings.Contains(run.output, dictationNote) {
		t.Errorf("the install did not say dictation finishes at the first dictation:\n%s", run.output)
	}
	if strings.Contains(run.output, "Failed:") || !strings.Contains(run.output, "Done: Code Goblins is installed") {
		t.Errorf("a dictation that could not be set up failed the install:\n%s", run.output)
	}
}
