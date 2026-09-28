package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

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
