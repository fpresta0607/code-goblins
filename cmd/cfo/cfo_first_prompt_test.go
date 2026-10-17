package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/standin"
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

// A Codex or pi CFO draws on its harness's own full screen, as a goblin
// does and as Claude Code's does for the CFO, which keeps the input line and
// footer on the bottom rows while the transcript scrolls above them: the
// Overlord, 2026-10-17, "when you scroll on like Pi and Codex terminals, it
// doesn't keep like the bottom banner". Codex copies what a drag selects when
// the button is released, as he asked on 2026-10-02. pi is asked its help
// first, as a pi goblin is: one whose --tui-mode offers no fullscreen is
// given no mode, so it draws the only way it can.
func TestACodexOrPiCFODrawsOnItsHarnesssOwnFullScreen(t *testing.T) {
	for _, test := range []struct {
		name, harness, piHelp string
		want                  []string
	}{
		{"Codex", "codex", "", []string{"-c", "tui.copy_on_select=always"}},
		{"pi offering fullscreen", "pi", "Options:\n  --tui-mode <mode>              TUI mode: regular (default) or fullscreen\n", []string{"--tui-mode", "fullscreen"}},
		{"pi offering only regular", "pi", "Options:\n  --tui-mode <mode>              TUI mode: regular\n", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			bin := t.TempDir()
			standin.RemoveAtCleanup(t, bin)
			standin.Put(t, filepath.Join(bin, "pi.exe"))
			t.Setenv("PATH", bin)
			for _, variable := range standin.Env("", standin.Rule{Args: "--help", Stdout: test.piHelp}) {
				name, value, _ := strings.Cut(variable, "=")
				t.Setenv(name, value)
			}

			// Act
			got, err := cfoScreenArguments(test.harness)

			// Assert
			if err != nil {
				t.Fatalf("cfoScreenArguments(%s): %v", test.harness, err)
			}
			if !slices.Equal(got, test.want) {
				t.Errorf("cfoScreenArguments(%s) = %q, want %q", test.harness, got, test.want)
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
