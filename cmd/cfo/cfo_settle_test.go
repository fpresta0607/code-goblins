package main

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
)

// scriptedCFO is a CFO's terminal that shows each screen in turn: a dialog
// screen until it is answered, any other until its time on screen passes.
type scriptedCFO struct {
	t        *testing.T
	screens  []scriptedScreen
	clock    time.Time
	answered []string
	fail     error
}

type scriptedScreen struct {
	rows []string
	// lasts is how long a screen that is not a dialog shows before the next.
	lasts time.Duration
}

func (s *scriptedCFO) terminal() cfoScreen {
	shownAt := s.clock
	return cfoScreen{
		read: func() ([]string, error) {
			for len(s.screens) > 1 && s.screens[0].lasts > 0 && s.clock.Sub(shownAt) >= s.screens[0].lasts {
				s.screens, shownAt = s.screens[1:], s.clock
			}
			return s.screens[0].rows, nil
		},
		answer: func(dialog harness.Dialog, _ []string) error {
			if s.fail != nil {
				return s.fail
			}
			s.answered = append(s.answered, dialog.Accept)
			s.screens, shownAt = s.screens[1:], s.clock
			return nil
		},
		sleep: func(d time.Duration) { s.clock = s.clock.Add(d) },
		now:   func() time.Time { return s.clock },
	}
}

var (
	claudeTrust  = []string{"Is this a project you created or one you trust?", "❯ 1. No, exit", "  2. Yes, I trust this folder"}
	codexTrust   = []string{"Do you trust the contents of this directory?", "› 1. Yes, continue", "  2. No, quit"}
	codexHooks   = []string{"Hooks need review", "2 hooks are new or changed", "› 1. Review hooks", "  2. Trust all and continue", "  3. Continue without trusting"}
	claudeWaits  = []string{"> ", "? for shortcuts"}
	themePicker  = []string{"Let's get started.", "Choose the text style that looks best with your terminal", "> 1. Dark mode"}
	settleScreen = func(t *testing.T, kind harness.Kind) harness.Screens {
		t.Helper()
		screens, ok := harness.NativeScreens(kind)
		if !ok {
			t.Fatalf("no native screens for %s", kind)
		}
		return screens
	}
)

// A new CFO's startup dialogs whose answers are known and safe are answered
// with the answer a goblin's spawn gives, and said; a screen the quick start
// does not know is never typed into, and what to choose at a dialog the CFO
// may still show is said instead.
func TestANewCFOsKnownStartupDialogsAreAnsweredAndOthersLeftToThePerson(t *testing.T) {
	for _, c := range []struct {
		name     string
		harness  string
		screens  []scriptedScreen
		answered []string
		notes    []string
	}{
		{
			"Claude Code's trust dialog, then its composer", "claude",
			[]scriptedScreen{{rows: claudeTrust}, {rows: claudeWaits}},
			[]string{"Yes, I trust this folder"},
			[]string{"Answered the workspace trust dialog in the CFO's terminal: Yes, I trust this folder."},
		},
		{
			"Claude Code's own first-run screen", "claude",
			[]scriptedScreen{{rows: themePicker}},
			nil,
			[]string{"If Claude Code asks whether you trust this folder, its first choice, No, exits: move to Yes, I trust this folder, then press Enter."},
		},
		{
			"Codex's directory trust and hook review", "codex",
			[]scriptedScreen{{rows: codexTrust}, {rows: nil, lasts: time.Second}, {rows: codexHooks}, {rows: []string{"› Ask Codex to do anything"}}},
			[]string{"1. Yes, continue", "3. Continue without trusting"},
			[]string{"Answered the directory trust prompt in the CFO's terminal: 1. Yes, continue.", "Answered the hook review prompt in the CFO's terminal: 3. Continue without trusting."},
		},
		{
			"a terminal that shows nothing", "codex",
			[]scriptedScreen{{rows: nil}},
			nil,
			[]string{"If Codex asks you to review hooks, Continue without trusting keeps them off; trusting them is your own decision."},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			terminal := &scriptedCFO{t: t, screens: c.screens, clock: time.Unix(0, 0)}
			started := terminal.clock

			// Act
			notes := settleCFO(c.harness, settleScreen(t, harness.Kind(c.harness)), terminal.terminal())

			// Assert
			if !slices.Equal(terminal.answered, c.answered) {
				t.Errorf("answered %q, want %q", terminal.answered, c.answered)
			}
			if !slices.Equal(notes, c.notes) {
				t.Errorf("notes %q, want %q", notes, c.notes)
			}
			if took := terminal.clock.Sub(started); took > cfoSettle {
				t.Errorf("the watch took %s, more than its bound %s", took, cfoSettle)
			}
		})
	}
}

// A screen that is not a dialog ends the watch once it has stayed unchanged
// for the quiet window, well before the bound.
func TestTheWatchEndsOnceTheScreenSettles(t *testing.T) {
	// Arrange
	terminal := &scriptedCFO{t: t, screens: []scriptedScreen{{rows: claudeWaits}}, clock: time.Unix(0, 0)}

	// Act
	settleCFO("claude", settleScreen(t, harness.Claude), terminal.terminal())

	// Assert
	if took := terminal.clock.Sub(time.Unix(0, 0)); took > cfoSettleQuiet+time.Second {
		t.Errorf("the watch took %s on a settled screen, want about %s", took, cfoSettleQuiet)
	}
}

// A CFO started with a first prompt redraws its working line all through its
// first turn, so its screen never settles: a turn in progress is past the
// startup dialogs and ends the watch at once, with what to choose at a dialog
// it may still show.
func TestTheWatchEndsOnceTheHarnessIsWorking(t *testing.T) {
	// Arrange
	terminal := &scriptedCFO{t: t, clock: time.Unix(0, 0)}
	for poll := range int(cfoSettle/cfoSettlePoll) + 1 {
		terminal.screens = append(terminal.screens, scriptedScreen{
			rows:  []string{fmt.Sprintf("• Working (%d • esc to interrupt)", poll), "› Ask Codex to do anything"},
			lasts: cfoSettlePoll,
		})
	}

	// Act
	notes := settleCFO("codex", settleScreen(t, harness.Codex), terminal.terminal())

	// Assert
	if took := terminal.clock.Sub(time.Unix(0, 0)); took != 0 {
		t.Errorf("the watch took %s on a screen showing a turn in progress, want it to end at once", took)
	}
	if len(terminal.answered) != 0 {
		t.Errorf("answered %q on a screen showing no dialog", terminal.answered)
	}
	want := []string{"If Codex asks you to review hooks, Continue without trusting keeps them off; trusting them is your own decision."}
	if !slices.Equal(notes, want) {
		t.Errorf("notes %q, want %q", notes, want)
	}
}

// A dialog shown after a screen that is not yet a turn in progress is still
// answered, and the turn that follows it ends the watch.
func TestADialogShownBeforeTheHarnessIsWorkingIsStillAnswered(t *testing.T) {
	// Arrange
	terminal := &scriptedCFO{t: t, clock: time.Unix(0, 0), screens: []scriptedScreen{
		{rows: []string{"  Starting Codex"}, lasts: time.Second},
		{rows: codexHooks},
		{rows: []string{"• Working (0s • esc to interrupt)", "› Ask Codex to do anything"}},
	}}

	// Act
	notes := settleCFO("codex", settleScreen(t, harness.Codex), terminal.terminal())

	// Assert
	if want := []string{"3. Continue without trusting"}; !slices.Equal(terminal.answered, want) {
		t.Errorf("answered %q, want %q", terminal.answered, want)
	}
	if want := []string{"Answered the hook review prompt in the CFO's terminal: 3. Continue without trusting."}; !slices.Equal(notes, want) {
		t.Errorf("notes %q, want %q", notes, want)
	}
	if took := terminal.clock.Sub(time.Unix(0, 0)); took > cfoSettleQuiet {
		t.Errorf("the watch took %s, want it to end once the turn shows, before the quiet window %s", took, cfoSettleQuiet)
	}
}

// A dialog that cannot be answered stops the watch and says so, naming it.
func TestADialogThatCannotBeAnsweredIsLeftToThePerson(t *testing.T) {
	// Arrange
	terminal := &scriptedCFO{t: t, screens: []scriptedScreen{{rows: claudeTrust}}, clock: time.Unix(0, 0), fail: errors.New("the focus never moved")}

	// Act
	notes := settleCFO("claude", settleScreen(t, harness.Claude), terminal.terminal())

	// Assert
	want := []string{"The CFO's terminal shows the workspace trust dialog, which could not be answered (the focus never moved): answer it there."}
	if !slices.Equal(notes, want) {
		t.Errorf("notes %q, want %q", notes, want)
	}
}
