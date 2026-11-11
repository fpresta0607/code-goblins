package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// A new CEO's startup dialogs are watched for at most ceoSettle, and the
// watch ends once its screen shows a turn in progress, which is past them, or
// has shown no dialog it knows and not changed for ceoSettleQuiet, read every
// ceoSettlePoll.
var (
	ceoSettle      = 45 * time.Second
	ceoSettleQuiet = 3 * time.Second
	ceoSettlePoll  = 250 * time.Millisecond
)

// ceoScreen is a new CEO's terminal as settleCEO sees it: its screen, the
// answer to one of its dialogs, and the clock.
type ceoScreen struct {
	read   func() ([]string, error)
	answer func(dialog harness.Dialog, screen []string) error
	sleep  func(time.Duration)
	now    func() time.Time
}

// settleNativeCEO answers the startup dialogs of the CEO just started as name
// in native terminal ceo, and returns what to tell the Overlord about them.
func settleNativeCEO(ctx context.Context, stateDir, name string) []string {
	screens, ok := harness.NativeScreens(harness.Kind(name))
	if !ok {
		return nil
	}
	record, err := host.ReadRecord(stateDir, supervisor.NativeCEOTerminal)
	if err != nil {
		return nil
	}
	return settleCEO(name, screens, ceoScreen{
		read: func() ([]string, error) { return host.ReadScreen(record) },
		answer: func(dialog harness.Dialog, screen []string) error {
			return spawn.AnswerDialog(ctx, record, dialog, screen)
		},
		sleep: time.Sleep,
		now:   time.Now,
	})
}

// settleCEO answers each startup dialog the CEO's screen shows whose answer
// the harness's screens name as known and safe, as a goblin's spawn answers
// it. It is only for a CEO started in the Code Goblins home, the one folder
// whose trust is known and safe to give: Claude Code's trust in the home the
// quick start made, Codex's directory trust and update prompt, and Codex's
// hook review without trusting the hooks, which only the Overlord trusts. It
// stops at a screen it does not know, which it never types into, and at once
// at a screen that shows a turn in progress, as a CEO started with a first
// prompt shows once its dialogs are behind it. It returns a line for each
// dialog it answered and, for a dialog it may not have reached, what to
// choose there.
func settleCEO(name string, screens harness.Screens, terminal ceoScreen) []string {
	var notes, answered []string
	deadline := terminal.now().Add(ceoSettle)
	last, since := "", time.Time{}
	for terminal.now().Before(deadline) {
		screen, err := terminal.read()
		if err != nil {
			terminal.sleep(ceoSettlePoll)
			continue
		}
		if dialog, found := screens.Dialog(screen); found {
			if err := terminal.answer(dialog, screen); err != nil {
				return append(notes, fmt.Sprintf("The CEO's terminal shows %s, which could not be answered (%v): answer it there.", dialog.Name, err))
			}
			if dialog.EscapeHint != "" {
				notes = append(notes, fmt.Sprintf("Dismissed %s in the CEO's terminal with Escape.", dialog.Name))
			} else {
				notes = append(notes, fmt.Sprintf("Answered %s in the CEO's terminal: %s.", dialog.Name, dialog.Accept))
			}
			answered = append(answered, dialog.Name)
			last, since = "", time.Time{}
			continue
		}
		if screens.IsWorking(screen) {
			return append(notes, unreached(name, answered)...)
		}
		shown := strings.TrimSpace(strings.Join(screen, "\n"))
		switch {
		case shown == "":
		case shown != last:
			last, since = shown, terminal.now()
		case terminal.now().Sub(since) >= ceoSettleQuiet:
			return append(notes, unreached(name, answered)...)
		}
		terminal.sleep(ceoSettlePoll)
	}
	return append(notes, unreached(name, answered)...)
}

// unreached says what to choose at a dialog the harness may still show once
// its own first questions are answered.
func unreached(name string, answered []string) []string {
	switch {
	case name == "claude" && !slices.Contains(answered, "the workspace trust dialog"):
		return []string{"If Claude Code asks whether you trust this folder, its first choice, No, exits: move to Yes, I trust this folder, then press Enter."}
	case name == "codex" && !slices.Contains(answered, "the hook review prompt"):
		return []string{"If Codex asks you to review hooks, Continue without trusting keeps them off; trusting them is your own decision."}
	}
	return nil
}
