package spawn

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// lateHookComposer opens the review at a key boundary, after launch has
// observed the settled composer. It reads raw runes so Daybreak takes Escape.
func lateHookComposer(mode string, keys <-chan string, record func(codexEvent), draw func(...string), composer func(string)) {
	hooks := []string{"1. Review hooks", "2. Trust all and continue", "3. Continue without trusting (hooks won't run)"}
	line, dialog, sequence := "", "", ""
	hasOpened, isWorking := false, strings.Contains(mode, "busy")
	focus := 0
	show := func() {
		switch dialog {
		case "hooks":
			rows := append([]string{"Hooks need review", "2 hooks are new or changed", ""}, focusRows(hooks, focus)...)
			footer := "enter confirm / esc skip"
			if strings.Contains(mode, "legacy") {
				footer = "Press enter to confirm or esc to go back"
			}
			if strings.Contains(mode, "middle-dot") {
				rows[1] = "1 hook is new or changed"
				footer = "enter confirm · esc skip"
			}
			draw(append(rows, footer)...)
		case "daybreak":
			draw("Set up security for Daybreak mode", "\u203a 1. Set up security", "Press a number to choose \u00b7 esc to dismiss \u00b7 type to continue", "", "\u203a "+line, "100% context left")
		default:
			if isWorking {
				text := line
				if text == "" {
					text = "Ask Codex to do anything"
				}
				draw("\u2022 Working (0s \u2022 esc to interrupt)", "", "\u203a "+text, "", "100% context left")
			} else if line == "" {
				composer("Ask Codex to do anything")
			} else {
				composer(strings.ReplaceAll(line, "\n", "\r\n"))
			}
		}
	}
	show()
	for key := range keys {
		if dialog == "daybreak" {
			if key == "\x1b" {
				record(codexEvent{Event: "security dismissed", Text: key})
				dialog = ""
			} else {
				record(codexEvent{Event: "typed into security offer", Text: key})
			}
			show()
			continue
		}
		isEscape := dialog == "hooks" && !strings.Contains(mode, "legacy") && key == "\x1b"
		if !isEscape && (sequence != "" || key == "\x1b") {
			sequence += key
			if sequence == "\x1b" || sequence == "\x1b[" {
				continue
			}
			key, sequence = sequence, ""
		}
		if !hasOpened && (key == "\x0c" || strings.Contains(mode, "during-typing") || key == "\r" && strings.Contains(mode, "after-submit")) {
			dialog, hasOpened = "hooks", true
			if strings.Contains(mode, "partial") {
				runes := []rune(line)
				line = string(runes[max(0, len(runes)-40):])
			}
			if strings.Contains(mode, "extra-footer") {
				footer := "100% context left"
				if strings.Contains(mode, "model") {
					footer = "GPT-6-Astra xhigh \u00b7 D:/x"
				}
				line += "\n\n" + footer + "\nextra retained instruction"
			}
			record(codexEvent{Event: "late hook review", Text: line})
			show()
			continue
		}
		if dialog == "hooks" {
			switch key {
			case "\x1b[B":
				if strings.Contains(mode, "stuck") || strings.Contains(mode, "lost-key") {
					record(codexEvent{Event: "lost", Text: key})
				} else {
					focus = min(focus+1, 2)
				}
			case "\r", "\x1b":
				if key == "\x1b" {
					if strings.Contains(mode, "stuck") {
						record(codexEvent{Event: "lost", Text: key})
						continue
					}
					record(codexEvent{Event: "hooks skipped", Text: key})
					record(codexEvent{Event: "hook prompt", Text: hooks[2]})
				} else {
					record(codexEvent{Event: "hook prompt", Text: hooks[focus]})
					if focus != 2 {
						return
					}
				}
				dialog = ""
				if strings.Contains(mode, "daybreak") {
					dialog = "daybreak"
				}
				if strings.Contains(mode, "after-submit") {
					record(codexEvent{Event: "submitted", Text: line})
					isWorking = true
				}
			default:
				record(codexEvent{Event: "typed into a list", Text: key})
			}
			show()
			continue
		}
		switch key {
		case "\r":
			if strings.Contains(mode, "busy") {
				recordQueuedPrompt(line, record)
				line = ""
			} else {
				record(codexEvent{Event: "submitted", Text: line})
			}
			isWorking = true
		case "\x1b[F":
		default:
			line += key
		}
		if !hasOpened && strings.Contains(mode, "busy") && strings.HasSuffix(line, "instruction from the CFO.") {
			dialog, hasOpened = "hooks", true
			record(codexEvent{Event: "late hook review", Text: line})
		}
		show()
	}
}

func TestNativeSpawnRecoversALateDialogAndSubmitsOnce(t *testing.T) {
	for _, mode := range []string{"hooks-during-typing", "hooks-during-typing-lost-key", "hooks-during-typing-legacy", "hooks-during-typing-legacy-middle-dot", "hooks-before-submit", "hooks-before-submit-daybreak", "hooks-before-submit-partial", "hooks-before-submit-partial-old-placeholder", "hooks-before-submit-extra-footer", "hooks-before-submit-extra-footer-model", "hooks-after-submit"} {
		t.Run(mode, func(t *testing.T) {
			// Arrange
			previousKey, previousPace := nativeKeyEffect, nativeTypedPace
			nativeKeyEffect, nativeTypedPace = 3*time.Second, 0
			t.Cleanup(func() { nativeKeyEffect, nativeTypedPace = previousKey, previousPace })
			f := newNativeFixture(t, harness.Codex, mode)
			if strings.Contains(mode, "before-submit") {
				hasOpened := false
				f.service.Sleep = func(ctx context.Context, duration time.Duration) error {
					if duration == launchSettle && !hasOpened {
						hasOpened = true
						terminal, err := host.ReadRecord(f.stateDir, f.request.ID)
						if err != nil {
							return err
						}
						client, err := host.Dial(terminal)
						if err != nil {
							return err
						}
						defer client.Close()
						if err := client.Input([]byte("\x0c")); err != nil {
							return err
						}
						_, err = (Service{}).awaitScreen(ctx, terminal, 3*time.Second, func(screen []string) bool {
							return strings.Contains(strings.Join(screen, "\n"), "Hooks need review")
						})
						return err
					}
					return (Service{}).sleep(ctx, duration)
				}
			}
			if strings.Contains(mode, "old-placeholder") {
				f.service.ReadScreen = func(record host.Record) ([]string, error) {
					screen, err := host.ReadScreen(record)
					if err == nil && strings.Contains(strings.Join(screen, "\n"), "100% context left") {
						screen = append([]string{"\u203a Ask Codex to do anything", "\u2022 Earlier quoted composer", ""}, screen...)
					}
					return screen, err
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
			defer cancel()

			// Act
			_, err := f.service.Spawn(ctx, f.request)

			// Assert
			isIncomplete := strings.Contains(mode, "partial") || strings.Contains(mode, "extra-footer")
			if (err != nil) != isIncomplete {
				t.Errorf("Spawn: %v, want failure %v", err, isIncomplete)
			}
			events := f.events(t)
			if answers := named(events, "hook prompt"); len(answers) != 1 || !strings.HasPrefix(answers[0].Text, "3. Continue without trusting") {
				t.Errorf("hook answers = %+v, want continue without trusting once", answers)
			}
			wantEscapes := 1
			if strings.Contains(mode, "legacy") {
				wantEscapes = 0
			}
			if skipped := named(events, "hooks skipped"); len(skipped) != wantEscapes {
				t.Errorf("hook skips = %+v, want %d", skipped, wantEscapes)
			}
			submitted := named(events, "submitted")
			wantSubmissions := 1
			if isIncomplete {
				wantSubmissions = 0
			}
			if len(submitted) != wantSubmissions || len(submitted) == 1 && submitted[0].Text != spawnPointer(t, f.fixture) {
				t.Errorf("submissions = %+v, want %d complete instructions", submitted, wantSubmissions)
			}
			if strings.Contains(mode, "daybreak") {
				if got := named(events, "security dismissed"); len(got) != 1 {
					t.Errorf("dismissals = %+v", got)
				}
				if got := named(events, "typed into security offer"); len(got) != 0 {
					t.Errorf("keys in Daybreak = %+v", got)
				}
			}
			if _, err := state.ReadTaskMeta(f.stateDir, f.request.ID); (err != nil) != isIncomplete {
				t.Errorf("task not retained: %v", err)
			}
			pending, err := wake.Pending(f.stateDir)
			if err != nil || !slices.ContainsFunc(pending, func(record wake.Record) bool {
				return record.Kind == "notify" && strings.Contains(record.Detail, "without trusting its hooks")
			}) {
				t.Errorf("untrusted-hook notification = %+v, %v", pending, err)
			}
		})
	}
}

func TestNativeResumeKeepsItsWorkWhenALateDialogCannotRecover(t *testing.T) {
	for _, mode := range []string{"hooks-during-typing-lost-key-daybreak", "hooks-during-typing-stuck"} {
		t.Run(mode, func(t *testing.T) {
			// Arrange
			previousKey, previousPace := nativeKeyEffect, nativeTypedPace
			nativeKeyEffect, nativeTypedPace = 3*time.Second, 0
			t.Cleanup(func() { nativeKeyEffect, nativeTypedPace = previousKey, previousPace })
			f := newQuickFixture(t)
			f.service.Commands = dirtyWorktree{f.service.Worktrees.Commands}
			closeCurrentTerminal(t, f)
			if _, err := f.service.Spawn(t.Context(), f.request); err != nil {
				t.Fatal(err)
			}
			first, err := host.ReadRecord(f.stateDir, f.request.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := host.Close(f.stateDir, first, nativeCloseWait); err != nil {
				t.Fatal(err)
			}
			before, err := state.ReadTaskMeta(f.stateDir, f.request.ID)
			if err != nil {
				t.Fatal(err)
			}
			unfinished := filepath.Join(before.Worktree, "unfinished.txt")
			if err := os.WriteFile(unfinished, []byte("preserve this work"), 0o600); err != nil {
				t.Fatal(err)
			}
			f.userEnv = append(f.userEnv, fakeCodexMode+"="+mode)
			ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
			defer cancel()
			paused := state.Lifecycle{ID: before.ID, Generation: before.SpawnGen, RequestGeneration: before.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused", Session: "owned-resume-session", Started: time.Now().UTC()}
			if err := state.WriteLifecycle(f.stateDir, paused); err != nil {
				t.Fatal(err)
			}
			controller := lifecycle.Service{StateDir: f.stateDir, Operations: lifecycle.Operations{
				Memory: func() (uint64, uint64, error) { return 5 << 30, 5 << 30, nil },
				Resume: func(ctx context.Context, meta state.TaskMeta, prior state.Lifecycle) error {
					_, err := f.service.Switch(ctx, SwitchRequest{ID: meta.ID, Generation: meta.SpawnGen, IsResume: true, ResumeSession: prior.Session})
					return err
				},
				Notify: func(state.Lifecycle) error { return nil },
			}}

			// Act
			result, err := controller.Run(ctx, lifecycle.Request{ID: before.ID, Generation: before.SpawnGen, Operation: "resume-1", Action: "resume"})

			// Assert
			isStuck := strings.Contains(mode, "stuck")
			if (err != nil) != isStuck {
				t.Errorf("Switch = %v, want failure %v", err, isStuck)
			}
			wantPhase := "running"
			if isStuck {
				wantPhase = "failed"
			}
			if result.Phase != wantPhase {
				t.Errorf("resume phase = %s, want %s", result.Phase, wantPhase)
			}
			events := f.events(t)
			submissions := named(events, "submitted")
			if isStuck {
				if len(submissions) != 1 {
					t.Errorf("stuck review received a submission: %+v", submissions)
				}
				if answers := named(events, "hook prompt"); len(answers) != 0 {
					t.Errorf("stuck review was answered: %+v", answers)
				}
				if losses := named(events, "lost"); len(losses) != 1 || losses[0].Text != "\x1b" {
					t.Errorf("lost keys = %+v, want one Escape with no blind retry", losses)
				}
			} else {
				if len(submissions) != 2 || submissions[1].Text != spawnPointer(t, f.fixture) {
					t.Errorf("recovery submissions = %+v", submissions)
				}
				if answers := named(events, "hook prompt"); len(answers) != 1 || !strings.HasPrefix(answers[0].Text, "3. Continue without trusting") {
					t.Errorf("hook answers = %+v", answers)
				}
				if dismissals := named(events, "security dismissed"); len(dismissals) != 1 {
					t.Errorf("Daybreak dismissals = %+v", dismissals)
				}
			}
			after, readErr := state.ReadTaskMeta(f.stateDir, f.request.ID)
			if readErr != nil || after.SpawnGen == before.SpawnGen {
				t.Errorf("replacement task = %+v, %v", after, readErr)
			}
			if work, err := os.ReadFile(unfinished); err != nil || string(work) != "preserve this work" {
				t.Errorf("unfinished work = %q, %v", work, err)
			}
			if launches := named(events, "env"); len(launches) != 2 {
				t.Errorf("launches = %+v, want one replacement only", launches)
			}
			if !nativeTerminalRuns(f.stateDir, f.request.ID) {
				t.Error("resume closed its live terminal")
			}
		})
	}
}

func TestNativeDeliveryDoesNotAnswerDialogTextInTheConversation(t *testing.T) {
	for _, delivery := range []string{"spawn", "send during a turn", "send at idle", "send at idle with quoted Daybreak", "send at idle with quoted folder trust", "send at idle with quoted directory trust"} {
		t.Run(delivery, func(t *testing.T) {
			previousKey, previousSettle := nativeKeyEffect, nativeDialogSettle
			nativeKeyEffect, nativeDialogSettle = time.Second, 0
			t.Cleanup(func() { nativeKeyEffect, nativeDialogSettle = previousKey, previousSettle })
			f := newQuickFixture(t)
			if strings.HasPrefix(delivery, "send at idle") {
				f.userEnv = append(f.userEnv, fakeCodexMode+"=turns")
			}
			if delivery != "spawn" {
				if _, err := f.service.Spawn(t.Context(), f.request); err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(delivery, "send at idle") {
					awaitComposer(t, f)
				}
			}
			screens, _ := harness.NativeScreens(harness.Codex)
			quote := []string{"Hooks need review", "2 hooks are new or changed", "1. Review hooks", "2. Trust all and continue", "3. Continue without trusting", "enter confirm / esc skip", ""}
			if strings.Contains(delivery, "Daybreak") {
				quote = []string{"Set up security for Daybreak mode", "Press a number to choose \u00b7 esc to dismiss \u00b7 type to continue", ""}
			} else if strings.Contains(delivery, "folder trust") {
				quote = []string{"Trust this folder?", "› 1. Trust and continue", "  2. Quit", "", "enter continue · esc quit", ""}
			} else if strings.Contains(delivery, "directory trust") {
				quote = []string{"Do you trust the contents of this directory?", "› 1. Yes, continue", "  2. No, quit", "", "Press enter to continue", ""}
			}
			f.service.ReadScreen = func(record host.Record) ([]string, error) {
				screen, err := host.ReadScreen(record)
				if err == nil && (delivery != "spawn" || screens.IsWorking(screen)) {
					screen = append(append([]string{}, quote...), screen...)
				}
				return screen, err
			}
			f.service.Took = func(context.Context, state.TaskMeta, string, string, time.Time) bool {
				return len(named(f.events(t), "submitted")) == 2
			}
			var err error
			wantSubmissions := 1
			if delivery == "spawn" {
				_, err = f.service.Spawn(t.Context(), f.request)
			} else {
				meta, readErr := state.ReadTaskMeta(f.stateDir, f.request.ID)
				if readErr != nil {
					t.Fatal(readErr)
				}
				err = f.service.SendNative(t.Context(), meta, "Continue the unfinished work.")
				wantSubmissions = 2
			}

			if err != nil {
				t.Errorf("delivery = %v; conversation text is not a startup prompt", err)
			}
			if submitted := named(f.events(t), "submitted"); len(submitted) != wantSubmissions {
				t.Errorf("submitted = %+v, want %d", submitted, wantSubmissions)
			}
		})
	}
}

func TestNativeRecoveryDoesNotChooseATrustOptionQuotedInADraft(t *testing.T) {
	for _, menu := range []struct{ title, accept string }{
		{"Trust this folder?", "1. Trust and continue"},
		{"Do you trust the contents of this directory?", "1. Yes, continue"},
	} {
		t.Run(menu.title, func(t *testing.T) {
			previousKey, previousSettle := nativeKeyEffect, nativeDialogSettle
			nativeKeyEffect, nativeDialogSettle = time.Second, 0
			t.Cleanup(func() { nativeKeyEffect, nativeDialogSettle = previousKey, previousSettle })
			f := newQuickFixture(t)
			if _, err := f.service.Spawn(t.Context(), f.request); err != nil {
				t.Fatal(err)
			}
			terminal, err := host.ReadRecord(f.stateDir, f.request.ID)
			if err != nil {
				t.Fatal(err)
			}
			client, err := host.Dial(terminal)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			draft := menu.accept + " with more retained text"
			if err := client.Input([]byte(draft)); err != nil {
				t.Fatal(err)
			}
			screens, _ := harness.NativeScreens(harness.Codex)
			if _, err := f.service.awaitScreen(t.Context(), terminal, nativeKeyEffect, func(rows []string) bool {
				return screens.ComposerMatches(rows, draft)
			}); err != nil {
				t.Fatal(err)
			}
			f.service.ReadScreen = func(record host.Record) ([]string, error) {
				rows, err := host.ReadScreen(record)
				return append([]string{menu.title, ""}, rows...), err
			}

			_, _, readyErr := f.service.awaitNativeReady(t.Context(), terminal, screens)
			if readyErr != nil {
				t.Errorf("quoted title blocks the current composer: %v", readyErr)
			} else if _, err := f.service.submitNative(t.Context(), terminal, screens, "Continue the unfinished work.", 0, true); err == nil {
				t.Error("the unrelated retained draft was submitted")
			}
			if submitted := named(f.events(t), "submitted"); len(submitted) != 1 {
				t.Errorf("submissions = %+v; want only the initial instruction, no trust choice or retained draft", submitted)
			}
		})
	}
}

func TestNativeRecoveryDoesNotSubmitAHookOptionQuotedInADraft(t *testing.T) {
	for _, footer := range []string{"", "Press enter to confirm or esc to go back", "enter confirm · esc skip"} {
		t.Run(footer, func(t *testing.T) {
			// Arrange
			previousKey, previousSettle := nativeKeyEffect, nativeDialogSettle
			nativeKeyEffect, nativeDialogSettle = time.Second, 0
			t.Cleanup(func() { nativeKeyEffect, nativeDialogSettle = previousKey, previousSettle })
			f := newQuickFixture(t)
			if _, err := f.service.Spawn(t.Context(), f.request); err != nil {
				t.Fatal(err)
			}
			terminal, err := host.ReadRecord(f.stateDir, f.request.ID)
			if err != nil {
				t.Fatal(err)
			}
			client, err := host.Dial(terminal)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			draft := "3. Continue without trusting with more retained text"
			if err := client.Input([]byte(draft)); err != nil {
				t.Fatal(err)
			}
			screens, _ := harness.NativeScreens(harness.Codex)
			if _, err := f.service.awaitScreen(t.Context(), terminal, nativeKeyEffect, func(rows []string) bool {
				return screens.ComposerMatches(rows, draft)
			}); err != nil {
				t.Fatal(err)
			}
			history := []string{"Hooks need review"}
			if footer != "" {
				history = append(history, "1 hook is new or changed", "1. Review hooks", "2. Trust all and continue", "3. Continue without trusting (hooks won't run)", "", footer)
			}
			f.service.ReadScreen = func(record host.Record) ([]string, error) {
				rows, err := host.ReadScreen(record)
				for index, row := range rows {
					if strings.Contains(row, "100% context left") {
						rows[index] = "gpt-6.1-sol high"
					}
				}
				return append(append([]string{}, history...), rows...), err
			}
			rows, err := f.service.ReadScreen(terminal)
			if err != nil {
				t.Fatal(err)
			}

			// Act: startup answers a recognized dialog before checking the draft.
			if dialog, isShown := screens.Dialog(rows); isShown {
				t.Logf("quoted history recognized as %s; answer returned %v", dialog.Name, f.service.answerDialog(t.Context(), terminal, dialog, rows))
			}
			_, submitErr := f.service.submitNative(t.Context(), terminal, screens, "Continue the unfinished work.", 0, true)

			// Assert
			if submitErr == nil {
				t.Error("the unrelated retained draft was accepted for submission")
			}
			if submitted := named(f.events(t), "submitted"); len(submitted) != 1 {
				t.Errorf("submissions = %+v; want only the initial instruction, no hook choice or retained draft", submitted)
			}
			rows, err = f.service.ReadScreen(terminal)
			if err != nil || !screens.ComposerMatches(rows, draft) {
				t.Errorf("retained draft changed or is unreadable: %v; screen ends:\n%s", err, host.ScreenTail(rows, 8))
			}
		})
	}
}
