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
// onto it the moment it is empty. With hasNoBreakSpace it draws a no-break
// space after its glyph, as Claude Code 2.1.328 did live on 2026-11-19 (#2776).
type claudePromptLine struct {
	words           string
	row             int
	isStuck         bool
	hasNoBreakSpace bool
	his             string
	presses         int
}

func (p *claudePromptLine) read() ([]string, error) {
	rule := strings.Repeat("─", 80)
	prompt := "❯ "
	if p.hasNoBreakSpace {
		prompt = "❯\u00a0"
	}
	return []string{"● Done.", rule, prompt + p.words, rule, "⏵⏵ bypass permissions on (shift+tab to cycle)"}, nil
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
		"words after a no-break space":   {line: claudePromptLine{words: "stop and rebase", row: 100, hasNoBreakSpace: true}, typed: "stop and rebase", wantTaken: true, wantPresses: 1},
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
	isTaken := TakeBack(line.read, line.press, screens, "stop and rebase")

	// Assert: a press every takeBackPoll for takeBackWait is a handful, where
	// pressing for as long as it does outside a test is fifty.
	if isTaken || line.presses == 0 || line.presses > 10 {
		t.Errorf("TakeBack = %t after %d presses; want it to press a few times, then give up", isTaken, line.presses)
	}
}

// A delivery that ends between typing its words and its submit key leaves
// nothing typed for the next delivery to join: it takes them back off the
// composer and says so, and nothing of it is submitted (#2306).
func TestADeliveryThatEndsBeforeItsSubmitKeyTakesItsWordsBack(t *testing.T) {
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
	if !errors.Is(err, ended) || !errors.Is(err, ErrTakenBack) || errors.Is(err, ErrLeftTyped) {
		t.Errorf("SendNative = %v; want it ended as it did and taken back", err)
	}
	events := f.events(t)
	if cleared := named(events, "cleared"); len(cleared) != 1 || !strings.Contains(cleared[0].Text, "stop and rebase") {
		t.Errorf("cleared = %+v, want its words cleared once", cleared)
	}
	for _, event := range named(events, "submitted") {
		if strings.Contains(event.Text, "stop and rebase") {
			t.Errorf("submitted %q, want nothing of the ended delivery submitted", event.Text)
		}
	}
}

// A long goblins send whose end never shows in the composer ends once its wait
// for that runs out, as one did live on 2026-11-05, and takes back what it
// typed, so the next delivery finds an empty prompt line rather than being
// refused for words nobody sent (#2104). The service is the zero one goblins
// send builds.
func TestALongSendThatNeverShowsLeavesNoWordsBehind(t *testing.T) {
	// Arrange
	f := newNativeFixture(t, harness.Codex, "short-line")
	if _, err := f.service.Spawn(t.Context(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	awaitComposer(t, f)
	previous := nativeKeyEffect
	nativeKeyEffect = 3 * time.Second
	t.Cleanup(func() { nativeKeyEffect = previous })
	send := Service{StateDir: f.stateDir}
	long := strings.TrimSpace(strings.Repeat("rebase onto main and run the gate again ", 4))

	// Act
	err = send.SendNative(t.Context(), meta, long)
	next := send.SendNative(t.Context(), meta, "stop")

	// Assert
	if !errors.Is(err, ErrTakenBack) || !strings.Contains(err.Error(), "never showed in its composer") {
		t.Errorf("the long send = %v; want it never shown and taken back", err)
	}
	if errors.Is(next, ErrUnsentWords) {
		t.Errorf("the next send = %v; want it typed onto an empty prompt line", next)
	}
	events := f.events(t)
	if cleared := named(events, "cleared"); len(cleared) != 1 || !strings.HasPrefix(long, cleared[0].Text) {
		t.Errorf("cleared = %+v, want what the long send typed cleared once", cleared)
	}
	if submitted := named(events, "submitted"); len(submitted) == 0 || submitted[len(submitted)-1].Text != "stop" {
		t.Errorf("submitted = %+v, want the next send's words alone", submitted)
	}
}
