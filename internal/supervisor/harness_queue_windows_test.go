package supervisor

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/monitor"
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
// screens of its harness, name.
func promptLineEmpties(t *testing.T, terminal hostedTerminal, name string) {
	t.Helper()
	record, err := host.ReadRecord(terminal.stateDir, terminal.id)
	if err != nil {
		t.Fatal(err)
	}
	screens, _ := harness.NativeScreens(harness.Kind(name))
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

// tryNext has the board's worker try the next message for the CEO, then waits
// until the prompt line of his harness, name, reads empty. A line typed into
// his turn is held as soon as its submit key goes, before his harness has
// taken it off its prompt line, so a try right after it can read that line
// still there and hold the next message as one his words block, with no Send
// now, until the worker tries that message again.
func tryNext(t *testing.T, store *Store, service *Service, terminal hostedTerminal, name string) {
	t.Helper()
	if err := store.ProcessOne(context.Background(), service.execute); err != nil && !errors.Is(err, ErrDeferred) {
		t.Fatal(err)
	}
	promptLineEmpties(t, terminal, name)
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

// The Overlord, 2026-11-12: "thes already sent in termianl but are shwoing
// queued in mdoern caht fix root synch" (#1915). Two messages he pasted for
// the CEO in a turn held tabs and a word joiner, U+2060, which draws nothing.
// Claude Code 2.1.322 took both into that turn and recorded them as it had
// cleaned them, each tab four spaces and the word joiner gone, so the record
// never held the words the board typed character for character: they read
// Queued, then Not confirmed, and his Send again typed both a second time.
// Such a message is delivered once the record shows its turn took the words
// a reader sees, and the board types it once.
func TestAPasteItsHarnessClearedOfWhatDrawsNothingIsDeliveredOnceItsTurnTakesIt(t *testing.T) {
	// Arrange
	records := useConversations(t)
	store, home := testStore(t)
	terminal := nativeCEOComposer(t, home.State, "claude-busy")
	service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
	pasted := "Level\terror: the search for project=f\u2060a1 failed." + strings.Repeat(" The same alert came again.", 20)
	if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: pasted}); err != nil {
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
	takenInRecord(t, records, "claude", "session-1", "", strings.NewReplacer("\t", "    ", "\u2060", "").Replace(pasted))
	settleErr := errors.Join(store.settleDeliveries(time.Now().UTC(), service.lookAtTerminal), service.submitHandedBack(context.Background()))
	taken := actionOf(store, "m1")

	// Assert
	if err != nil || !isHarnessHeld(held) || !held.Awaiting.Pasted || held.Awaiting.Typed != pasted {
		t.Fatalf("delivery = %v, action = %+v; want his words pasted into the CEO's turn as he wrote them", err, held)
	}
	if settleErr != nil || taken.Status != "succeeded" || taken.Awaiting != nil {
		t.Errorf("settle = %v, action = %+v; want it delivered once the CEO's record shows the words taken", settleErr, taken)
	}
	if input := strings.Join(terminal.lines(t), "\n"); strings.Count(input, "queued Level") != 1 {
		t.Errorf("terminal input = %q; want the message typed once", input)
	}
}

// #1915: a build before that fix stored each delivery under a name made from
// its text character for character, a word joiner included, and four of the
// Overlord's messages stored so read Queued or Not confirmed on 2026-11-12
// after the CEO's session had taken them. A delivery stored that way is found
// once this build runs, by the words it stored as typed, read as a reader
// sees them, so it reads Sent whichever of the two it read. One whose words
// the record does not show stays as it was.
func TestADeliveryStoredUnderItsTextCharacterForCharacterIsStillFoundTaken(t *testing.T) {
	typed := "Level\terror: the search for project=f⁠a1 failed."
	taken := strings.NewReplacer("\t", "    ", "⁠", "").Replace(typed)
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(typed), " ")))
	stored := hex.EncodeToString(sum[:])
	if stored == monitor.TextDigest(typed) {
		t.Fatal("the name stored is the one this build gives the text; want the one a build before it gave")
	}
	for _, test := range []struct {
		name     string
		status   string
		recorded string
		want     string
	}{
		{name: "one that read Queued", status: "running", recorded: taken, want: "succeeded"},
		{name: "one that read Not confirmed", status: "uncertain", recorded: taken, want: "succeeded"},
		{name: "one whose words the record does not show", status: "uncertain", recorded: strings.Replace(taken, "=fa1", "=fb1", 1), want: "uncertain"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			records := useConversations(t)
			store, _ := testStore(t)
			since := time.Now().UTC().Add(-time.Minute)
			store.db.Actions = append(store.db.Actions, Action{ID: "m1", Kind: "message", Text: typed, Status: test.status, CreatedAt: since, UpdatedAt: since,
				Awaiting: &Awaiting{Host: "ceo", Harness: "claude", Since: since, QuietSince: since, Digest: stored, Session: "session-1", Typed: typed, Pasted: true, IsHeld: true}})
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			takenInRecord(t, records, "claude", "session-1", "", test.recorded)

			// Act
			err := store.settleDeliveries(time.Now().UTC(), (&Service{Store: store}).lookAtTerminal)
			after := actionOf(store, "m1")

			// Assert
			if err != nil || after.Status != test.want || (after.Awaiting == nil) != (test.want == "succeeded") {
				t.Errorf("settle = %v, action = %+v; want it %s", err, after, test.want)
			}
		})
	}
}

// #1915, the CEO's done-when of 2026-11-12: "Cancel and Edit on a message the
// session already took say so plainly and leave no stuck bubble." A message
// its turn took is in no queue, and a press can land before the board's next
// look at the session's record. The board looks then: he reads that it was
// taken and the message reads delivered at once, rather than that its prompt
// line offers no queue and to press again.
func TestCancelOfAMessageItsSessionAlreadyTookSaysSoAndLeavesItDelivered(t *testing.T) {
	for _, test := range []struct {
		name     string
		isGoblin bool
	}{
		{name: "the CEO's"},
		{name: "a goblin's", isGoblin: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: its turn took the message at his Send now, and its
			// record shows that before the board's next look.
			store, service, terminal, task, typed := heldQueue(t, test.isGoblin, "claude-busy", "stop and rebase")
			promptLineEmpties(t, terminal, "claude")
			if pressed := sendNow(service, task, "m1"); pressed.Code != 202 {
				t.Fatalf("Send now = %d %s", pressed.Code, pressed.Body)
			}
			terminal.waitForLines(t, 3)
			session, dir := "session-1", ""
			if test.isGoblin {
				meta, err := state.ReadTaskMeta(store.Home.State, task)
				if err != nil {
					t.Fatal(err)
				}
				session, dir = "", meta.Worktree
			}
			takenInRecord(t, os.Getenv("USERPROFILE"), "claude", session, dir, typed("stop and rebase"))

			// Act
			cancel := pressOnQueued(service, "message_withdraw", task, "m1", "stop and rebase")
			after := actionOf(store, "m1")

			// Assert
			if cancel.Code != 409 || !strings.Contains(cancel.Body.String(), "already took it.") {
				t.Errorf("Cancel = %d %s; want it to say the session already took the message", cancel.Code, cancel.Body)
			}
			if after.Status != "succeeded" || after.Awaiting != nil {
				t.Errorf("after the press = %+v; want the message delivered, as the session's record shows", after)
			}
			if input := terminal.lines(t); slices.ContainsFunc(input, func(line string) bool { return strings.HasPrefix(line, "took back ") || line == "cleared" }) {
				t.Errorf("terminal input = %q; want no key pressed in a session that already took it", input)
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
			promptLineEmpties(t, terminal, "claude")
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

// The Overlord, 2026-11-13, of a message held in the CEO's queue when the
// machine restarted, whose Cancel the board refused again and again: "clear
// out this garbage message" (#2541). A terminal started again since the board
// typed a message into its turn, the CEO's after a restart as a goblin's after
// a switch, holds no queue with the message in it, so the message reads Not
// sent, saying so, and his Cancel takes it off the board with no key pressed
// in the new terminal.
func TestAHeldMessageWhoseTerminalStartedAgainReadsNotSentAndHisCancelRemovesIt(t *testing.T) {
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
			replacement := startedAgain(t, terminal, task, "claude-busy")

			// Act
			settleErr := store.settleDeliveries(time.Now().UTC(), service.lookAtTerminal)
			lost := actionOf(store, "m1")
			cancel := pressOnQueued(service, "message_withdraw", task, "m1", "first words")

			// Assert
			if settleErr != nil {
				t.Fatal(settleErr)
			}
			if lost.Status != "failed" || !strings.HasPrefix(lost.Message, "Not sent.") || !strings.Contains(lost.Message, "started again") {
				t.Errorf("after its terminal started again, the message = %+v; want it Not sent, saying the restart lost it", lost)
			}
			if cancel.Code != 202 || slices.ContainsFunc(store.Snapshot().Actions, func(a Action) bool { return a.ID == "m1" }) {
				t.Errorf("Cancel = %d %s, actions = %+v; want the message taken off the board", cancel.Code, cancel.Body, store.Snapshot().Actions)
			}
			if input := replacement.lines(t); len(input) != 1 {
				t.Errorf("the new terminal's input = %q; want no key pressed in it", input)
			}
		})
	}
}

// Send now on a held message presses its harness's send-now key only in the
// terminal the board typed the message into (#2541). One started again since
// holds no queue with the message in it, and the key there would send at once
// what he queued in it himself.
func TestSendNowOnAHeldMessageWhoseTerminalStartedAgainPressesNoKey(t *testing.T) {
	for _, test := range []struct {
		name     string
		isGoblin bool
	}{
		{name: "the CEO's"},
		{name: "a goblin's", isGoblin: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			_, service, terminal, task, _ := heldQueue(t, test.isGoblin, "claude-busy", "first words")
			replacement := startedAgain(t, terminal, task, "claude-busy")
			replacement.typeLine(t, "his own words")
			if lines := replacement.waitForLines(t, 2); len(lines) != 2 || lines[1] != "queued his own words" {
				t.Fatalf("the new terminal's input = %q; want his own words queued in its turn", lines)
			}
			promptLineEmpties(t, replacement, "claude")

			// Act
			pressed := sendNow(service, task, "m1")

			// Assert
			if pressed.Code != 409 || !strings.Contains(pressed.Body.String(), "send-now key was not pressed") {
				t.Errorf("Send now = %d %s; want it refused, saying no send-now key went", pressed.Code, pressed.Body)
			}
			if input := replacement.lines(t); slices.Contains(input, "sent now") {
				t.Errorf("the new terminal's input = %q; want no send-now key pressed in it", input)
			}
		})
	}
}

// The Overlord, 2026-11-13, of two of his messages that read Not confirmed
// for hours after the CEO's session had taken them: "Stale old messages that
// appear randomly when memory gets low. Why do they exist? They should be
// deleted." (#2541). A message submitted to a session reads Not confirmed once
// its terminal is gone before the session's record shows it taken, and stays
// watched, so a record that shows the take later, as one written late on a
// loaded machine does, settles it as Sent.
func TestANotConfirmedMessageWhoseRecordShowsItTakenLaterReadsSent(t *testing.T) {
	// Arrange
	records := useConversations(t)
	store, _ := testStore(t)
	since := time.Now().UTC().Add(-time.Minute)
	text := "rebase onto main"
	store.db.Actions = append(store.db.Actions, Action{ID: "m1", Kind: "message", Text: text, Status: "running", CreatedAt: since, UpdatedAt: since,
		Awaiting: &Awaiting{Host: "ceo", Harness: "claude", Since: since, QuietSince: since, Digest: monitor.TextDigest(text), Session: "session-1", Typed: text}})
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	look := (&Service{Store: store}).lookAtTerminal
	if err := store.settleDeliveries(time.Now().UTC(), look); err != nil {
		t.Fatal(err)
	}
	if gone := actionOf(store, "m1"); gone.Status != "uncertain" {
		t.Fatalf("with its terminal gone, the message = %+v; want it Not confirmed", gone)
	}
	takenInRecord(t, records, "claude", "session-1", "", text)

	// Act
	err := store.settleDeliveries(time.Now().UTC(), look)
	after := actionOf(store, "m1")

	// Assert
	if err != nil || after.Status != "succeeded" {
		t.Errorf("settle = %v, the message = %+v; want it Sent once its record shows it taken", err, after)
	}
}

// The Overlord, 2026-11-13, of messages that read Not confirmed with no
// control that clears them: "There's no way to clear em out." (#2541). Cancel
// on a message that reads Not confirmed takes it off the board, the CEO's and
// a goblin's alike, as it takes one that reads Not sent.
func TestCancelOfANotConfirmedMessageTakesItOffTheBoard(t *testing.T) {
	for _, task := range []string{"", "task-1"} {
		t.Run(cmp.Or(task, "the CEO"), func(t *testing.T) {
			// Arrange
			store, home := testStore(t)
			service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}}}
			since := time.Now().UTC().Add(-time.Hour)
			store.db.Actions = append(store.db.Actions, Action{ID: "m1", Kind: "message", TaskID: task, Text: "stop and rebase", Status: "uncertain", Message: "Supervisor stopped during delivery. Inspect the task before sending again.", CreatedAt: since, UpdatedAt: since})
			if err := store.save(); err != nil {
				t.Fatal(err)
			}

			// Act
			cancel := pressOnQueued(service, "message_withdraw", task, "m1", "stop and rebase")

			// Assert
			if cancel.Code != 202 || slices.ContainsFunc(store.Snapshot().Actions, func(a Action) bool { return a.ID == "m1" }) {
				t.Errorf("Cancel = %d %s, actions = %+v; want the message taken off the board", cancel.Code, cancel.Body, store.Snapshot().Actions)
			}
		})
	}
}

// startedAgain ends terminal, whose harness holds the board's messages to task,
// or to the CEO for none, and starts the fixture harness of mode in its place:
// a restart of the machine starts the CEO's terminal again, and a switch a
// goblin's, in a spawn generation of its own.
func startedAgain(t *testing.T, terminal hostedTerminal, task, mode string) hostedTerminal {
	t.Helper()
	record, err := host.ReadRecord(terminal.stateDir, terminal.id)
	if err != nil {
		t.Fatal(err)
	}
	client, err := host.Dial(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(client.CloseTerminal(), client.Close()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-terminal.ended:
	case <-time.After(time.Minute):
		t.Fatal("the terminal did not end")
	}
	if task != "" {
		meta, err := state.ReadTaskMeta(terminal.stateDir, task)
		if err != nil {
			t.Fatal(err)
		}
		meta.SpawnGen = "g2"
		if err := state.WriteTaskMeta(terminal.stateDir, meta); err != nil {
			t.Fatal(err)
		}
	}
	return nativeComposer(t, terminal.stateDir, terminal.id, mode)
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
// whatever its harness, with a Send now of its own (#1915), and Send now on
// the message behind it types that message into the turn ahead of it and
// presses the key over it alone.
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
			if command.Status != "queued" || !command.CanSendNow || command.Message != "Queued. A command runs when its turn ends. Send now interrupts the turn to run it." {
				t.Errorf("the command = %+v; want it waiting on the board for the turn's end, with a Send now of its own", command)
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

// Send now on a command held for the CEO's turn has its harness run it at
// once, as a goblin's does (#1915): Claude Code takes the command into its
// turn's queue and runs it at once with its send-now chord, and Codex, which
// refuses such a command in a turn, ends its turn on Escape, so the command
// goes at the prompt line the turn's end leaves.
func TestSendNowRunsACommandHeldForTheCEOsTurnAtOnce(t *testing.T) {
	for _, test := range []struct {
		name, mode string
		wantInput  []string
	}{
		{name: "Claude Code", mode: "claude-busy", wantInput: []string{"queued /compact", "sent now"}},
		{name: "Codex", mode: "busy", wantInput: []string{"interrupted", "accepted /compact"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, test.mode)
			service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}}}
			if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "/compact"}); err != nil {
				t.Fatal(err)
			}
			pastUndoWindow(t, store)
			if err := store.ProcessOne(context.Background(), service.execute); !errors.Is(err, ErrDeferred) {
				t.Fatalf("the command's delivery = %v; want it held for the turn's end", err)
			}
			held := actionOf(store, "m1")

			// Act
			pressed := sendNow(service, "", "m1")
			err := store.ProcessOne(context.Background(), service.execute)
			input := terminal.waitForLines(t, 1+len(test.wantInput))
			sent := actionOf(store, "m1")

			// Assert
			if !held.CanSendNow || held.Message != "Queued. A command runs when its turn ends. Send now interrupts the turn to run it." {
				t.Errorf("the held command = %+v; want Send now offered, saying it interrupts the turn to run the command", held)
			}
			if pressed.Code != 202 || err != nil || sent.Status != "succeeded" {
				t.Errorf("Send now = %d %s, delivery = %v, command = %+v; want the command run at once", pressed.Code, pressed.Body, err, sent)
			}
			if !slices.Equal(input[1:], test.wantInput) {
				t.Errorf("terminal input = %q; want %q after its registration", input, test.wantInput)
			}
		})
	}
}

// Claude Code's send-now chord takes its turn's queue from the head, so the
// board presses it over a command only while that queue holds nothing else.
// A Send now that finds lines queued ahead of the command is spent by that
// try, which says it could not run it at once, and the board presses nothing
// for it at a later try: one press of his is one try, never a standing order
// to interrupt whatever turn runs next (#1915). Those words stay until a new
// Send now, whose try says how it went, or until the turn ends and the
// command runs. The fixture's turn ends only on Escape, which hands his words
// back onto its prompt line, so he ends it here as he can in its terminal,
// with Escape and then Ctrl+U over those words, which leaves the board the
// empty, ready prompt line it finds at any turn's end.
func TestASendNowThatCannotRunAHeldCommandAtOnceIsSpentAndSaysSo(t *testing.T) {
	// Arrange
	useConversations(t)
	store, home := testStore(t)
	terminal := nativeCEOComposer(t, home.State, "claude-busy")
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}}}
	for _, message := range []Action{{ID: "m1", Kind: "message", Text: "first words"}, {ID: "m2", Kind: "message", Text: "/compact"}} {
		if _, err := store.Queue(message); err != nil {
			t.Fatal(err)
		}
		pastUndoWindow(t, store)
		tryNext(t, store, service, terminal, "claude")
	}
	if words, command := actionOf(store, "m1"), actionOf(store, "m2"); !isHarnessHeld(words) || command.Status != "queued" {
		t.Fatalf("his words = %+v, the command = %+v; want the words in the turn's queue and the command held on the board", words, command)
	}
	retry := func() error {
		store.mu.Lock()
		store.deferredUntil["m2"] = time.Now().Add(-time.Second)
		store.mu.Unlock()
		return store.ProcessOne(context.Background(), service.execute)
	}

	// Act
	pressed := sendNow(service, "", "m2")
	err := store.ProcessOne(context.Background(), service.execute)
	spent := actionOf(store, "m2")
	later := retry()
	kept := actionOf(store, "m2")
	pressedAgain := sendNow(service, "", "m2")
	renewed := actionOf(store, "m2")
	again := store.ProcessOne(context.Background(), service.execute)
	spentAgain := actionOf(store, "m2")
	terminal.typeKeys(t, "\x1b")
	terminal.waitForLines(t, 3)
	terminal.typeKeys(t, "\x15")
	terminal.waitForLines(t, 4)
	terminal.showsComposer(t, "claude")
	ended := retry()
	taken := actionOf(store, "m2")
	input := terminal.waitForLines(t, 5)

	// Assert
	if pressed.Code != 202 || !errors.Is(err, ErrDeferred) || !errors.Is(later, ErrDeferred) {
		t.Fatalf("Send now = %d %s, its try = %v, the next try = %v; want it taken, and the command held at each try", pressed.Code, pressed.Body, err, later)
	}
	if spent.IsSentNow || !spent.CanSendNow || spent.Message != "Queued. Send now could not run it at once, so it runs when its turn ends." {
		t.Errorf("after its try = %+v; want Send now spent, offered again, and words that say it could not run the command at once", spent)
	}
	if kept.IsSentNow || !kept.CanSendNow || kept.Message != "Queued. Send now could not run it at once, so it runs when its turn ends." {
		t.Errorf("after the next try = %+v; want Send now still offered, and the words that say it could not run the command at once kept", kept)
	}
	if pressedAgain.Code != 202 || !renewed.IsSentNow || renewed.IsSendNowSpent || renewed.Message != "Queued. A command runs when its turn ends. Send now interrupts the turn to run it." {
		t.Errorf("a new Send now = %d %s, the command before its try = %+v; want it taken, and words that say what Send now does in place of what the last one could not do", pressedAgain.Code, pressedAgain.Body, renewed)
	}
	if !errors.Is(again, ErrDeferred) || spentAgain.IsSentNow || !spentAgain.CanSendNow || spentAgain.Message != "Queued. Send now could not run it at once, so it runs when its turn ends." {
		t.Errorf("its try = %v, the command = %+v; want it held, its Send now spent, and words that say this one could not run it at once either", again, spentAgain)
	}
	if ended != nil || taken.Status != "succeeded" || taken.Message != "Taken by the CEO in its native terminal, which ran your command." || taken.IsSendNowSpent {
		t.Errorf("once its turn ended, its try = %v, the command = %+v; want it run, read as taken, and no longer marked spent", ended, taken)
	}
	if !slices.Equal(input[1:], []string{"queued first words", "interrupted", "cleared", "accepted /compact"}) {
		t.Errorf("terminal input = %q; want nothing typed or pressed for the command until its turn ended", input)
	}
}

// A command held for the CEO's turn behind another keeps saying that his
// spent Send now could not run it at once, with its Send now offered,
// whenever the board gives it words again: at the next try of the command
// ahead of it, and when he queues another message (#1915).
func TestACommandWaitingBehindAnotherKeepsTheWordsOfItsSpentSendNow(t *testing.T) {
	// Arrange
	useConversations(t)
	store, home := testStore(t)
	terminal := nativeCEOComposer(t, home.State, "claude-busy")
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}}}
	for _, message := range []Action{{ID: "m1", Kind: "message", Text: "first words"}, {ID: "m2", Kind: "message", Text: "/compact"}, {ID: "m3", Kind: "message", Text: "/clear"}} {
		if _, err := store.Queue(message); err != nil {
			t.Fatal(err)
		}
	}
	pastUndoWindow(t, store)
	for range 2 {
		tryNext(t, store, service, terminal, "claude")
	}
	if words, ahead, behind := actionOf(store, "m1"), actionOf(store, "m2"), actionOf(store, "m3"); !isHarnessHeld(words) || ahead.Status != "queued" || behind.Status != "queued" || !behind.CanSendNow {
		t.Fatalf("his words = %+v, the command ahead = %+v, the one behind it = %+v; want the words in the turn's queue and both commands held on the board, the one behind with Send now", words, ahead, behind)
	}
	deferAhead := func(until time.Time) {
		store.mu.Lock()
		store.deferredUntil["m2"] = until
		store.mu.Unlock()
	}

	// Act
	deferAhead(time.Now().Add(time.Hour))
	pressed := sendNow(service, "", "m3")
	err := store.ProcessOne(context.Background(), service.execute)
	spent := actionOf(store, "m3")
	deferAhead(time.Now().Add(-time.Second))
	aheadTried := store.ProcessOne(context.Background(), service.execute)
	afterAhead := actionOf(store, "m3")
	if _, queueErr := store.Queue(Action{ID: "m4", Kind: "message", Text: "more words"}); queueErr != nil {
		t.Fatal(queueErr)
	}
	afterQueued := actionOf(store, "m3")

	// Assert
	if pressed.Code != 202 || !errors.Is(err, ErrDeferred) || !errors.Is(aheadTried, ErrDeferred) {
		t.Fatalf("Send now = %d %s, its try = %v, the try of the command ahead = %v; want it taken, and both commands held", pressed.Code, pressed.Body, err, aheadTried)
	}
	for _, step := range []struct {
		name    string
		command Action
	}{{"after its try", spent}, {"after the try of the command ahead", afterAhead}, {"after another message was queued", afterQueued}} {
		if step.command.IsSentNow || !step.command.CanSendNow || step.command.Message != "Queued. Send now could not run it at once, so it runs when its turn ends." {
			t.Errorf("%s, the command behind = %+v; want Send now offered, and words that say his could not run it at once", step.name, step.command)
		}
	}
	if input := terminal.lines(t); !slices.Equal(input[1:], []string{"queued first words"}) {
		t.Errorf("terminal input = %q; want nothing typed or pressed for either command", input)
	}
}

// A command held for the CEO's turn behind another keeps its Send now and its
// words when he queues another message, which takes words of its own and gives
// none to those queued before it, so a Send now he presses on the command then
// is taken, never refused as a command that runs when its turn ends (#1915).
func TestACommandHeldBehindAnotherKeepsItsSendNowWhenHeQueuesAnotherMessage(t *testing.T) {
	// Arrange
	useConversations(t)
	store, home := testStore(t)
	terminal := nativeCEOComposer(t, home.State, "claude-busy")
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}}}
	for _, message := range []Action{{ID: "m1", Kind: "message", Text: "first words"}, {ID: "m2", Kind: "message", Text: "/compact"}, {ID: "m3", Kind: "message", Text: "/clear"}} {
		if _, err := store.Queue(message); err != nil {
			t.Fatal(err)
		}
	}
	pastUndoWindow(t, store)
	for range 2 {
		tryNext(t, store, service, terminal, "claude")
	}
	if behind := actionOf(store, "m3"); behind.Status != "queued" || !behind.CanSendNow || behind.Message != "Queued. A command runs when its turn ends. Send now interrupts the turn to run it." {
		t.Fatalf("the command behind = %+v; want it held on the board for the turn's end, with a Send now of its own", behind)
	}

	// Act
	_, queueErr := store.Queue(Action{ID: "m4", Kind: "message", Text: "more words"})
	kept := actionOf(store, "m3")
	pressed := sendNow(service, "", "m3")

	// Assert
	if queueErr != nil {
		t.Fatal(queueErr)
	}
	if !kept.CanSendNow || kept.SendNowWhy != "" || kept.Message != "Queued. A command runs when its turn ends. Send now interrupts the turn to run it." {
		t.Errorf("after another message was queued, the command behind = %+v; want its Send now and its words kept", kept)
	}
	if pressed.Code != 202 {
		t.Errorf("Send now = %d %s; want it taken", pressed.Code, pressed.Body)
	}
}

// A spent Send now's words give way to another reason a command held for the
// CEO's turn cannot go now only while that reason lasts (#1915): words he
// types onto the CEO's prompt line hold it with no Send now, and once he
// clears them it waits for the turn with Send now on again, saying again that
// his Send now could not run it at once.
func TestAnotherReasonAHeldCommandCannotGoShowsInPlaceOfItsSpentSendNowOnlyWhileItLasts(t *testing.T) {
	// Arrange
	useConversations(t)
	store, home := testStore(t)
	terminal := nativeCEOComposer(t, home.State, "claude-busy")
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}}}
	for _, message := range []Action{{ID: "m1", Kind: "message", Text: "first words"}, {ID: "m2", Kind: "message", Text: "/compact"}} {
		if _, err := store.Queue(message); err != nil {
			t.Fatal(err)
		}
		pastUndoWindow(t, store)
		tryNext(t, store, service, terminal, "claude")
	}
	if pressed := sendNow(service, "", "m2"); pressed.Code != 202 {
		t.Fatalf("Send now = %d %s; want it taken", pressed.Code, pressed.Body)
	}
	if err := store.ProcessOne(context.Background(), service.execute); !errors.Is(err, ErrDeferred) {
		t.Fatalf("its try = %v; want the command held", err)
	}
	if spent := actionOf(store, "m2"); spent.Message != "Queued. Send now could not run it at once, so it runs when its turn ends." {
		t.Fatalf("after its try = %+v; want words that say his Send now could not run it at once", spent)
	}
	retry := func() error {
		store.mu.Lock()
		store.deferredUntil["m2"] = time.Now().Add(-time.Second)
		store.mu.Unlock()
		return store.ProcessOne(context.Background(), service.execute)
	}

	// Act
	typeIntoCEO(t, home.State, "a draft", "a draft")
	blocked := retry()
	held := actionOf(store, "m2")
	typeIntoCEO(t, home.State, "\x15", "Press up to edit queued messages")
	back := retry()
	again := actionOf(store, "m2")

	// Assert
	if !errors.Is(blocked, ErrDeferred) || held.CanSendNow || held.SendNowWhy != "Its terminal is not ready to take it yet." || held.Message != "The CEO's input is not empty and ready. Your answer is queued; nothing was sent." {
		t.Errorf("its try over his words = %v, the command = %+v; want it held with no Send now, saying why", blocked, held)
	}
	if !errors.Is(back, ErrDeferred) || !again.CanSendNow || again.Message != "Queued. Send now could not run it at once, so it runs when its turn ends." {
		t.Errorf("its try once he cleared them = %v, the command = %+v; want Send now offered, and words that say his could not run it at once", back, again)
	}
	if input := terminal.lines(t); !slices.Equal(input[1:], []string{"queued first words", "cleared"}) {
		t.Errorf("terminal input = %q; want nothing typed or pressed for the command", input)
	}
}

// Send now on a command held for the CEO's turn reads as run only once its
// harness's key ran it (#1915). A Codex whose Escape ended its turn by handing
// the line it queued back onto its prompt line leaves no empty prompt line, so
// the command is never typed and the Send now is spent and says so. A Claude
// Code that takes no Ctrl+X Ctrl+S leaves the command in its turn's queue,
// typed and sent now, for the CEO's record to show it taken. One whose turn
// asks a question of its own as the command goes into its queue is pressed no
// key at all, since the key would go to that question, and the command waits
// in the turn's queue, typed and not sent now, as his words there do.
func TestASendNowWhoseKeyDidNotRunTheCEOsCommandSaysWhereItWaits(t *testing.T) {
	for _, test := range []struct {
		name, mode  string
		harness     string
		before      []string
		wantErr     error
		wantStatus  string
		wantMessage string
		isAwaited   bool
		wantInput   []string
	}{
		{name: "a Codex that hands its queue back onto its prompt line", mode: "busy", harness: "codex", before: []string{"first words"}, wantErr: ErrDeferred, wantStatus: "queued", wantMessage: "Queued. Send now could not run it at once, so it runs when its turn ends.", wantInput: []string{"queued first words", "interrupted"}},
		{name: "a Claude Code that ignores its send-now chord", mode: "claude-busy-ignores-send-now", harness: "claude", wantStatus: "running", wantMessage: "Typed while it was working and sent now.", isAwaited: true, wantInput: []string{"queued /compact", "send now ignored"}},
		{name: "a Claude Code whose turn asks a question over its queue", mode: "claude-busy-asks", harness: "claude", wantStatus: "running", wantMessage: heldByCEO, isAwaited: true, wantInput: []string{"queued /compact"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, test.mode)
			service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}}}
			command := fmt.Sprintf("m%d", len(test.before)+1)
			for i, text := range append(test.before, "/compact") {
				if _, err := store.Queue(Action{ID: fmt.Sprintf("m%d", i+1), Kind: "message", Text: text}); err != nil {
					t.Fatal(err)
				}
				pastUndoWindow(t, store)
				tryNext(t, store, service, terminal, test.harness)
			}
			if held := actionOf(store, command); held.Status != "queued" || !held.CanSendNow {
				t.Fatalf("the command = %+v; want it held on the board with Send now", held)
			}

			// Act
			pressed := sendNow(service, "", command)
			err := store.ProcessOne(context.Background(), service.execute)
			sent := actionOf(store, command)
			input := terminal.lines(t)

			// Assert
			if pressed.Code != 202 || !errors.Is(err, test.wantErr) {
				t.Fatalf("Send now = %d %s, its try = %v; want it taken, and its try to end with %v", pressed.Code, pressed.Body, err, test.wantErr)
			}
			if sent.Status != test.wantStatus || (sent.Awaiting != nil) != test.isAwaited || sent.Message != test.wantMessage {
				t.Errorf("after its try = %+v; want it %s, awaited %t, reading %q", sent, test.wantStatus, test.isAwaited, test.wantMessage)
			}
			if !slices.Equal(input[1:], test.wantInput) {
				t.Errorf("terminal input = %q; want %q after its registration", input, test.wantInput)
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
