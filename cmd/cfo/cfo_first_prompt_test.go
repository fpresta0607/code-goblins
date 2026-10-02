package main

import (
	"slices"
	"strings"
	"testing"
)

// A Codex or pi CFO has no session-start hook that registers it, so the
// first prompt it starts with tells it to: registering is what lets the board
// reach it and the supervisor wake it. Claude Code registers through its own
// hook and starts with no prompt. A conversation that is resumed keeps its
// arguments first, the prompt last.
func TestACFOWithNoSessionStartHookStartsWithAPromptThatRegistersIt(t *testing.T) {
	tests := []struct {
		name    string
		harness string
		resume  []string
		want    []string
	}{
		{"Claude Code, new", "claude", nil, nil},
		{"Claude Code, resumed", "claude", []string{"--resume", "a1b2"}, []string{"--resume", "a1b2"}},
		{"Codex, new", "codex", nil, []string{cfoFirstPrompt}},
		{"Codex, resumed", "codex", []string{"resume", "a1b2"}, []string{"resume", "a1b2", cfoFirstPrompt}},
		{"pi, new", "pi", nil, []string{cfoFirstPrompt}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got := cfoStartArguments(test.harness, test.resume)

			// Assert
			if !slices.Equal(got, test.want) {
				t.Errorf("cfoStartArguments(%s, %q) = %q, want %q", test.harness, test.resume, got, test.want)
			}
		})
	}
}

// The prompt names the one command that registers the CFO, and a harness
// started through its npm shim, by cmd /c, is given it whole as its last
// argument: cmd reads nothing in it as its own.
func TestTheFirstPromptNamesTheRegisterCommandInWordsCmdLeavesAlone(t *testing.T) {
	if !strings.Contains(cfoFirstPrompt, "cfo register") {
		t.Errorf("the first prompt %q does not name cfo register", cfoFirstPrompt)
	}
	for _, harness := range []string{"codex", "pi"} {
		t.Run(harness, func(t *testing.T) {
			// Arrange
			harnessesOnPath(t, harness)

			// Act
			program, err := nativeCFOProgram(harness, cfoStartArguments(harness, nil)...)

			// Assert
			if err != nil {
				t.Fatalf("nativeCFOProgram(%s) with the first prompt: %v", harness, err)
			}
			if len(program) == 0 || program[len(program)-1] != cfoFirstPrompt {
				t.Errorf("nativeCFOProgram(%s) = %q, want the first prompt whole as its last argument", harness, program)
			}
		})
	}
}
