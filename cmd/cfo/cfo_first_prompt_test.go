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
// hook and, on a home's first start, starts with no prompt. A CFO started
// again is given its first instruction on every harness, in place of the
// first prompt. A conversation that is resumed keeps its arguments first, the
// prompt last.
func TestACFOWithNoSessionStartHookStartsWithAPromptThatRegistersIt(t *testing.T) {
	const back = "You are the CFO of this Code Goblins home, started again."
	tests := []struct {
		name    string
		harness string
		resume  []string
		prompt  string
		want    []string
	}{
		{"Claude Code, new", "claude", nil, "", nil},
		{"Claude Code, resumed", "claude", []string{"--resume", "a1b2"}, "", []string{"--resume", "a1b2"}},
		{"Claude Code, started again", "claude", nil, back, []string{back}},
		{"Claude Code, resumed again", "claude", []string{"--resume", "a1b2"}, back, []string{"--resume", "a1b2", back}},
		{"Codex, new", "codex", nil, "", []string{cfoFirstPrompt}},
		{"Codex, resumed", "codex", []string{"resume", "a1b2"}, "", []string{"resume", "a1b2", cfoFirstPrompt}},
		{"Codex, resumed again", "codex", []string{"resume", "a1b2"}, back, []string{"resume", "a1b2", back}},
		{"pi, new", "pi", nil, "", []string{cfoFirstPrompt}},
		{"pi, started again", "pi", nil, back, []string{back}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got := cfoStartArguments(test.harness, test.resume, test.prompt)

			// Assert
			if !slices.Equal(got, test.want) {
				t.Errorf("cfoStartArguments(%s, %q, %q) = %q, want %q", test.harness, test.resume, test.prompt, got, test.want)
			}
		})
	}
}

// A CFO started again is told it is the home's CFO, why it started again and
// why it is on a new conversation, and to take up the fleet's work without
// waiting for the Overlord. Claude Code's SessionStart hook has registered it;
// a Codex or pi CFO is told to register itself first.
func TestACFOStartedAgainIsToldWhyAndToTakeUpItsWork(t *testing.T) {
	for _, test := range []struct {
		harness  string
		register bool
	}{
		{"claude", false},
		{"codex", true},
		{"pi", true},
	} {
		t.Run(test.harness, func(t *testing.T) {
			// Act
			prompt := cfoBackPrompt(test.harness, "its terminal ended with exit code 1", "You start on a new conversation. Its last conversation is 404 MB, past the 20 MB a CFO resumes.")

			// Assert
			for _, want := range []string{"You are the CFO of this Code Goblins home, started again because its terminal ended with exit code 1.", "Its last conversation is 404 MB", "not from the Overlord", "Run cfo drain", "carry on with the fleet without waiting for the Overlord"} {
				if !strings.Contains(prompt, want) {
					t.Errorf("cfoBackPrompt(%s) = %q, want it to say %q", test.harness, prompt, want)
				}
			}
			if strings.Contains(prompt, "cfo register") != test.register {
				t.Errorf("cfoBackPrompt(%s) = %q, want cfo register named %v", test.harness, prompt, test.register)
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
			program, err := nativeCFOProgram(harness, cfoStartArguments(harness, nil, "")...)

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

// Every first instruction of a CFO started again reaches a Codex or pi CFO,
// started by cmd /c, whole as its last argument, whatever brought it back and
// whichever conversation it is on.
func TestEveryFirstInstructionOfACFOStartedAgainIsInWordsCmdLeavesAlone(t *testing.T) {
	whys := []string{
		"its terminal ended with exit code 3221225786, after an update of Codex was installed while it ran",
		"its terminal ended with its host, which recorded nothing of how",
		"the Overlord reopened it from the board",
		"the Overlord opened the board, which reopened it",
		"the machine restarted or its Windows sign-in ended, which ended its terminal",
		"goblins started it again after it closed",
		restartedWhy,
	}
	freshes := []string{
		"",
		"You start on a new conversation. Its last conversation is 404 MB, past the 20 MB a CFO resumes.",
		"You start on a new conversation. Your conversation 0f3c9a2e-5b1d-4c7e-9a8b-2d6e4f1a3b5c could not be resumed.",
		"You start on a new conversation. pi has written no file for its conversation a1b2, which it writes when its first reply ends.",
		"The CFO starts a new conversation in codex because its harness changed.",
	}
	for _, harness := range []string{"codex", "pi"} {
		t.Run(harness, func(t *testing.T) {
			// Arrange
			harnessesOnPath(t, harness)
			for _, why := range whys {
				for _, fresh := range freshes {
					prompt := cfoBackPrompt(harness, why, fresh)

					// Act
					program, err := nativeCFOProgram(harness, cfoStartArguments(harness, nil, prompt)...)

					// Assert
					if err != nil || len(program) == 0 || program[len(program)-1] != prompt {
						t.Errorf("nativeCFOProgram(%s) with %q = %q, %v; want the instruction whole as its last argument", harness, prompt, program, err)
					}
				}
			}
		})
	}
}
