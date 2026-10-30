package spawn

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A native goblin takes a steer the way it took its instruction: typed,
// submitted once its composer shows it, and delivered once it works on it. A
// key reaches its terminal as the console reads it, and a key cfo send does
// not name is refused before anything is sent.
func TestANativeGoblinTakesTextAndKeysThroughItsTerminal(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "turns")
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	awaitComposer(t, f)

	textErr := f.service.SendNative(context.Background(), meta, "CFO: run the tests")
	keyErr := f.service.SendNativeKey(meta, "Enter")
	unknownErr := f.service.SendNativeKey(meta, "F5")

	if textErr != nil {
		t.Errorf("SendNative: %v", textErr)
	}
	if keyErr != nil {
		t.Errorf("SendNativeKey Enter: %v", keyErr)
	}
	if unknownErr == nil || !strings.Contains(unknownErr.Error(), "unsupported key") {
		t.Errorf("SendNativeKey F5 = %v, want it refused", unknownErr)
	}
	want := []string{spawnPointer(t, f.fixture), "CFO: run the tests", ""}
	var submitted []string
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		submitted = submitted[:0]
		for _, event := range named(f.events(t), "submitted") {
			submitted = append(submitted, event.Text)
		}
		if len(submitted) >= len(want) {
			break
		}
	}
	if !slices.Equal(submitted, want) {
		t.Errorf("submitted = %q, want the instruction, the steer, then an empty line from Enter", submitted)
	}
}

// A slash command reaches a native goblin's harness once, submitted only after
// the wait a completion popup needs, and is reported unconfirmed without
// waiting for a working screen: /exit ends the harness, so nothing afterwards
// can show it ran.
func TestANativeGoblinTakesASlashCommandOnceAndReportsItUnconfirmed(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "")
	previous := nativeReadGrace
	nativeReadGrace = 2 * time.Minute
	t.Cleanup(func() { nativeReadGrace = previous })
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	var slept []time.Duration
	f.service.Sleep = func(ctx context.Context, duration time.Duration) error {
		slept = append(slept, duration)
		timer := time.NewTimer(duration) // stall-accepted: it sleeps for the send's own pauses, which are meant to run out.
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}

	began := time.Now()
	err = f.service.SendNative(context.Background(), meta, "/exit")
	took := time.Since(began)

	if err == nil || !strings.Contains(err.Error(), "submitted once") || !strings.Contains(err.Error(), "cfo peek task-7") {
		t.Errorf("SendNative /exit = %v, want it reported submitted once but unconfirmed", err)
	}
	if !slices.ContainsFunc(slept, func(duration time.Duration) bool { return duration >= 1200*time.Millisecond }) {
		t.Errorf("waits = %v, want one of at least 1.2s before Enter", slept)
	}
	if took >= time.Minute {
		t.Errorf("SendNative took %s, want it back without waiting on the ended harness's screen", took)
	}
	want := []string{spawnPointer(t, f.fixture), "/exit"}
	var submitted []string
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		submitted = submitted[:0]
		for _, event := range named(f.events(t), "submitted") {
			submitted = append(submitted, event.Text)
		}
		if len(submitted) >= len(want) {
			break
		}
	}
	if !slices.Equal(submitted, want) {
		t.Errorf("submitted = %q, want the instruction, then /exit once", submitted)
	}
}

// A native task whose terminal has ended takes nothing, and says which task.
// Text sent to it says nothing was typed, so a sender may send it again once
// the terminal is back, as for a goblin paused or between sessions.
func TestANativeTaskWithNoTerminalTakesNothing(t *testing.T) {
	service := Service{StateDir: t.TempDir()}
	meta := state.TaskMeta{ID: "task-7", Window: "native", Harness: string(harness.Codex), Backend: "native"}

	textErr := service.SendNative(context.Background(), meta, "CFO: run the tests")
	keyErr := service.SendNativeKey(meta, "Enter")

	for name, err := range map[string]error{"text": textErr, "key": keyErr} {
		if err == nil || !strings.Contains(err.Error(), "native task task-7 has no running terminal") {
			t.Errorf("%s = %v, want it refused naming the task", name, err)
		}
	}
	if !errors.Is(textErr, fleet.ErrNotTyped) {
		t.Errorf("text = %v, want it to say nothing was typed", textErr)
	}
}

func TestNativeDeliveryDoesNotTypeIntoAnOpenCodexPicker(t *testing.T) {
	f := newQuickFixture(t)
	if _, err := f.service.Spawn(t.Context(), f.request); err != nil {
		t.Fatal(err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	before := len(named(f.events(t), "submitted"))
	f.service.ReadScreen = func(host.Record) ([]string, error) {
		return []string{"Select a model", "› 1. Current", "  2. Another model", "Enter to confirm · Esc to exit"}, nil
	}
	err = f.service.SendNative(t.Context(), meta, "do not select a model")
	if !errors.Is(err, fleet.ErrNotTyped) || !strings.Contains(err.Error(), "waiting on its own screen") {
		t.Fatalf("delivery = %v, want the picker to refuse input", err)
	}
	if after := len(named(f.events(t), "submitted")); after != before {
		t.Fatalf("submitted %d messages after %d before, want no picker input", after, before)
	}
}

// A delivery onto a prompt line holding words that were never sent, such as
// ones he typed in the terminal itself, types nothing: what it typed would
// join them, and its Enter would send both as one message (the Overlord,
// 2026-10-28: "it's sending and appending messages together that were sent
// previously").
func TestADeliveryTypesNothingOntoAPromptLineHoldingWords(t *testing.T) {
	for _, text := range []string{"CFO: run the tests", "/compact"} {
		t.Run(text, func(t *testing.T) {
			// Arrange: Codex idle at its prompt, an unsent draft on the line.
			f := newQuickFixture(t)
			if _, err := f.service.Spawn(t.Context(), f.request); err != nil {
				t.Fatal(err)
			}
			meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
			if err != nil {
				t.Fatal(err)
			}
			before := len(named(f.events(t), "submitted"))
			f.service.ReadScreen = func(host.Record) ([]string, error) {
				return []string{"• ok", "› An unfinished user draft", "100% context left"}, nil
			}

			// Act
			err = f.service.SendNative(t.Context(), meta, text)

			// Assert
			if !errors.Is(err, fleet.ErrNotTyped) || !errors.Is(err, ErrUnsentWords) {
				t.Fatalf("SendNative = %v, want it refused with nothing typed onto the draft", err)
			}
			if after := len(named(f.events(t), "submitted")); after != before {
				t.Fatalf("submitted %d messages after %d before, want nothing submitted with the draft", after, before)
			}
		})
	}
}

// Text typed into a harness already in a turn is queued by the harness, so
// with nothing showing the harness took it the send says it is queued, where
// it once reported it delivered because the screen already showed the harness
// working.
func TestASteerToANativeGoblinInATurnIsQueuedWithoutAHookReport(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "")
	nativeQueuedProof = 500 * time.Millisecond
	t.Cleanup(func() { nativeQueuedProof = 5 * time.Second })
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}

	err = f.service.SendNative(context.Background(), meta, "CFO: run the tests")

	if !errors.Is(err, fleet.ErrQueuedForToolCall) {
		t.Errorf("SendNative = %v, want it queued for the next tool call", err)
	}
	if submitted := submittedLines(t, f, 2); !slices.Equal(submitted, []string{spawnPointer(t, f.fixture), "CFO: run the tests"}) {
		t.Errorf("submitted = %q, want the steer submitted once after the instruction", submitted)
	}
}

// A delivery that refuses a harness in a turn, as the supervisor's wake line
// and a board message held for the turn do, types nothing into one, where it
// once submitted the text into the turn and reported it queued for the next
// tool call (#1468). A command he sends is refused the same way.
func TestADeliveryThatRefusesATurnTypesNothingIntoOne(t *testing.T) {
	for _, text := range []string{"cfo watcher wake: 1 queued wake", "/compact"} {
		t.Run(text, func(t *testing.T) {
			f := newNativeFixture(t, harness.Codex, "")
			if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
			if err != nil {
				t.Fatal(err)
			}
			f.service.ShouldRefuseTurn = true

			err = f.service.SendNative(context.Background(), meta, text)

			if !errors.Is(err, ErrInTurn) || !errors.Is(err, fleet.ErrNotTyped) {
				t.Fatalf("SendNative = %v, want it refused as in a turn with nothing typed", err)
			}
			if submitted := submittedLines(t, f, 1); !slices.Equal(submitted, []string{spawnPointer(t, f.fixture)}) {
				t.Errorf("submitted = %q, want only the spawn's instruction", submitted)
			}
		})
	}
}

// A board message he asked for now reaches a Claude Code in a turn the way
// its terminal sends one now: typed and queued, then Ctrl+X Ctrl+S once its
// composer shows the line gone into that queue, so it takes the line at once
// (the Overlord, 2026-10-30: "when sent now it actually sends now same way
// termianl expereince down with ctrl x crtl s"). Without that ask the line
// waits in the queue for the harness's next tool call.
func TestSendNowHasAClaudeCodeInATurnTakeTheLineAtOnce(t *testing.T) {
	for _, test := range []struct {
		name          string
		shouldSendNow bool
		wantSentNow   []string
	}{
		{name: "sent now", shouldSendNow: true, wantSentNow: []string{"use the staging database"}},
		{name: "queued", shouldSendNow: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newNativeFixture(t, harness.Claude, "claude-queues")
			nativeQueuedProof = 500 * time.Millisecond
			t.Cleanup(func() { nativeQueuedProof = 5 * time.Second })
			if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
			if err != nil {
				t.Fatal(err)
			}
			f.service.ShouldSendNow = test.shouldSendNow

			// Act
			err = f.service.SendNative(t.Context(), meta, "use the staging database")

			// Assert
			if !errors.Is(err, fleet.ErrQueuedForToolCall) {
				t.Fatalf("SendNative = %v, want it typed into the turn with nothing yet proving it taken", err)
			}
			events := f.events(t)
			if queued := named(events, "queued"); len(queued) != 1 || queued[0].Text != "use the staging database" {
				t.Errorf("queued = %+v, want the line queued once", queued)
			}
			if sentNow := sentNowLines(t, f, len(test.wantSentNow)); !slices.Equal(sentNow, test.wantSentNow) {
				t.Errorf("sent now = %q, want %q", sentNow, test.wantSentNow)
			}
		})
	}
}

// A message already typed into a Claude Code in a turn waits in its queue
// until SendNow presses the harness's send-now key, which it does only at an
// empty composer, so words typed into the terminal itself never go with it.
// A harness with no such key is told so, with nothing pressed.
func TestSendNowPressesTheHarnessKeyOnlyAtAnEmptyComposer(t *testing.T) {
	claude, _ := harness.NativeScreens(harness.Claude)
	codex, _ := harness.NativeScreens(harness.Codex)
	for _, test := range []struct {
		name        string
		screens     harness.Screens
		draft       string
		wantErr     error
		wantSentNow []string
	}{
		{name: "an empty composer", screens: claude, wantSentNow: []string{"use the staging database"}},
		{name: "his own words in the composer", screens: claude, draft: "and the", wantErr: ErrUnsentWords},
		{name: "a harness with no such key", screens: codex, wantErr: ErrNoSendNow},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newNativeFixture(t, harness.Claude, "claude-queues")
			nativeQueuedProof = 500 * time.Millisecond
			t.Cleanup(func() { nativeQueuedProof = 5 * time.Second })
			if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
			if err != nil {
				t.Fatal(err)
			}
			if err := f.service.SendNative(t.Context(), meta, "use the staging database"); !errors.Is(err, fleet.ErrQueuedForToolCall) {
				t.Fatalf("SendNative = %v, want the line queued", err)
			}
			record, err := host.ReadRecord(f.stateDir, meta.ID)
			if err != nil {
				t.Fatal(err)
			}
			if test.draft != "" {
				if err := pressNativeKeys(record, "his draft", test.draft); err != nil {
					t.Fatal(err)
				}
				for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
					screen, err := f.service.readNativeScreen(t.Context(), record)
					if err == nil && claude.ComposerHoldsText(screen) {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("the composer never showed his draft: %v", err)
					}
				}
			}

			// Act
			err = f.service.SendNow(t.Context(), record, test.screens)

			// Assert
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("SendNow = %v, want %v", err, test.wantErr)
			}
			if sentNow := sentNowLines(t, f, len(test.wantSentNow)); !slices.Equal(sentNow, test.wantSentNow) {
				t.Errorf("sent now = %q, want %q", sentNow, test.wantSentNow)
			}
		})
	}
}

// sentNowLines waits for at least n lines the fake harness recorded as sent
// now and returns them.
func sentNowLines(t *testing.T, f *nativeFixture, n int) []string {
	t.Helper()
	var sentNow []string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		sentNow = sentNow[:0]
		for _, event := range named(f.events(t), "sent now") {
			sentNow = append(sentNow, event.Text)
		}
		if len(sentNow) >= n {
			break
		}
	}
	return sentNow
}

// A harness in a turn reports a prompt through its hook when it queues the
// text, not when it takes it: Claude Code 2.1.292 ran UserPromptSubmit for a
// steer typed during a tool call ten seconds before it took the steer into
// its turn at the next tool call, live on 2026-10-06. So a hook report never
// proves a delivery to a goblin in a turn, and the steer is reported queued
// for its next tool call, typed and submitted once.
func TestAHookReportDoesNotProveASteerToANativeGoblinInATurn(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "")
	nativeQueuedProof = 500 * time.Millisecond
	t.Cleanup(func() { nativeQueuedProof = 5 * time.Second })
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	f.service.PromptSince = func(string, string, time.Time) (bool, error) { return true, nil }

	err = f.service.SendNative(context.Background(), meta, "CFO: run the tests")

	if !errors.Is(err, fleet.ErrQueuedForToolCall) {
		t.Errorf("SendNative = %v, want it queued for the goblin's next tool call despite the hook report", err)
	}
	if submitted := submittedLines(t, f, 2); !slices.Equal(submitted, []string{spawnPointer(t, f.fixture), "CFO: run the tests"}) {
		t.Errorf("submitted = %q, want the steer submitted once after the instruction", submitted)
	}
}

// A steer to a goblin in a turn is delivered once the harness's own record of
// the conversation shows it handed to the model, asked for with the task and
// the exact text typed, from before the submit; until then it is queued.
func TestASteerToANativeGoblinInATurnIsProvenByItsRecord(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "")
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	type ask struct {
		task, worktree, text string
		since                time.Time
	}
	var asked []ask
	before := time.Now()
	f.service.Took = func(_ context.Context, meta state.TaskMeta, session, text string, since time.Time) bool {
		if session != "" {
			t.Errorf("send unexpectedly selected a resume session: %s", session)
		}
		asked = append(asked, ask{meta.ID, meta.Worktree, text, since})
		// The fake records a line once it takes it, as a harness's record does.
		return len(named(f.events(t), "submitted")) == 2
	}

	err = f.service.SendNative(context.Background(), meta, "CFO: run the tests")
	after := time.Now()

	if err != nil {
		t.Errorf("SendNative = %v, want the record to prove it taken", err)
	}
	if len(asked) == 0 || asked[0].task != "task-7" || asked[0].worktree != meta.Worktree || asked[0].text != "CFO: run the tests" || asked[0].since.Before(before) || asked[0].since.After(after) {
		t.Errorf("the record was asked for %+v, want task-7's in %s holding the steer since the send", asked, meta.Worktree)
	}
	if submitted := submittedLines(t, f, 2); !slices.Equal(submitted, []string{spawnPointer(t, f.fixture), "CFO: run the tests"}) {
		t.Errorf("submitted = %q, want the steer submitted once after the instruction", submitted)
	}
}

// submittedLines waits for at least n lines the fake harness recorded as
// submitted and returns them.
func submittedLines(t *testing.T, f *nativeFixture, n int) []string {
	t.Helper()
	var submitted []string
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		submitted = submitted[:0]
		for _, event := range named(f.events(t), "submitted") {
			submitted = append(submitted, event.Text)
		}
		if len(submitted) >= n {
			break
		}
	}
	return submitted
}

// awaitComposer waits until the fake harness's turn has ended and its
// composer waits for input.
func awaitComposer(t *testing.T, f *nativeFixture) {
	t.Helper()
	record, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	screens, _ := harness.NativeScreens(harness.Codex)
	if _, err := f.service.awaitScreen(context.Background(), record, 30*time.Second, screens.IsReady); err != nil {
		t.Fatalf("the fake harness never ended its turn: %v", err)
	}
}
