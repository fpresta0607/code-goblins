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
// key reaches its terminal as the console reads it, and a key goblins send does
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

	textErr := f.service.SendNative(context.Background(), meta, "CEO: run the tests")
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
	want := []string{spawnPointer(t, f.fixture), "CEO: run the tests", ""}
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

// Codex 0.160 lists its skills while its line ends in a $ word and takes an
// Enter then as a pick from that list, so a steer that ends in a mention is
// typed with a space after it, and the Enter submits it.
func TestASteerEndingInAMentionIsSubmittedToANativeCodexGoblin(t *testing.T) {
	// Arrange
	f := newNativeFixture(t, harness.Codex, "turns")
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	awaitComposer(t, f)

	// Act
	sendErr := f.service.SendNative(context.Background(), meta, "CEO: use $alpha-notes")

	// Assert
	if sendErr != nil {
		t.Errorf("SendNative: %v", sendErr)
	}
	want := []string{spawnPointer(t, f.fixture), "CEO: use $alpha-notes "}
	var submitted []string
	for _, event := range named(f.events(t), "submitted") {
		submitted = append(submitted, event.Text)
	}
	if !slices.Equal(submitted, want) {
		t.Errorf("submitted = %q, want the instruction, then the steer with its trailing space", submitted)
	}
	if picked := named(f.events(t), "mention picked"); len(picked) != 0 {
		t.Errorf("mention picks = %+v, want the Enter to submit the steer", picked)
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

	if err == nil || !strings.Contains(err.Error(), "submitted once") || !strings.Contains(err.Error(), "goblins peek task-7") {
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

// A slash command an idle harness runs itself starts no turn, so it is
// delivered once it leaves the harness's input line, and typed once (#2193).
func TestAnIdleNativeGoblinTakesASlashCommandOnceItLeavesTheInputLine(t *testing.T) {
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

	// Act
	err = f.service.SendNative(t.Context(), meta, "/status")

	// Assert
	if err != nil {
		t.Errorf("SendNative /status = %v, want it delivered", err)
	}
	var submitted []string
	for _, event := range named(f.events(t), "submitted") {
		submitted = append(submitted, event.Text)
	}
	if want := []string{spawnPointer(t, f.fixture), "/status"}; !slices.Equal(submitted, want) {
		t.Errorf("submitted = %q, want the instruction, then /status once", submitted)
	}
}

// A native task whose terminal has ended takes nothing, and says which task.
// Text sent to it says nothing was typed, so a sender may send it again once
// the terminal is back, as for a goblin paused or between sessions.
func TestANativeTaskWithNoTerminalTakesNothing(t *testing.T) {
	service := Service{StateDir: t.TempDir()}
	meta := state.TaskMeta{ID: "task-7", Window: "native", Harness: string(harness.Codex), Backend: "native"}

	textErr := service.SendNative(context.Background(), meta, "CEO: run the tests")
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
	for _, text := range []string{"CEO: run the tests", "/compact"} {
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

	err = f.service.SendNative(context.Background(), meta, "CEO: run the tests")

	if !errors.Is(err, fleet.ErrQueuedForToolCall) {
		t.Errorf("SendNative = %v, want it queued for the next tool call", err)
	}
	if submitted := submittedLines(t, f, 2); !slices.Equal(submitted, []string{spawnPointer(t, f.fixture), "CEO: run the tests"}) {
		t.Errorf("submitted = %q, want the steer submitted once after the instruction", submitted)
	}
}

// A delivery that refuses a harness in a turn, as the supervisor's wake line
// and a board message held for the turn do, types nothing into one, where it
// once submitted the text into the turn and reported it queued for the next
// tool call (#1468). A command he sends is refused the same way.
func TestADeliveryThatRefusesATurnTypesNothingIntoOne(t *testing.T) {
	for _, text := range []string{"ceo watcher wake: 1 queued wake", "/compact"} {
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

// A command typed while a turn runs waits in the harness's queue, so it is
// reported submitted once but unconfirmed, even when the harness clears its
// input line for it, as Codex does for a slash command (#2193).
func TestASlashCommandTypedIntoATurnIsReportedUnconfirmed(t *testing.T) {
	// Arrange
	f := newNativeFixture(t, harness.Codex, "")
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}

	// Act
	err = f.service.SendNative(context.Background(), meta, "/compact")

	// Assert
	if err == nil || !strings.Contains(err.Error(), "submitted once") || !strings.Contains(err.Error(), "goblins peek task-7") {
		t.Errorf("SendNative /compact = %v, want it reported submitted once but unconfirmed", err)
	}
	if submitted := submittedLines(t, f, 2); !slices.Equal(submitted, []string{spawnPointer(t, f.fixture), "/compact"}) {
		t.Errorf("submitted = %q, want the instruction, then /compact once", submitted)
	}
}

// The Overlord, 2026-11-11: "cant type / commands into modern chat like
// terminal" (#2372). In the terminal a command its harness runs at once
// beside a turn, as Codex 0.160 runs /status, runs the moment he enters it,
// while a delivery that holds commands for the turn's end refused it the turn
// like any other. Such a command is typed into the turn, once, and delivered
// once the prompt line shows it ran; a command its harness would only queue
// is refused that same turn still. Its own harness's commands decide which:
// Codex 0.160 has no /remote-control, which Claude Code runs beside a turn,
// and runs the /diff there that Claude Code holds for the turn's end.
func TestACommandItsHarnessRunsBesideATurnIsDeliveredIntoOne(t *testing.T) {
	for _, test := range []struct{ name, refused, delivered string }{
		{name: "commands Claude Code would take the same way", refused: "/compact", delivered: "/status"},
		{name: "commands Claude Code would take the other way", refused: "/remote-control", delivered: "/diff"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newNativeFixture(t, harness.Codex, "")
			if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
			if err != nil {
				t.Fatal(err)
			}
			f.service.ShouldRefuseTurn = true

			// Act
			held := f.service.SendNative(context.Background(), meta, test.refused)
			ran := f.service.SendNative(context.Background(), meta, test.delivered)

			// Assert
			if !errors.Is(held, ErrInTurn) {
				t.Fatalf("SendNative %s = %v, want it refused as in a turn", test.refused, held)
			}
			if ran != nil {
				t.Errorf("SendNative %s = %v, want it delivered into the turn", test.delivered, ran)
			}
			if submitted := submittedLines(t, f, 2); !slices.Equal(submitted, []string{spawnPointer(t, f.fixture), test.delivered}) {
				t.Errorf("submitted = %q, want the instruction, then %s once", submitted, test.delivered)
			}
		})
	}
}

// A command typed into a turn starts none and can leave no record, so the
// prompt line says what became of it: a Claude Code that ran it at once
// leaves its prompt line empty, and one that queued it after all, as a
// release whose own list of such commands has moved on would, offers its
// queue there. The first is delivered and the second reported queued for the
// turn's end, never delivered (#2372). A prompt line that offered its queue
// before the command was typed, his words waiting in it, offers it still
// once the command ran, and that command is delivered all the same.
func TestACommandTypedIntoATurnReadsRanOrQueuedAsItsPromptLineShows(t *testing.T) {
	for _, test := range []struct {
		name      string
		ahead     string
		command   string
		wantErr   error
		wantEvent string
	}{
		{name: "one its harness ran at once", command: "/model opus", wantEvent: "ran beside its turn"},
		{name: "one its harness ran at once with his words in its queue", ahead: "use the staging database", command: "/model opus", wantEvent: "ran beside its turn"},
		{name: "one its harness queued after all", command: "/status", wantErr: fleet.ErrQueuedForToolCall, wantEvent: "queued"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newNativeFixture(t, harness.Claude, "claude-queues")
			if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
			if err != nil {
				t.Fatal(err)
			}
			if test.ahead != "" {
				nativeQueuedProof = 500 * time.Millisecond
				t.Cleanup(func() { nativeQueuedProof = 5 * time.Second })
				if err := f.service.SendNative(t.Context(), meta, test.ahead); !errors.Is(err, fleet.ErrQueuedForToolCall) {
					t.Fatalf("SendNative %q = %v, want it queued in the turn", test.ahead, err)
				}
			}
			f.service.ShouldRefuseTurn = true

			// Act
			err = f.service.SendNative(t.Context(), meta, test.command)

			// Assert
			if !errors.Is(err, test.wantErr) || errors.Is(err, ErrInTurn) {
				t.Errorf("SendNative %s = %v, want %v", test.command, err, test.wantErr)
			}
			if recorded := recordedLines(t, f, test.wantEvent, 1); !slices.Equal(recorded, []string{test.command}) {
				t.Errorf("%s = %q, want the command once", test.wantEvent, recorded)
			}
		})
	}
}

// A harness can empty its prompt line a moment before it draws what it
// queued, so a command typed into a turn reads run only once that line shows
// it gone on two reads in a row: one empty read ahead of the queue's offer is
// a command its harness queued, and a read that shows the command on the line
// again starts the count over. A queue the line offered before the command
// was typed says nothing of the command (#2372).
func TestACommandTypedIntoATurnReadsRanOnlyOnceItsPromptLineIsEmptyTwiceInARow(t *testing.T) {
	rule := strings.Repeat("─", 80)
	screen := func(prompt string) []string {
		return []string{"✽ Reticulating… (esc to interrupt)", rule, prompt, rule, "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}
	}
	empty, typed, offersQueue := screen(">"), screen("> /model opus"), screen("> Press up to edit queued messages")
	for _, test := range []struct {
		name         string
		offeredQueue bool
		reads        [][]string
		wantRan      bool
		wantQueued   bool
	}{
		{name: "empty twice in a row", reads: [][]string{empty, empty}, wantRan: true},
		{name: "empty once, then offering its queue", reads: [][]string{empty, offersQueue}, wantQueued: true},
		{name: "empty, the command on the line again, empty, then offering its queue", reads: [][]string{empty, typed, empty, offersQueue}, wantQueued: true},
		{name: "offering the queue it offered before the command", offeredQueue: true, reads: [][]string{offersQueue, offersQueue}, wantRan: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			screens, _ := harness.NativeScreens(harness.Claude)
			read := 0
			next := func(context.Context) ([]string, error) {
				shown := test.reads[min(read, len(test.reads)-1)]
				read++
				return shown, nil
			}

			// Act
			ran, isQueued := RanBesideTurn(t.Context(), next, screens, "/model opus", test.offeredQueue)

			// Assert
			if ran != test.wantRan || isQueued != test.wantQueued {
				t.Errorf("RanBesideTurn = ran %t, queued %t after %d reads; want ran %t, queued %t", ran, isQueued, read, test.wantRan, test.wantQueued)
			}
		})
	}
}

// Send now on a command held for a Claude Code turn presses its send-now key
// once, as soon as the prompt line shows the command gone into the turn's
// queue, however often the line reads so after, and the command ran only once
// that line shows it gone with no queue offered on two reads in a row; one
// that never queued ran beside the turn with no key pressed (#1915). A case
// that waits out the window is read for a second of it.
func TestSendNowOnACommandInATurnsQueuePressesItsKeyOnceAndReadsItRanOnceTheQueueIsGone(t *testing.T) {
	rule := strings.Repeat("─", 80)
	screen := func(prompt string) []string {
		return []string{"✽ Reticulating… (esc to interrupt)", rule, prompt, rule, "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}
	}
	empty, typed, offersQueue := screen(">"), screen("> /compact"), screen("> Press up to edit queued messages")
	errGone := errors.New("the terminal is gone")
	for _, test := range []struct {
		name        string
		reads       [][]string
		isWaitedOut bool
		pressErr    error
		wantRan     bool
		wantPressed bool
		wantPresses int
		wantErr     error
	}{
		{name: "queued, then gone with no queue twice", reads: [][]string{typed, offersQueue, offersQueue, empty, empty}, wantRan: true, wantPressed: true, wantPresses: 1},
		{name: "empty once before its queue shows", reads: [][]string{typed, empty, offersQueue, empty, empty}, wantRan: true, wantPressed: true, wantPresses: 1},
		{name: "never queued, gone twice", reads: [][]string{typed, empty, empty}, wantRan: true},
		{name: "still queued after its key", reads: [][]string{offersQueue}, isWaitedOut: true, wantPressed: true, wantPresses: 1},
		{name: "its key cannot be pressed", reads: [][]string{offersQueue}, pressErr: errGone, wantPresses: 1, wantErr: errGone},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			screens, _ := harness.NativeScreens(harness.Claude)
			ctx := t.Context()
			if test.isWaitedOut {
				// stall-accepted: the case means this context to run out, and a stall only ends it sooner, after the one press it checks.
				waited, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				ctx = waited
			}
			read, presses := 0, 0
			next := func(context.Context) ([]string, error) {
				shown := test.reads[min(read, len(test.reads)-1)]
				read++
				return shown, nil
			}
			press := func() error {
				presses++
				return test.pressErr
			}

			// Act
			ran, isPressed, err := RanNow(ctx, next, press, screens, "/compact")

			// Assert
			if ran != test.wantRan || isPressed != test.wantPressed || presses != test.wantPresses || !errors.Is(err, test.wantErr) {
				t.Errorf("RanNow = ran %t, pressed %t, %v with %d presses after %d reads; want ran %t, pressed %t, %v with %d presses", ran, isPressed, err, presses, read, test.wantRan, test.wantPressed, test.wantErr, test.wantPresses)
			}
		})
	}
}

// Send now on a command held for a Codex's or a pi's turn presses the Escape
// that ends the turn once, and only over an empty prompt line, so words left
// there keep their own; the command then goes at the empty, ready prompt line
// the turn's end leaves, read so twice in a row, and a turn that goes on, one
// whose line reads so only once before a turn of its own, or one whose harness
// hands its queue back onto that line, leaves none (#1915). The rows are Codex
// 0.160's and pi 0.85.1's full screens, read live on 2026-10-17
// (TestFullScreenCodexAndPiReadAsReadyOnlyWhileNoTurnRuns). A case that waits
// out the window is read for a second of it.
func TestSendNowEndsATurnForACommandOnlyOverAnEmptyPromptLineAndWaitsForTheLineItLeaves(t *testing.T) {
	codexFooter := "  ? for shortcuts                                    ⚠ 5 warnings · f2 to view"
	codexTurn := []string{"› Count from 1 to 300, one number per line, nothing else.", "", "", "Working (2s • esc to interrupt)", "", "", "› Ask Codex to do anything", "", `  GPT-6-Astra low · ~\work\task · ⠋`, codexFooter}
	codexAfter := []string{"• done", "", "  Worked for 10s • 10:28 PM", "", "", "› Ask Codex to do anything", "", `  GPT-6-Astra low · ~\work\task · Count to 300`, codexFooter}
	codexWords := slices.Clone(codexTurn)
	codexWords[6] = "› run the tests"
	rule := strings.Repeat("─", 40)
	piFooter := "↑8.4k ↓357 R15k CH91.8% $0.001 0.8%/1.0M (auto)             (openrouter) deepseek/deepseek-v4.1-flash • high"
	piTurn := []string{" Reply from 127.0.0.1: bytes=32 time<1ms TTL=128", " Elapsed 2.0s", "", "", "── ⠸ Working " + rule, "", rule, `~\work\task`, piFooter}
	piAfter := []string{" Took 5.7s", "", " done", "", rule, "", rule, `~\work\task`, piFooter}
	piHandedBack := slices.Clone(piAfter)
	piHandedBack[5] = "run the tests"
	errGone := errors.New("the terminal is gone")
	for _, test := range []struct {
		name        string
		kind        harness.Kind
		screen      []string
		reads       [][]string
		isWaitedOut bool
		pressErr    error
		wantPresses int
		want        []string
	}{
		{name: "codex's turn ends", kind: harness.Codex, screen: codexTurn, reads: [][]string{codexTurn, codexAfter}, wantPresses: 1, want: codexAfter},
		{name: "pi's turn ends", kind: harness.Pi, screen: piTurn, reads: [][]string{piTurn, piAfter}, wantPresses: 1, want: piAfter},
		{name: "words on codex's prompt line", kind: harness.Codex, screen: codexWords, reads: [][]string{codexAfter}},
		{name: "codex's turn goes on", kind: harness.Codex, screen: codexTurn, reads: [][]string{codexTurn}, isWaitedOut: true, wantPresses: 1},
		{name: "one idle read between codex's turns", kind: harness.Codex, screen: codexTurn, reads: [][]string{codexTurn, codexAfter, codexTurn}, isWaitedOut: true, wantPresses: 1},
		{name: "pi hands its queue back onto its prompt line", kind: harness.Pi, screen: piTurn, reads: [][]string{piHandedBack}, isWaitedOut: true, wantPresses: 1},
		{name: "its key cannot be pressed", kind: harness.Codex, screen: codexTurn, reads: [][]string{codexAfter}, pressErr: errGone, wantPresses: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			screens, _ := harness.NativeScreens(test.kind)
			if !screens.IsWorking(test.screen) {
				t.Fatalf("%q reads as no turn", test.screen)
			}
			ctx := t.Context()
			if test.isWaitedOut {
				// stall-accepted: the case means this context to run out, and a stall only ends it sooner, after the one press it checks.
				waited, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				ctx = waited
			}
			read, presses := 0, 0
			next := func(context.Context) ([]string, error) {
				shown := test.reads[min(read, len(test.reads)-1)]
				read++
				return shown, nil
			}
			press := func() error {
				presses++
				return test.pressErr
			}

			// Act
			idle := InterruptTurn(ctx, next, press, screens, test.screen)

			// Assert
			if !slices.Equal(idle, test.want) || presses != test.wantPresses {
				t.Errorf("InterruptTurn = %q with %d presses after %d reads; want %q with %d presses", idle, presses, read, test.want, test.wantPresses)
			}
		})
	}
}

// A board message he asked for now reaches a Claude Code in a turn the way
// its terminal sends one now: typed and queued, then Ctrl+X Ctrl+S once its
// composer shows the line gone into that queue, so it takes the line at once
// (the Overlord, 2026-10-30: "when sent now it actually sends now same way
// termianl expereince down with ctrl x crtl s"). Without that ask the line
// waits in the queue for the harness's next tool call. A line whose key went
// but which nothing yet shows taken says its key went (ErrSentNow), and one
// whose key never went says only that it is queued.
func TestSendNowHasAClaudeCodeInATurnTakeTheLineAtOnce(t *testing.T) {
	for _, test := range []struct {
		name          string
		proof         time.Duration
		shouldSendNow bool
		isTakeSeen    bool
		wantErr       error
		wantSentNow   []string
	}{
		{name: "sent now", proof: 30 * time.Second, shouldSendNow: true, isTakeSeen: true, wantSentNow: []string{"use the staging database"}},
		{name: "sent now with its take not seen yet", proof: 30 * time.Second, shouldSendNow: true, wantErr: ErrSentNow, wantSentNow: []string{"use the staging database"}},
		{name: "queued", proof: 500 * time.Millisecond, wantErr: fleet.ErrQueuedForToolCall},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newNativeFixture(t, harness.Claude, "claude-queues")
			nativeQueuedProof = test.proof
			t.Cleanup(func() { nativeQueuedProof = 5 * time.Second })
			if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
			if err != nil {
				t.Fatal(err)
			}
			f.service.ShouldSendNow = test.shouldSendNow
			f.service.Took = func(context.Context, state.TaskMeta, string, string, time.Time) bool {
				return test.isTakeSeen && len(named(f.events(t), "sent now")) > 0
			}

			// Act
			err = f.service.SendNative(t.Context(), meta, "use the staging database")

			// Assert
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("SendNative = %v, want %v", err, test.wantErr)
			}
			if errors.Is(err, ErrSentNow) != (test.wantErr == ErrSentNow) {
				t.Errorf("SendNative = %v, want it to say its key went only when it did", err)
			}
			if queued := recordedLines(t, f, "queued", 1); !slices.Equal(queued, []string{"use the staging database"}) {
				t.Errorf("queued = %q, want the line queued once", queued)
			}
			if sentNow := recordedLines(t, f, "sent now", len(test.wantSentNow)); !slices.Equal(sentNow, test.wantSentNow) {
				t.Errorf("sent now = %q, want %q", sentNow, test.wantSentNow)
			}
		})
	}
}

// A screen of its own drawn over a Claude Code's turn takes no send-now key
// from a delivery, even with an empty prompt line above it, since the key
// would go to that screen rather than the composer, and the delivery says
// only that the line is queued, never that its key went.
func TestADeliverySentNowPressesNoKeyOverAScreenOfItsOwn(t *testing.T) {
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
	rule := strings.Repeat("─", 80)
	f.service.ShouldSendNow = true
	f.service.ReadScreen = func(record host.Record) ([]string, error) {
		screen, err := host.ReadScreen(record)
		if len(named(f.events(t), "queued")) > 0 {
			return []string{"✽ Reticulating… (esc to interrupt)", rule, ">", rule, " Do you want to create nine.txt?", " ❯ 1. Yes", "   2. No", ""}, err
		}
		return screen, err
	}

	// Act
	err = f.service.SendNative(t.Context(), meta, "use the staging database")

	// Assert
	if !errors.Is(err, fleet.ErrQueuedForToolCall) || errors.Is(err, ErrSentNow) {
		t.Fatalf("SendNative = %v, want the line queued with nothing proving it taken and no key gone", err)
	}
	time.Sleep(500 * time.Millisecond)
	if sentNow := recordedLines(t, f, "sent now", 0); len(sentNow) != 0 {
		t.Errorf("sent now = %q, want no key pressed over the prompt", sentNow)
	}
}

// A delivery whose send-now key cannot be pressed, its terminal gone with the
// line already in its harness's queue, says the line is queued, never that
// its key went, rather than an error that leaves the message uncertain.
func TestADeliverySentNowWhoseKeyCannotBePressedSaysOnlyThatItIsQueued(t *testing.T) {
	// Arrange
	f := newNativeFixture(t, harness.Claude, "claude-queues")
	nativeQueuedProof = 30 * time.Second
	t.Cleanup(func() { nativeQueuedProof = 5 * time.Second })
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	claude, _ := harness.NativeScreens(harness.Claude)
	f.service.ShouldSendNow = true
	f.service.ReadScreen = func(record host.Record) ([]string, error) {
		screen, err := host.ReadScreen(record)
		if err == nil && len(named(f.events(t), "queued")) > 0 && claude.ComposerEmpty(screen) {
			return screen, host.Close(f.stateDir, record, nativeCloseWait)
		}
		return screen, err
	}

	// Act
	err = f.service.SendNative(t.Context(), meta, "use the staging database")

	// Assert
	if !errors.Is(err, fleet.ErrQueuedForToolCall) || errors.Is(err, ErrSentNow) {
		t.Fatalf("SendNative = %v, want the line queued with no key gone", err)
	}
	if sentNow := recordedLines(t, f, "sent now", 0); len(sentNow) != 0 {
		t.Errorf("sent now = %q, want no key pressed", sentNow)
	}
}

// recordedLines waits for at least n lines the fake harness recorded as name
// and returns them.
func recordedLines(t *testing.T, f *nativeFixture, name string, n int) []string {
	t.Helper()
	var lines []string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		lines = lines[:0]
		for _, event := range named(f.events(t), name) {
			lines = append(lines, event.Text)
		}
		if len(lines) >= n {
			break
		}
	}
	return lines
}

// keyRecorder stands in for a terminal's input: it keeps each write apart,
// with when it came, and answers each with err.
type keyRecorder struct {
	writes []string
	at     []time.Time
	err    error
}

func (r *keyRecorder) Input(keys []byte) error {
	r.writes = append(r.writes, string(keys))
	r.at = append(r.at, time.Now())
	return r.err
}

// Send now interrupts a harness's own turn through the control it binds to
// that, pressed the way every delivery presses it: Claude Code 2.1.275's
// chat:sendNow chord, and the Escape Codex's working row names and pi binds to
// app.interrupt. Codex and pi were read as having none, so a message sent now
// into either stayed in its queue until its next tool call.
func TestEachHarnessNamesTheKeyItsSendNowPresses(t *testing.T) {
	for name, test := range map[string]struct {
		kind       harness.Kind
		wantWrites []string
	}{
		"claude": {harness.Claude, []string{"\x18", "\x13"}},
		"codex":  {harness.Codex, []string{"\x1b"}},
		"pi":     {harness.Pi, []string{"\x1b"}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			screens, _ := harness.NativeScreens(test.kind)
			recorder := &keyRecorder{}

			// Act: the press a goblin's delivery and the CEO's delivery make.
			err := PressKeys(recorder.Input, screens.SendNow)

			// Assert
			if err != nil || !slices.Equal(recorder.writes, test.wantWrites) {
				t.Fatalf("pressing %q wrote %q, %v; want %q", screens.SendNow, recorder.writes, err, test.wantWrites)
			}
		})
	}
}

// Ctrl+X Ctrl+S is two keys, which fingers press one after the other, so it
// reaches a harness as two writes, in that order and 50 ms apart, never as
// one input it might not read as a chord. The stand-in harnesses read input a
// rune at a time and cannot tell the two forms apart, so the writes themselves
// are what is checked. A key alone is one write, and a key that fails ends the
// press, leaving the next key unsent.
func TestKeysReachATerminalOneWriteEachAndApart(t *testing.T) {
	claude, _ := harness.NativeScreens(harness.Claude)
	codex, _ := harness.NativeScreens(harness.Codex)
	pi, _ := harness.NativeScreens(harness.Pi)
	failed := errors.New("the terminal went away")
	for _, test := range []struct {
		name       string
		keys       string
		failure    error
		wantWrites []string
	}{
		{name: "Claude Code's send-now chord", keys: claude.SendNow, wantWrites: []string{"\x18", "\x13"}},
		{name: "Codex's interrupt key", keys: codex.SendNow, wantWrites: []string{"\x1b"}},
		{name: "pi's interrupt key", keys: pi.SendNow, wantWrites: []string{"\x1b"}},
		{name: "Enter", keys: "\r", wantWrites: []string{"\r"}},
		{name: "a chord whose first key fails", keys: claude.SendNow, failure: failed, wantWrites: []string{"\x18"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			recorder := &keyRecorder{err: test.failure}

			// Act
			err := PressKeys(recorder.Input, test.keys)

			// Assert
			if !errors.Is(err, test.failure) {
				t.Fatalf("PressKeys = %v, want %v", err, test.failure)
			}
			if !slices.Equal(recorder.writes, test.wantWrites) {
				t.Fatalf("writes = %q, want %q", recorder.writes, test.wantWrites)
			}
			for i := 1; i < len(recorder.at); i++ {
				if gap := recorder.at[i].Sub(recorder.at[i-1]); gap < 50*time.Millisecond {
					t.Errorf("write %d came %s after the one before it, want at least 50ms", i, gap)
				}
			}
		})
	}
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

	err = f.service.SendNative(context.Background(), meta, "CEO: run the tests")

	if !errors.Is(err, fleet.ErrQueuedForToolCall) {
		t.Errorf("SendNative = %v, want it queued for the goblin's next tool call despite the hook report", err)
	}
	if submitted := submittedLines(t, f, 2); !slices.Equal(submitted, []string{spawnPointer(t, f.fixture), "CEO: run the tests"}) {
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

	err = f.service.SendNative(context.Background(), meta, "CEO: run the tests")
	after := time.Now()

	if err != nil {
		t.Errorf("SendNative = %v, want the record to prove it taken", err)
	}
	if len(asked) == 0 || asked[0].task != "task-7" || asked[0].worktree != meta.Worktree || asked[0].text != "CEO: run the tests" || asked[0].since.Before(before) || asked[0].since.After(after) {
		t.Errorf("the record was asked for %+v, want task-7's in %s holding the steer since the send", asked, meta.Worktree)
	}
	if submitted := submittedLines(t, f, 2); !slices.Equal(submitted, []string{spawnPointer(t, f.fixture), "CEO: run the tests"}) {
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

// A board message or answer of his is his own input, so his harness's next
// screen is bound to him, while a line nobody says is his, the supervisor's
// notice, a wake line or a command's own typing, clears that binding: a
// picker those draw never reaches Modern Chat as his to answer. His mark is
// never a property of what the generic SendNative is given, so a caller that
// authors a notice cannot set it by accident.
func TestOnlyHisOwnDeliveryKeepsTheGoblinsMenuHis(t *testing.T) {
	// Arrange
	f := newNativeFixture(t, harness.Codex, "turns")
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	awaitComposer(t, f)
	record, found := f.terminal()
	if !found {
		t.Fatal("the spawn recorded no terminal")
	}
	viewer, err := host.View(record)
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()

	// Act: the board delivers the message he queued, which its own path marks
	// as his.
	his := f.service
	his.His = true
	if err := his.SendNative(t.Context(), meta, "carry on"); err != nil {
		t.Fatalf("SendNative: %v", err)
	}

	// Assert
	if _, submission, _, err := viewer.Screen(record, ""); err != nil || submission == "" {
		t.Fatalf("submission = %q, %v after his own message; want the screen bound to him", submission, err)
	}

	// Act: a notice nobody says is his, as the supervisor's own lines go,
	// once his own message's turn has ended and the composer waits again.
	awaitComposer(t, f)
	if err := f.service.SendNative(t.Context(), meta, "the supervisor's notice"); err != nil {
		t.Fatalf("SendNative: %v", err)
	}

	// Assert
	if _, submission, _, err := viewer.Screen(record, ""); err != nil || submission != "" {
		t.Fatalf("submission = %q, %v after a notice; want it cleared", submission, err)
	}
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
