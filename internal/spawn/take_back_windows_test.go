package spawn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// claudePromptLine stands in for Claude Code's prompt line. One ClearLine
// takes row characters off it, the row its cursor is in, as Claude Code
// 2.1.322 did live on 2026-11-10, and nothing while isStuck. He types his
// onto it the moment it is empty.
type claudePromptLine struct {
	words   string
	row     int
	isStuck bool
	his     string
	presses int
}

func (p *claudePromptLine) read() ([]string, error) {
	rule := strings.Repeat("─", 80)
	return []string{"● Done.", rule, "❯ " + p.words, rule, "⏵⏵ bypass permissions on (shift+tab to cycle)"}, nil
}

func (p *claudePromptLine) press(keys []byte) error {
	if string(keys) != harness.ClearLine {
		return fmt.Errorf("pressed %q, want only the clear key", keys)
	}
	p.presses++
	if !p.isStuck {
		words := []rune(p.words)
		p.words = string(words[:max(0, len(words)-p.row)])
	}
	if p.words == "" {
		p.words, p.his = p.his, ""
	}
	return nil
}

// TakeBack reports words taken back off a prompt line only once it pressed
// its key and then read the line empty. It presses once for each row Claude
// Code gives up, and never behind a dialog, on a screen it cannot read, for
// words the screen does not show, which a harness that has not read them yet
// would take the key as part of, or over words of his: beside the delivery's,
// alone, or typed as the line cleared (#2306).
func TestTakeBackPressesOnlyForWordsItSeesAndReportsThemGoneOnceTheLineIsEmpty(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("stop and rebase ", 15))
	dialog := []string{" Quick safety check: Is this a project you created or one you trust?", "   No, exit", " ❯ Yes, I trust this folder"}
	for name, test := range map[string]struct {
		line        claudePromptLine
		typed       string
		screen      []string
		readErr     error
		wantTaken   bool
		wantPresses int
	}{
		"words on one row":               {line: claudePromptLine{words: "stop and rebase", row: 100}, typed: "stop and rebase", wantTaken: true, wantPresses: 1},
		"words over three rows":          {line: claudePromptLine{words: long, row: 100}, typed: long, wantTaken: true, wantPresses: 3},
		"a paste shown as a placeholder": {line: claudePromptLine{words: "[Pasted text #1 +3 lines]", row: 100}, typed: long, wantTaken: true, wantPresses: 1},
		"words the screen does not show": {line: claudePromptLine{row: 100}, typed: "stop and rebase"},
		"his words after the delivery's": {line: claudePromptLine{words: "stop and rebase and the", row: 100}, typed: "stop and rebase"},
		"his words alone":                {line: claudePromptLine{words: "wait for me", row: 100}, typed: "stop and rebase"},
		"his words as the line cleared":  {line: claudePromptLine{words: "stop and rebase", row: 100, his: "wait for me"}, typed: "stop and rebase", wantPresses: 1},
		"a dialog over the prompt line":  {line: claudePromptLine{words: "stop and rebase", row: 100}, typed: "stop and rebase", screen: dialog},
		"a screen that cannot be read":   {line: claudePromptLine{words: "stop and rebase", row: 100}, typed: "stop and rebase", readErr: errors.New("the terminal does not answer")},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			screens, _ := harness.NativeScreens(harness.Claude)
			line := test.line
			read := line.read
			if test.screen != nil || test.readErr != nil {
				read = func() ([]string, error) { return test.screen, test.readErr }
			}

			// Act
			isTaken := TakeBack(read, line.press, screens, test.typed)

			// Assert
			if isTaken != test.wantTaken || line.presses != test.wantPresses {
				t.Errorf("TakeBack = %t after %d presses; want %t after %d", isTaken, line.presses, test.wantTaken, test.wantPresses)
			}
		})
	}
}

// Words that stay on the prompt line however often the key is pressed are
// not reported taken back, and the pressing ends.
func TestTakeBackGivesUpOnWordsThatNeverLeave(t *testing.T) {
	// Arrange
	previous := takeBackWait
	takeBackWait = 300 * time.Millisecond
	t.Cleanup(func() { takeBackWait = previous })
	screens, _ := harness.NativeScreens(harness.Claude)
	line := claudePromptLine{words: "stop and rebase", row: 100, isStuck: true}

	// Act
	began := time.Now()
	isTaken := TakeBack(line.read, line.press, screens, "stop and rebase")

	// Assert
	if isTaken || line.presses == 0 || time.Since(began) > 5*time.Second {
		t.Errorf("TakeBack = %t after %d presses in %s; want it to press, then give up soon", isTaken, line.presses, time.Since(began))
	}
}

// A board delivery that ends between typing its words and its submit key
// leaves nothing typed for the next delivery to join: it takes them back off
// the composer and says so, and nothing of it is submitted (#2306). A send
// that does not take its text back ends as it did, its words left there.
func TestADeliveryThatEndsBeforeItsSubmitKeyTakesItsWordsBackOnlyWhenAsked(t *testing.T) {
	for _, test := range []struct {
		name           string
		shouldTakeBack bool
		wantCleared    int
	}{
		{name: "a board delivery", shouldTakeBack: true, wantCleared: 1},
		{name: "a send that leaves its words", shouldTakeBack: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newNativeFixture(t, harness.Codex, "turns")
			if _, err := f.service.Spawn(t.Context(), f.request); err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
			if err != nil {
				t.Fatal(err)
			}
			awaitComposer(t, f)
			ended := errors.New("the delivery's time ran out")
			f.service.ShouldTakeBack = test.shouldTakeBack
			f.service.Sleep = func(ctx context.Context, duration time.Duration) error {
				if duration == launchSettle {
					return ended
				}
				timer := time.NewTimer(duration) // stall-accepted: it sleeps for the send's own pauses, which are meant to run out.
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-timer.C:
					return nil
				}
			}

			// Act
			err = f.service.SendNative(t.Context(), meta, "stop and rebase")

			// Assert
			if !errors.Is(err, ended) || errors.Is(err, ErrTakenBack) != test.shouldTakeBack || errors.Is(err, ErrLeftTyped) {
				t.Errorf("SendNative = %v; want it ended as it did, taken back %t", err, test.shouldTakeBack)
			}
			events := f.events(t)
			if cleared := named(events, "cleared"); len(cleared) != test.wantCleared || test.wantCleared == 1 && !strings.Contains(cleared[0].Text, "stop and rebase") {
				t.Errorf("cleared = %+v, want %d of his words", cleared, test.wantCleared)
			}
			for _, event := range named(events, "submitted") {
				if strings.Contains(event.Text, "stop and rebase") {
					t.Errorf("submitted %q, want nothing of the ended delivery submitted", event.Text)
				}
			}
		})
	}
}
