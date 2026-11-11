package supervisor

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// heldQueue starts the fixture harness of mode in a turn, as the CEO's
// terminal or as goblin task-1's, and has the board type each of texts into
// that turn as his messages m1, m2 and so on, which the harness queues. It
// returns the task those messages name and the line the board typed for a
// text. The CEO's are typed by the board's own delivery; a goblin's by the
// line that delivery types, recorded as that delivery records one submitted
// into a turn.
func heldQueue(t *testing.T, isGoblin bool, mode string, texts ...string) (*Store, *Service, hostedTerminal, string, func(string) string) {
	t.Helper()
	useConversations(t)
	store, home := testStore(t)
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}}}
	task, typed := "", func(text string) string { return text }
	var meta state.TaskMeta
	if isGoblin {
		meta = makeNative(t, home.State, "task-1")
		meta.Harness = "codex"
		if strings.HasPrefix(mode, "claude-") {
			meta.Harness = "claude"
		}
		if err := state.WriteTaskMeta(home.State, meta); err != nil {
			t.Fatal(err)
		}
		task, typed = meta.ID, goblinLine
	}
	terminal := nativeComposer(t, home.State, cmp.Or(task, "ceo"), mode)
	for i, text := range texts {
		id := fmt.Sprintf("m%d", i+1)
		execute := service.execute
		if isGoblin {
			messageTo(t, store, meta, id, text)
			execute = func(_ context.Context, a Action) (Evaluation, error) {
				terminal.typeLine(t, goblinLine(a.Text))
				return service.behindGoblinsTurn(meta.ID, time.Now().UTC(), a.Text, ""), nil
			}
		} else {
			if _, err := store.Queue(Action{ID: id, Kind: "message", Text: text}); err != nil {
				t.Fatal(err)
			}
			pastUndoWindow(t, store)
		}
		if err := store.ProcessOne(context.Background(), execute); err != nil {
			t.Fatal(err)
		}
		if lines := terminal.waitForLines(t, i+2); len(lines) != i+2 || lines[i+1] != "queued "+typed(text) {
			t.Fatalf("terminal input = %q; want %q queued in its turn", lines, typed(text))
		}
		if held := actionOf(store, id); !isHarnessHeld(held) {
			t.Fatalf("%s = %+v; want it held in its harness's queue", id, held)
		}
	}
	return store, service, terminal, task, typed
}

// promptLineEmpties waits until terminal's prompt line reads empty to the
// screens of Claude Code.
func promptLineEmpties(t *testing.T, terminal hostedTerminal) {
	t.Helper()
	record, err := host.ReadRecord(terminal.stateDir, terminal.id)
	if err != nil {
		t.Fatal(err)
	}
	screens, _ := harness.NativeScreens(harness.Claude)
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		screen, err := host.ReadScreen(record)
		if err == nil && screens.ComposerEmpty(screen) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the prompt line never read empty: %v\n%s", err, host.ScreenTail(screen, 8))
		}
	}
}

// The Overlord, 2026-11-10: "a message the harness still holds queued reads
// Sent in Modern Chat. It must read Queued" (#2332). A message submitted into
// a turn under way waits in its harness's own queue, so it says it is queued
// there, and is delivered once the session's record shows the turn took it.
func TestHisMessageSubmittedIntoATurnReadsQueuedInItsSessionUntilTheTurnTakesIt(t *testing.T) {
	for _, test := range []struct{ mode, kind string }{
		{mode: "claude-busy", kind: "claude"},
		{mode: "busy", kind: "codex"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			// Arrange
			records := useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, test.mode)
			service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
			if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "stop and rebase"}); err != nil {
				t.Fatal(err)
			}
			pastUndoWindow(t, store)

			// Act
			err := store.ProcessOne(context.Background(), service.execute)
			held := actionOf(store, "m1")
			if err := os.WriteFile(terminal.typed+".accept", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			terminal.waitForLines(t, 3)
			takenInRecord(t, records, test.kind, "session-1", "", "stop and rebase")
			settleErr := store.settleDeliveries(time.Now().UTC(), service.lookAtTerminal)
			taken := actionOf(store, "m1")

			// Assert
			if err != nil || held.Status != "running" || held.Awaiting == nil || !held.Awaiting.IsHeld || held.Message != heldByCEO {
				t.Errorf("delivery = %v, action = %+v; want it to say it is queued in the CEO's session", err, held)
			}
			if settleErr != nil || taken.Status != "succeeded" || taken.Awaiting != nil {
				t.Errorf("settle = %v, action = %+v; want it delivered once the CEO's record shows it taken", settleErr, taken)
			}
		})
	}
}

// #2332: "Send now must interrupt and deliver it". Send now on a message its
// harness holds in its queue presses that harness's send-now key, once, and a
// press that finds the queue given up already presses nothing, since the key
// would interrupt the turn that reads the message.
func TestSendNowOnAMessageItsHarnessHoldsPressesThatHarnesssSendNowKeyOnce(t *testing.T) {
	for _, test := range []struct {
		name     string
		isGoblin bool
		mode     string
		wantKey  string
	}{
		{name: "the CEO's Claude Code", mode: "claude-busy", wantKey: "sent now"},
		{name: "a goblin's Claude Code", isGoblin: true, mode: "claude-busy", wantKey: "sent now"},
		{name: "the CEO's Codex", mode: "busy", wantKey: "interrupted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, service, terminal, task, _ := heldQueue(t, test.isGoblin, test.mode, "first words", "second words")
			before := actionOf(store, "m2").UpdatedAt

			// Act
			pressed := sendNow(service, task, "m2")
			terminal.waitForLines(t, 4)
			sent := actionOf(store, "m2")
			again := boardRequest(NewHTTP(service, "board.local", nil), "POST", "/api/actions", fmt.Sprintf(`{"id":"again","kind":"message_now","task_id":%q,"generation":"","target":"m2"}`, task))

			// Assert
			if pressed.Code != 202 || again.Code != 202 {
				t.Errorf("Send now = %d %s, again = %d %s; want both taken", pressed.Code, pressed.Body, again.Code, again.Body)
			}
			if !isHarnessHeld(sent) || sent.Message != sentNowInTurn || !sent.UpdatedAt.After(before) {
				t.Errorf("after Send now = %+v; want it to say its send-now key went, as of then", sent)
			}
			if lines := terminal.lines(t); len(lines) != 4 || lines[3] != test.wantKey {
				t.Errorf("terminal input = %q; want %q pressed once", lines, test.wantKey)
			}
		})
	}
}

// #2332: a queued message "can be edited and deleted from Modern Chat and the
// change lands on the harness's own queued item". Claude Code gives its queue
// back only whole, every line onto the prompt line for one Up, so Cancel,
// which Edit sends too, takes that queue back, clears it off the prompt line
// unsubmitted, deletes the message he named, and types the others again in
// the order he sent them.
func TestCancelOfAMessageItsHarnessHoldsTakesItOutOfThatQueueAndKeepsTheOthers(t *testing.T) {
	for _, test := range []struct {
		name     string
		isGoblin bool
	}{
		{name: "the CEO's"},
		{name: "a goblin's", isGoblin: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, service, terminal, task, typed := heldQueue(t, test.isGoblin, "claude-busy", "first words", "second words", "third words")

			// Act
			cancel := pressOnQueued(service, "message_withdraw", task, "m2", "second words")
			kept := []string{actionOf(store, "m1").Status, actionOf(store, "m3").Status}
			promptLineEmpties(t, terminal)
			if !test.isGoblin {
				// The worker tries a delivery its terminal put off again, as here.
				for deadline := time.Now().Add(30 * time.Second); !(isHarnessHeld(actionOf(store, "m1")) && isHarnessHeld(actionOf(store, "m3"))) && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
					if err := store.ProcessOne(context.Background(), service.execute); err != nil && !errors.Is(err, ErrDeferred) {
						t.Fatal(err)
					}
				}
				terminal.waitForLines(t, 8)
			}

			// Assert
			if cancel.Code != 202 || slices.ContainsFunc(store.Snapshot().Actions, func(a Action) bool { return a.ID == "m2" }) {
				t.Errorf("Cancel = %d %s, actions = %+v; want the message taken off the board", cancel.Code, cancel.Body, store.Snapshot().Actions)
			}
			if !slices.Equal(kept, []string{"queued", "queued"}) {
				t.Errorf("the other messages = %q; want each waiting on the board to be typed again", kept)
			}
			input := terminal.lines(t)
			if !slices.Contains(input, "took back "+typed("first words")+" | "+typed("second words")+" | "+typed("third words")) || !slices.Contains(input, "cleared") {
				t.Errorf("terminal input = %q; want its queue taken back and cleared off the prompt line", input)
			}
			if test.isGoblin {
				return
			}
			joined := strings.Join(input, "\n")
			if strings.Count(joined, "queued first words") != 2 || strings.Count(joined, "queued second words") != 1 || strings.Count(joined, "queued third words") != 2 {
				t.Errorf("terminal input = %q; want the two he kept typed into the turn again, and the one he cancelled never again", input)
			}
			if !isHarnessHeld(actionOf(store, "m1")) || !isHarnessHeld(actionOf(store, "m3")) {
				t.Errorf("after they were typed again = %+v; want both held in the CEO's queue", store.Snapshot().Actions)
			}
		})
	}
}

// A queue the board cannot take back whole and alone is left as it is, and
// the press says why: a harness with no key for it, a prompt line that holds
// his own draft, and a queue that also holds a line typed in the terminal,
// which comes onto the prompt line with the board's and is never cleared with
// them.
func TestAQueueTheBoardCannotTakeBackAloneIsNeverCleared(t *testing.T) {
	for _, test := range []struct {
		name, mode, before string
		wantSays           string
		wantHeld           bool
	}{
		{name: "Codex, which gives its queue back to no key", mode: "busy", wantSays: "gives back to no key", wantHeld: true},
		{name: "his own draft on the prompt line", mode: "claude-busy", before: "a draft of his", wantSays: "does not offer its queue now", wantHeld: true},
		{name: "a line typed in the terminal in the same queue", mode: "claude-busy", before: "typed in the terminal\r", wantSays: "holds words the board cannot take back alone"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, service, terminal, _, _ := heldQueue(t, false, test.mode, "first words")
			if test.before != "" {
				record, err := host.ReadRecord(terminal.stateDir, terminal.id)
				if err != nil {
					t.Fatal(err)
				}
				client, err := host.Dial(record)
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				if err := client.Input([]byte(test.before)); err != nil {
					t.Fatal(err)
				}
				if strings.HasSuffix(test.before, "\r") {
					terminal.waitForLines(t, 3)
				} else {
					screens, _ := harness.NativeScreens(harness.Claude)
					for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
						if screen, err := host.ReadScreen(record); err == nil && screens.ComposerHoldsText(screen) {
							break
						}
					}
				}
			}

			// Act
			cancel := pressOnQueued(service, "message_withdraw", "", "m1", "first words")
			after := actionOf(store, "m1")

			// Assert
			if cancel.Code != 409 || !strings.Contains(cancel.Body.String(), test.wantSays) {
				t.Errorf("Cancel = %d %s; want it refused saying %q", cancel.Code, cancel.Body, test.wantSays)
			}
			if isHarnessHeld(after) != test.wantHeld || !test.wantHeld && (after.Status != "uncertain" || after.LeftTyped == nil) {
				t.Errorf("after the press = %+v; want held %t, else watched where it was left typed", after, test.wantHeld)
			}
			if input := terminal.lines(t); slices.Contains(input, "cleared") || test.wantHeld && slices.ContainsFunc(input, func(line string) bool { return strings.HasPrefix(line, "took back ") }) {
				t.Errorf("terminal input = %q; want nothing cleared, and no key where the queue was left", input)
			}
		})
	}
}

// #2332: "Send now on a queued message interrupts the running turn and
// delivers that message next. It never leaves the message queued." Claude
// Code's send-now key takes a slash command at the head of its queue alone,
// as a turn of its own, and left his message behind three such commands
// queued on 2026-11-10. So a command to a CEO in a turn waits on the board,
// whatever its harness, and Send now on the message behind it types that
// message into the turn ahead of it and presses the key over it alone.
func TestSendNowOnAMessageBehindACommandForTheCEOsTurnHasThatMessageTakenFirst(t *testing.T) {
	for _, test := range []struct{ name, mode, wantKey string }{
		{name: "Claude Code", mode: "claude-busy", wantKey: "sent now"},
		{name: "Codex", mode: "busy", wantKey: "interrupted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, test.mode)
			service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}}}
			for i, text := range []string{"/review", "stop and rebase"} {
				if _, err := store.Queue(Action{ID: fmt.Sprintf("m%d", i+1), Kind: "message", Text: text}); err != nil {
					t.Fatal(err)
				}
			}
			pastUndoWindow(t, store)
			if err := store.ProcessOne(context.Background(), service.execute); !errors.Is(err, ErrDeferred) {
				t.Fatalf("the command's delivery = %v; want it held for the turn's end", err)
			}
			command, behind := actionOf(store, "m1"), actionOf(store, "m2")

			// Act
			pressed := sendNow(service, "", "m2")
			err := store.ProcessOne(context.Background(), service.execute)
			input := terminal.waitForLines(t, 3)
			sent := actionOf(store, "m2")

			// Assert
			if command.Status != "queued" || command.CanSendNow || command.Message != "Queued. "+commandWaits {
				t.Errorf("the command = %+v; want it waiting on the board for the turn's end, with no Send now", command)
			}
			if behind.Status != "queued" || !behind.CanSendNow || behind.Message != "Queued until the CEO's turn ends. Send now has it take the message at once." {
				t.Errorf("the message behind it = %+v; want it waiting with Send now", behind)
			}
			if pressed.Code != 202 || err != nil || !isHarnessHeld(sent) || sent.Message != sentNowInTurn {
				t.Errorf("Send now = %d %s, delivery = %v, action = %+v; want it typed into the turn and sent now", pressed.Code, pressed.Body, err, sent)
			}
			if !slices.Equal(input[1:], []string{"queued stop and rebase", test.wantKey}) {
				t.Errorf("terminal input = %q; want his message alone in the turn's queue as its send-now key went", input)
			}
			if still := actionOf(store, "m1"); still.Status != "queued" {
				t.Errorf("the command after Send now = %+v; want it still waiting for the turn's end", still)
			}
		})
	}
}

// The board takes a queue back only while the worker runs no delivery to that
// session: one under way may be typing onto its prompt line, or have typed a
// line the board has yet to record, which would come back with the queue as
// words the board does not know.
func TestAQueueIsNotTakenBackWhileADeliveryToItsSessionRuns(t *testing.T) {
	for _, test := range []struct {
		name     string
		isGoblin bool
	}{
		{name: "the CEO's"},
		{name: "a goblin's", isGoblin: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, service, terminal, task, _ := heldQueue(t, test.isGoblin, "claude-busy", "first words")
			store.mu.Lock()
			store.db.Actions = append(store.db.Actions, Action{ID: "m2", Kind: "message", TaskID: task, Text: "second words", Status: "running"})
			store.trying = []string{"m2"}
			store.mu.Unlock()

			// Act
			cancel := pressOnQueued(service, "message_withdraw", task, "m1", "first words")

			// Assert
			if cancel.Code != 409 || !strings.Contains(cancel.Body.String(), "The board is typing for") {
				t.Errorf("Cancel = %d %s; want it refused while the board types for that session", cancel.Code, cancel.Body)
			}
			if !isHarnessHeld(actionOf(store, "m1")) {
				t.Errorf("after the press = %+v; want it still held in its harness's queue", actionOf(store, "m1"))
			}
			if input := terminal.lines(t); slices.ContainsFunc(input, func(line string) bool { return strings.HasPrefix(line, "took back ") }) {
				t.Errorf("terminal input = %q; want no key pressed", input)
			}
		})
	}
}
