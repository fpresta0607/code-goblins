package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// The CFO's question is held to the same rule as a goblin's: a choice that
// is only a letter or number is refused before the command reaches a home.
func TestQuestionRefusesAChoiceThatIsOnlyALetterOrNumber(t *testing.T) {
	resolved := 0
	runtime := commandRuntime{resolveHome: func() (home.Home, error) {
		resolved++
		return home.Home{}, errors.New("no home in this test")
	}}
	ask := func(options ...string) (int, string) {
		args := []string{"--id", "question-letters", "--text", "Merge now?"}
		for _, option := range options {
			args = append(args, "--option", option)
		}
		var stdout, stderr bytes.Buffer
		return runQuestion(args, &stdout, &stderr, runtime), stderr.String()
	}
	for _, options := range [][]string{{"a", "b"}, {"Merge now", "2"}, {"(c)", "Wait"}} {
		if exit, stderr := ask(options...); exit != 2 || !strings.Contains(stderr, "write the answer itself") {
			t.Fatalf("%q: exit=%d stderr=%q, want it refused with how to write the answer", options, exit, stderr)
		}
	}
	if resolved != 0 {
		t.Fatalf("a refused question reached the home %d times", resolved)
	}
	if exit, stderr := ask("Fix it next", "Keep 300 s", "A plan"); exit != 1 || resolved != 1 {
		t.Fatalf("exit=%d resolved=%d stderr=%q, want answers written as phrases past the check and on to the home", exit, resolved, stderr)
	}
}

// While AFK mode is on, what the CFO would have asked the Overlord is held for
// him with the choice the CFO recommends, so a question with choices and no
// --recommend is refused before it reaches the supervisor. A written question
// offers no choice to recommend, and while AFK mode is off nothing is held:
// both go on to the supervisor, which this test has none of.
func TestQuestionWhileAFKModeIsOnNamesTheChoiceTheCFORecommends(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	h := home.Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }}
	ask := func(args ...string) (int, string) {
		var stdout, stderr bytes.Buffer
		args = append([]string{"--id", "drop-legacy-invoices", "--text", "Migration 0042 drops legacy_invoices. Apply it?"}, args...)
		return runQuestion(args, &stdout, &stderr, runtime), stderr.String()
	}
	choices := []string{"--option", "Apply it", "--option", "Keep it held"}
	offExit, off := ask(choices...)
	if _, _, err := afk.TurnOn(h.State, "his own terminal (powershell.exe pid 4242)", nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Act
	refusedExit, refused := ask(choices...)
	recommendedExit, recommended := ask(append(choices, "--recommend", "Keep it held")...)
	writtenExit, written := ask()

	// Assert
	if refusedExit != 2 || !strings.Contains(refused, "AFK mode is on") || !strings.Contains(refused, "--recommend") {
		t.Errorf("a question with choices and no recommendation while AFK mode is on: exit=%d stderr=%q, want it refused, naming --recommend", refusedExit, refused)
	}
	for name, outcome := range map[string]struct {
		exit   int
		stderr string
	}{"with AFK mode off": {offExit, off}, "with a recommendation": {recommendedExit, recommended}, "written": {writtenExit, written}} {
		if outcome.exit != 1 || strings.Contains(outcome.stderr, "AFK mode is on") {
			t.Errorf("a question %s: exit=%d stderr=%q, want it past the check and refused only by the missing supervisor", name, outcome.exit, outcome.stderr)
		}
	}
}
