package supervisor

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
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
// into a turn. The fixture records a line queued before it draws its queue,
// so each is waited for on the screen too, where the board reads it.
func heldQueue(t *testing.T, isGoblin bool, mode string, texts ...string) (*Store, *Service, hostedTerminal, string, func(string) string) {
	t.Helper()
	useConversations(t)
	store, home := testStore(t)
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}}}
	task, typed := "", func(text string) string { return text }
	name := "codex"
	if strings.HasPrefix(mode, "claude-") {
		name = "claude"
	}
	if strings.HasPrefix(mode, "pi-") {
		name = "pi"
	}
	var meta state.TaskMeta
	if isGoblin {
		meta = makeNative(t, home.State, "task-1")
		meta.Harness = name
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
		promptLineEmpties(t, terminal, name)
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
		kind     harness.Kind
		wantKey  string
	}{
		{name: "the CEO's Claude Code", mode: "claude-busy", kind: harness.Claude, wantKey: "sent now"},
		{name: "a goblin's Claude Code", isGoblin: true, mode: "claude-busy", kind: harness.Claude, wantKey: "sent now"},
		{name: "the CEO's Codex", mode: "busy", kind: harness.Codex, wantKey: "interrupted"},
		{name: "the CEO's pi", mode: "pi-busy", kind: harness.Pi, wantKey: "interrupted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, service, terminal, task, _ := heldQueue(t, test.isGoblin, test.mode, "first words", "second words")
			before := actionOf(store, "m2").UpdatedAt
			record, err := host.ReadRecord(terminal.stateDir, terminal.id)
			if err != nil {
				t.Fatal(err)
			}
			screens, _ := harness.NativeScreens(test.kind)

			// Act
			pressed := sendNow(service, task, "m2")
			terminal.waitForLines(t, 4)
			// The fixture draws what its key did only after it records the
			// key, and a press reads the screen, so the second waits until
			// it shows the turn ended or its queue no longer offered.
			for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
				if screen, err := host.ReadScreen(record); err == nil && (!screens.IsWorking(screen) || screens.Queued != nil && !screens.OffersQueue(screen)) {
					break
				}
			}
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

// pi takes a line typed into its turn at its next tool call, and the board
// sees that only in pi's record, at its next look, so Send now can find the
// message still held while pi's turn runs over an empty editor that no longer
// offers its queue. pi's Escape would then end the turn that reads the
// message, so Send now presses it only while that queue is offered, as it
// presses Claude Code's send-now key, and leaves the message held (#2340).
func TestSendNowPressesNoEscapeWherePisTurnNoLongerOffersItsQueue(t *testing.T) {
	// Arrange
	store, service, terminal, task, _ := heldQueue(t, false, "pi-busy-takes-steers", "first words")

	// Act
	pressed := sendNow(service, task, "m1")
	after := actionOf(store, "m1")
	terminal.typeLine(t, "his own words")
	input := terminal.waitForLines(t, 3)

	// Assert
	if pressed.Code != 202 {
		t.Errorf("Send now = %d %s; want it taken", pressed.Code, pressed.Body)
	}
	if !isHarnessHeld(after) || after.Message != heldByCEO {
		t.Errorf("after Send now = %+v; want it still held in the CEO's session, with no send-now key pressed", after)
	}
	if !slices.Equal(input[1:], []string{"queued first words", "queued his own words"}) {
		t.Errorf("terminal input = %q; want no Escape, so its turn still queues the words typed after Send now", input)
	}
}

// #2332: a queued message "can be edited and deleted from Modern Chat and the
// change lands on the harness's own queued item". Claude Code gives its queue
// back only whole, every line onto the prompt line for one Up, and pi 1.4.0
// every line into its editor for one Alt+Q (#2340), so Cancel, which Edit
// sends too, takes that queue back, clears it off the prompt line unsubmitted,
// deletes the message he named, and types the others again in the order he
// sent them.
func TestCancelOfAMessageItsHarnessHoldsTakesItOutOfThatQueueAndKeepsTheOthers(t *testing.T) {
	for _, test := range []struct {
		name, mode, harness string
		isGoblin            bool
	}{
		{name: "the CEO's Claude Code", mode: "claude-busy", harness: "claude"},
		{name: "a goblin's Claude Code", mode: "claude-busy", harness: "claude", isGoblin: true},
		{name: "the CEO's pi", mode: "pi-busy", harness: "pi"},
		{name: "a goblin's pi", mode: "pi-busy", harness: "pi", isGoblin: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, service, terminal, task, typed := heldQueue(t, test.isGoblin, test.mode, "first words", "second words", "third words")

			// Act
			cancel := pressOnQueued(service, "message_withdraw", task, "m2", "second words")
			kept := []string{actionOf(store, "m1").Status, actionOf(store, "m3").Status}
			promptLineEmpties(t, terminal, test.harness)
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
// them. Codex 0.160 has no such key: it sends a line typed into a turn to its
// model at once, for its next tool call, and its Alt+Up gives back only a line
// queued with Tab (live on 2026-11-18, #2340). Only the refusal that lasts for
// as long as the queue stays as it is says so, Codex's, so the board's Up
// passes that queue, and asks again after a draft's, which passes once the
// prompt line is clear.
func TestAQueueTheBoardCannotTakeBackAloneIsNeverCleared(t *testing.T) {
	for _, test := range []struct {
		name, mode, before string
		kind               harness.Kind
		wantSays           string
		wantHeld           bool
		wantLasting        bool
	}{
		{name: "Codex, which gives its queue back to no key", mode: "busy", kind: harness.Codex, wantSays: "gives back to no key", wantHeld: true, wantLasting: true},
		{name: "his own draft on the prompt line", mode: "claude-busy", kind: harness.Claude, before: "a draft of his", wantSays: "does not offer its queue now", wantHeld: true},
		{name: "a line typed in the terminal in the same queue", mode: "claude-busy", kind: harness.Claude, before: "typed in the terminal\r", wantSays: "holds words the board cannot take back alone"},
		{name: "his own draft in pi's editor", mode: "pi-busy", kind: harness.Pi, before: "a draft of his", wantSays: "does not offer its queue now", wantHeld: true},
		{name: "a line typed in pi's terminal in the same queue", mode: "pi-busy", kind: harness.Pi, before: "typed in the terminal\r", wantSays: "holds words the board cannot take back alone"},
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
					promptLineEmpties(t, terminal, string(test.kind))
				} else {
					screens, _ := harness.NativeScreens(test.kind)
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
			if cancel.Code != 409 || !strings.Contains(cancel.Body.String(), test.wantSays) || strings.Contains(cancel.Body.String(), `"is_lasting":true`) != test.wantLasting {
				t.Errorf("Cancel = %d %s; want it refused saying %q, lasting %t", cancel.Code, cancel.Body, test.wantSays, test.wantLasting)
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

// pi 1.4.0's editor shows at most 30% of its terminal's rows, and no fewer
// than five, and scrolls the rest out of sight (pi-tui's Editor.render): seven
// of the stand-in's 24. Alt+Q would put the five lines its queue holds there
// in nine rows, an empty one between each two, and the board would read back
// only the last four, so it could not take them off, and all five would wait
// unsent in that editor. Cancel of one of them presses nothing, says why, for
// as long as the queue stays as it is, so the board's Up passes it, and
// leaves every one in pi's queue for its turn (#2340).
func TestCancelOfAMessageInAQueueTallerThanPisEditorPressesNoKey(t *testing.T) {
	// Arrange
	store, service, terminal, _, _ := heldQueue(t, false, "pi-busy", "first words", "second words", "third words", "fourth words", "fifth words")

	// Act
	cancel := pressOnQueued(service, "message_withdraw", "", "m2", "second words")

	// Assert
	if body := cancel.Body.String(); cancel.Code != 409 || !strings.Contains(body, "too short to show its queue whole") || !strings.Contains(body, "next tool call, or as its turn ends") || !strings.Contains(body, `"is_lasting":true`) {
		t.Errorf("Cancel = %d %s; want it refused for as long as the queue stays as it is, saying pi's editor is too short to show its queue whole and pi takes it in its turn", cancel.Code, cancel.Body)
	}
	for _, id := range []string{"m1", "m2", "m3", "m4", "m5"} {
		if after := actionOf(store, id); !isHarnessHeld(after) {
			t.Errorf("%s after the press = %+v; want it still held in pi's queue", id, after)
		}
	}
	if input := terminal.lines(t); slices.ContainsFunc(input, func(line string) bool { return strings.HasPrefix(line, "took back ") || line == "cleared" }) {
		t.Errorf("terminal input = %q; want no key pressed", input)
	}
}

// A harness gives its queue back only whole, so his words beside an answer the
// board typed into Claude Code's queue cannot be taken back alone for as long
// as that answer is there: Cancel of them presses nothing and says why, for as
// long as the queue stays as it is, so the board's Up passes it (#2340).
func TestCancelOfHisWordsBesideAnAnswerInClaudeCodesQueueIsRefusedForAsLongAsItLasts(t *testing.T) {
	// Arrange
	store, service, terminal, _, _ := heldQueue(t, false, "claude-busy", "first words")
	identity := registrationIdentity(t, terminal.stateDir)
	question := Question{ID: "held-question", Identity: identity, Text: "Choose", CreatedAt: time.Now().UTC()}
	if err := store.acceptQuestion(question); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queue(Action{ID: "held-answer", Kind: "ceo_answer", Generation: identity, QuestionID: question.ID, Text: "Reply received"}); err != nil {
		t.Fatal(err)
	}
	tryNext(t, store, service, terminal, "claude")
	if given := actionOf(store, "held-answer"); !isHarnessHeld(given) {
		t.Fatalf("the answer = %+v; want it in the turn's queue beside his words", given)
	}

	// Act
	cancel := pressOnQueued(service, "message_withdraw", "", "m1", "first words")

	// Assert
	if body := cancel.Body.String(); cancel.Code != 409 || !strings.Contains(body, "also holds an answer the board typed") || !strings.Contains(body, `"is_lasting":true`) {
		t.Errorf("Cancel = %d %s; want it refused for as long as the answer is in the queue, saying so", cancel.Code, cancel.Body)
	}
	if words := actionOf(store, "m1"); !isHarnessHeld(words) {
		t.Errorf("his words after the press = %+v; want them still in the turn's queue", words)
	}
	if input := terminal.lines(t); slices.ContainsFunc(input, func(line string) bool { return strings.HasPrefix(line, "took back ") || line == "cleared" }) {
		t.Errorf("terminal input = %q; want no key pressed", input)
	}
}

// writeQueueRecord writes, at conversation, the record Claude Code keeps of a
// session whose queue holds lines, oldest first, each as it records one
// queued during a turn.
func writeQueueRecord(t *testing.T, conversation string, lines ...string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	entries := make([]map[string]any, len(lines))
	for i, line := range lines {
		entries[i] = map[string]any{"type": "queue-operation", "operation": "enqueue", "timestamp": now, "content": line}
	}
	writeRecord(t, conversation, entries...)
}

// refusalOf reads the reason a refused press gives, and whether it says it
// lasts for as long as its queue stays as it is.
func refusalOf(t *testing.T, response *httptest.ResponseRecorder) (string, bool) {
	t.Helper()
	var refusal struct {
		Error     string `json:"error"`
		IsLasting bool   `json:"is_lasting"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &refusal); err != nil {
		t.Fatalf("the press answered %d %s, which is no refusal: %v", response.Code, response.Body, err)
	}
	return refusal.Error, refusal.IsLasting
}

// #2341: Claude Code gives its queue back only whole, so a line in it that
// the board did not type came onto the prompt line with the board's lines
// and stayed there unsent, and the board's messages read unconfirmed. Cancel,
// which Edit sends too, first reads that queue in the session's record, the
// record the board reads that session's waiting lines from. A line there that
// no board message typed, as one typed in the terminal, refuses the press,
// naming the line, for as long as the queue stays as it is. A notice the
// supervisor told the CEO in its turn, and a message goblins send typed into
// a goblin's, refuse it too, each named as what it is whether or not the
// record shows it yet. No key goes, and a record that shows only the board's
// lines has them taken back as before, his words among them though the record
// shows them without a character that draws nothing (#1915).
func TestCancelPressesNoKeyWhileTheQueueHoldsALineTheBoardDidNotType(t *testing.T) {
	for _, test := range []struct {
		name                 string
		isGoblin             bool
		words                string
		inTerminal, inRecord string
		hasNotice, hasSend   bool
		wantSays             string
	}{
		{name: "a line typed in the CEO's terminal", inTerminal: "check the logs too", wantSays: `The CEO's queue also holds "check the logs too", which the board did not type, so this message cannot be taken back alone.`},
		{name: "a line typed in a goblin's terminal", isGoblin: true, inTerminal: "check the logs too", wantSays: `task-1's queue also holds "check the logs too", which the board did not type, so this message cannot be taken back alone.`},
		{name: "a notice the supervisor told the CEO", hasNotice: true, inRecord: "Overlord: Run item r1 (build) finished with exit code 0.", wantSays: "The CEO's queue also holds a notice the supervisor told it, so this message cannot be taken back alone."},
		{name: "a message goblins send typed into a goblin's turn", isGoblin: true, hasSend: true, inRecord: "CEO: merge main first", wantSays: "task-1's queue also holds a message goblins send typed there, so this message cannot be taken back alone."},
		{name: "only the board's lines in the CEO's queue"},
		{name: "only the board's lines in a goblin's queue", isGoblin: true},
		// The console draws a word joiner over the letter before it, so the
		// board could not read back words that hold one; a soft hyphen draws
		// nothing too, and keeps a cell of its own.
		{name: "only his words in the CEO's queue, recorded without a character that draws nothing", words: "first wo\u00adrds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			words := cmp.Or(test.words, "first words")
			store, service, terminal, task, typed := heldQueue(t, test.isGoblin, "claude-busy", words)
			userHome := os.Getenv("USERPROFILE")
			service.Options.Tree = &fleettree.Reader{Home: userHome}
			// The record holds the line the board's delivery types, which for a
			// goblin is his words behind the board's stamp.
			conversation, lines := filepath.Join(ceoConversationFile(userHome, store.Home.Root), "session-1.jsonl"), []string{"first words"}
			if test.isGoblin {
				meta, err := state.ReadTaskMeta(store.Home.State, task)
				if err != nil {
					t.Fatal(err)
				}
				if err := state.WriteConversation(store.Home.State, state.Conversation{ID: task, Generation: meta.SpawnGen, Harness: "claude", Session: "goblin-session", ProvedAt: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
				conversation, lines = filepath.Join(ceoConversationFile(userHome, meta.Worktree), "goblin-session.jsonl"), []string{goblinLine(fromOverlord("first words"))}
			}
			if test.inTerminal != "" {
				terminal.typeLine(t, test.inTerminal)
				terminal.waitForLines(t, 3)
				promptLineEmpties(t, terminal, "claude")
				lines = append(lines, test.inTerminal)
			}
			if test.inRecord != "" {
				lines = append(lines, test.inRecord)
			}
			writeQueueRecord(t, conversation, lines...)
			now := time.Now().UTC()
			store.mu.Lock()
			if test.hasNotice {
				store.db.HeldNotices = []Awaiting{{Host: "ceo", Harness: "claude", Since: now, QuietSince: now, Digest: monitor.TextDigest("Overlord: Run item r1 (build) finished with exit code 0."), IsHeld: true}}
			}
			if test.hasSend {
				store.db.QueuedSends = []BoardActivity{{ID: "send-1", Kind: "send", TaskID: task, Target: task, State: "queued", At: now}}
			}
			saved := store.save()
			store.mu.Unlock()
			if saved != nil {
				t.Fatal(saved)
			}

			// Act
			cancel := pressOnQueued(service, "message_withdraw", task, "m1", words)
			input := terminal.lines(t)

			// Assert
			if test.wantSays == "" {
				if cancel.Code != 202 || slices.ContainsFunc(store.Snapshot().Actions, func(a Action) bool { return a.ID == "m1" }) || !slices.Contains(input, "took back "+typed(words)) || !slices.Contains(input, "cleared") {
					t.Errorf("Cancel = %d %s, terminal input = %q; want the message taken back out of its queue and off the board", cancel.Code, cancel.Body, input)
				}
				return
			}
			if says, isLasting := refusalOf(t, cancel); cancel.Code != 409 || !strings.HasPrefix(says, test.wantSays) || !isLasting {
				t.Errorf("Cancel = %d %s; want it refused for as long as the queue stays as it is, saying %q", cancel.Code, cancel.Body, test.wantSays)
			}
			if actions := store.Snapshot().Actions; !slices.ContainsFunc(actions, func(a Action) bool { return a.ID == "m1" && isHarnessHeld(a) }) {
				t.Errorf("after the press, actions = %+v; want m1 still held in the turn's queue", actions)
			}
			if slices.ContainsFunc(input, func(line string) bool { return strings.HasPrefix(line, "took back ") || line == "cleared" }) {
				t.Errorf("terminal input = %q; want no key pressed", input)
			}
		})
	}
}

// The board reads a session's queue only in the conversation that session
// writes now (#2341). A CEO that moved to another conversation the board could
// not record leaves the record naming the one it left, whose queue can still
// show lines that wait in no queue now. Cancel reads no queue there, and takes
// his message back out of the CEO's queue as before.
func TestCancelReadsNoQueueInAConversationTheCEOLeft(t *testing.T) {
	// Arrange
	store, service, terminal, task, typed := heldQueue(t, false, "claude-busy", "first words")
	userHome := os.Getenv("USERPROFILE")
	primary, _, err := readPrimary(store.Home.State)
	if err != nil {
		t.Fatal(err)
	}
	// The CEO's harness records itself in another conversation now, which the
	// board cannot record, since a folder stands where it would write it.
	service.Options.Tree = &fleettree.Reader{Home: userHome, Processes: func() ([]fleettree.Process, error) {
		return []fleettree.Process{{PID: primary.Process.PID, Exe: `C:\tools\claude.exe`, Created: 1, Started: primary.Process.Start}}, nil
	}}
	writeFile(t, filepath.Join(userHome, ".claude", "sessions", fmt.Sprintf("%d.json", primary.Process.PID)), fmt.Sprintf(`{"pid":%d,"sessionId":"live-session","procStart":"1"}`, primary.Process.PID))
	folder := ceoConversationFile(userHome, store.Home.Root)
	writeQueueRecord(t, filepath.Join(folder, "live-session.jsonl"))
	writeQueueRecord(t, filepath.Join(folder, "session-1.jsonl"), "first words", "check the logs too")
	if err := os.Remove(ceoConversationPath(store.Home.State)); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := os.Mkdir(ceoConversationPath(store.Home.State), 0o700); err != nil {
		t.Fatal(err)
	}
	locate, err := NewHTTP(service, "board.local", nil).messagesLocator(context.Background(), map[string][]string{"ceo": {"ceo"}})
	if err != nil {
		t.Fatal(err)
	}
	if left, err := locate(); err != nil || filepath.Base(left.Path) != "session-1.jsonl" || left.Stale == "" {
		t.Fatalf("the CEO's conversation = %+v, %v; want the one it left, read as stale", left, err)
	}

	// Act
	cancel := pressOnQueued(service, "message_withdraw", task, "m1", "first words")
	input := terminal.lines(t)

	// Assert
	if cancel.Code != 202 || slices.ContainsFunc(store.Snapshot().Actions, func(a Action) bool { return a.ID == "m1" }) || !slices.Contains(input, "took back "+typed("first words")) || !slices.Contains(input, "cleared") {
		t.Errorf("Cancel = %d %s, terminal input = %q; want the message taken back out of its queue and off the board", cancel.Code, cancel.Body, input)
	}
}

// #2341: Claude Code's send-now key takes a slash command at the head of its
// queue alone, as a turn of its own, and leaves the lines behind it queued.
// So his message behind a command typed in the CEO's terminal stayed Queued
// after Send now, reading that it was sent now. Send now first reads that
// queue in the session's record: behind a command that no board message typed
// it presses nothing and names the command ahead, as it does while the record
// shows that command and not yet his message, and behind words typed in the
// terminal, or ahead of such a command, it presses the key, which takes his
// message and any words ahead of it together.
func TestSendNowBehindACommandTypedInTheTerminalPressesNoKeyAndSaysWhatIsAhead(t *testing.T) {
	for _, test := range []struct {
		name, before, after string
		recorded            []string
		wantSays            string
	}{
		{name: "a command", before: "/compact", recorded: []string{"/compact", "stop and rebase"}, wantSays: `"/compact", a command the board did not type, waits ahead of this message in the CEO's queue, and its send-now key would run that command alone, so no key was pressed.`},
		{name: "a command its record shows before it shows his message", before: "/compact", recorded: []string{"/compact"}, wantSays: `"/compact", a command the board did not type, waits ahead of this message in the CEO's queue, and its send-now key would run that command alone, so no key was pressed.`},
		{name: "words", before: "check the logs too", recorded: []string{"check the logs too", "stop and rebase"}},
		{name: "a command behind his message", after: "/compact", recorded: []string{"stop and rebase", "/compact"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			userHome := useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, "claude-busy")
			service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}, Tree: &fleettree.Reader{Home: userHome}}}
			if test.before != "" {
				terminal.typeLine(t, test.before)
				terminal.waitForLines(t, 2)
				promptLineEmpties(t, terminal, "claude")
			}
			if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "stop and rebase"}); err != nil {
				t.Fatal(err)
			}
			pastUndoWindow(t, store)
			tryNext(t, store, service, terminal, "claude")
			if held := actionOf(store, "m1"); !isHarnessHeld(held) {
				t.Fatalf("his message = %+v; want it held in the turn's queue", held)
			}
			if test.after != "" {
				terminal.typeLine(t, test.after)
				terminal.waitForLines(t, 3)
				promptLineEmpties(t, terminal, "claude")
			}
			writeQueueRecord(t, filepath.Join(ceoConversationFile(userHome, home.Root), "session-1.jsonl"), test.recorded...)

			// Act
			pressed := sendNow(service, "", "m1")
			after := actionOf(store, "m1")

			// Assert
			if test.wantSays == "" {
				if input := terminal.waitForLines(t, 4); pressed.Code != 202 || !slices.Contains(input, "sent now") || after.Message != sentNowInTurn {
					t.Errorf("Send now = %d %s, terminal input = %q, his message = %+v; want the send-now key pressed and the message to say so", pressed.Code, pressed.Body, input, after)
				}
				return
			}
			if says, _ := refusalOf(t, pressed); pressed.Code != 409 || !strings.HasPrefix(says, test.wantSays) {
				t.Errorf("Send now = %d %s; want it refused, saying %q", pressed.Code, pressed.Body, test.wantSays)
			}
			if !isHarnessHeld(after) || after.Message != heldByCEO {
				t.Errorf("his message after the press = %+v; want it still held in the CEO's queue, reading %q", after, heldByCEO)
			}
			if input := terminal.lines(t); slices.Contains(input, "sent now") {
				t.Errorf("terminal input = %q; want no key pressed", input)
			}
		})
	}
}

// #2341, of his message the board still holds, as in its undo window: Send
// now types it into the CEO's turn and presses Claude Code's send-now key over
// it, which runs a command typed in the terminal ahead of it alone, as a turn
// of its own, and leaves his message queued. So Send now first reads the queue
// of the terminal the board would type it into, in that session's record:
// behind a command that no board message typed it types nothing, presses no
// key and names the command, and his message waits on the board as it did,
// while behind words typed in the terminal it goes on as before.
func TestSendNowOnHisMessageTheBoardStillHoldsPressesNoKeyBehindACommandTypedInTheTerminal(t *testing.T) {
	for _, test := range []struct{ name, inTerminal, wantSays string }{
		{name: "a command", inTerminal: "/compact", wantSays: `"/compact", a command the board did not type, waits ahead of this message in the CEO's queue, and its send-now key would run that command alone, so no key was pressed. It takes this message at its next tool call, or as its turn ends.`},
		{name: "words", inTerminal: "check the logs too"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			userHome := useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, "claude-busy")
			service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}, Tree: &fleettree.Reader{Home: userHome}}}
			terminal.typeLine(t, test.inTerminal)
			terminal.waitForLines(t, 2)
			promptLineEmpties(t, terminal, "claude")
			writeQueueRecord(t, filepath.Join(ceoConversationFile(userHome, home.Root), "session-1.jsonl"), test.inTerminal)
			if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "stop and rebase"}); err != nil {
				t.Fatal(err)
			}
			queued := actionOf(store, "m1")

			// Act
			pressed := sendNow(service, "", "m1")
			tried := store.ProcessOne(context.Background(), service.execute)
			after := actionOf(store, "m1")

			// Assert
			if test.wantSays == "" {
				if input := terminal.waitForLines(t, 4); pressed.Code != 202 || tried != nil || !slices.Equal(input[1:], []string{"queued check the logs too", "queued stop and rebase", "sent now"}) || after.Message != sentNowInTurn {
					t.Errorf("Send now = %d %s, its try = %v, terminal input = %q, his message = %+v; want it typed into the turn and the send-now key pressed", pressed.Code, pressed.Body, tried, input, after)
				}
				return
			}
			if says, _ := refusalOf(t, pressed); pressed.Code != 409 || says != test.wantSays {
				t.Errorf("Send now = %d %s; want it refused, saying %q", pressed.Code, pressed.Body, test.wantSays)
			}
			if after.Status != "queued" || after.IsSentNow || after.CanSendNow != queued.CanSendNow || after.Message != queued.Message {
				t.Errorf("his message after the press = %+v; want it waiting on the board as it did, %+v", after, queued)
			}
			if input := terminal.lines(t); !slices.Equal(input[1:], []string{"queued /compact"}) {
				t.Errorf("terminal input = %q; want nothing typed and no key pressed", input)
			}
		})
	}
}

// #2341: Claude Code gives its queue back only whole, and its send-now key
// takes that queue from its head, so a command he sends now cannot go ahead of
// a line there that no board message typed, as one typed in the terminal: Up
// would bring that line back onto the prompt line with his words, where none
// of them could be taken back alone, and with none of his words there the key
// would take that line first. Send now on such a command first reads that
// queue in the session's record, presses nothing, and names that line, with
// his words in that queue or with none.
func TestSendNowOnACommandPressesNoKeyWhileTheQueueHoldsALineTheBoardDidNotType(t *testing.T) {
	for _, test := range []struct {
		name  string
		words []string
	}{
		{name: "beside his words in the CEO's queue", words: []string{"first words"}},
		{name: "alone in the CEO's queue"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, service, terminal, _, _ := heldQueue(t, false, "claude-busy", test.words...)
			userHome := os.Getenv("USERPROFILE")
			service.Options.Tree = &fleettree.Reader{Home: userHome}
			terminal.typeLine(t, "check the logs too")
			terminal.waitForLines(t, len(test.words)+2)
			promptLineEmpties(t, terminal, "claude")
			writeQueueRecord(t, filepath.Join(ceoConversationFile(userHome, store.Home.Root), "session-1.jsonl"), append(test.words, "check the logs too")...)
			// Its try left the command on the board with Send now, as a try
			// does where Claude Code's send-now chord reaches the turn.
			now := time.Now().UTC()
			store.mu.Lock()
			store.db.Actions = append(store.db.Actions, Action{ID: "m2", Kind: "message", Text: "/compact", Status: "queued", CanSendNow: true, Message: "Queued. " + commandWaits + " " + commandSendsNow, CreatedAt: now, UpdatedAt: now})
			saved := store.save()
			store.mu.Unlock()
			if saved != nil {
				t.Fatal(saved)
			}

			// Act
			pressed := sendNow(service, "", "m2")
			command := actionOf(store, "m2")

			// Assert
			if says, _ := refusalOf(t, pressed); pressed.Code != 409 || says != `The CEO's queue also holds "check the logs too", which the board did not type, so this command cannot go ahead of it. A command runs when its turn ends.` {
				t.Errorf("Send now = %d %s; want it refused, naming the line typed in the terminal", pressed.Code, pressed.Body)
			}
			if command.Status != "queued" || command.IsSentNow {
				t.Errorf("the command after the press = %+v; want it still on the board, not sent now", command)
			}
			if words := actionOf(store, "m1"); len(test.words) > 0 && !isHarnessHeld(words) {
				t.Errorf("his words after the press = %+v; want them still in the turn's queue", words)
			}
			if input := terminal.lines(t); slices.ContainsFunc(input, func(line string) bool {
				return strings.HasPrefix(line, "took back ") || line == "cleared" || line == "sent now" || line == "queued /compact"
			}) {
				t.Errorf("terminal input = %q; want no key pressed", input)
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
// goes at the prompt line the turn's end leaves. pi, which runs /compact
// beside its turn and expands another command into a steer, ends its turn on
// Escape first too, though Alt+Q gives its queue back (#2340).
func TestSendNowRunsACommandHeldForTheCEOsTurnAtOnce(t *testing.T) {
	for _, test := range []struct {
		name, mode, command string
		wantInput           []string
	}{
		{name: "Claude Code", mode: "claude-busy", command: "/compact", wantInput: []string{"queued /compact", "sent now"}},
		{name: "Codex", mode: "busy", command: "/compact", wantInput: []string{"interrupted", "accepted /compact"}},
		{name: "pi", mode: "pi-busy", command: "/review", wantInput: []string{"interrupted", "accepted /review"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, test.mode)
			service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}}}
			if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: test.command}); err != nil {
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

// The Overlord, 2026-11-19: "commands dont ahve a send now function in modern
// terminal ?" (#2759). A command he queues behind his words to the CEO while
// they wait out their undo window offers Send now from the start, as words
// do, and a press runs it at once, ahead of those words, which stay on the
// board for Edit and Cancel.
func TestSendNowRunsACommandQueuedBehindHisWordsInTheirUndoWindowAtOnce(t *testing.T) {
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
			for _, message := range []Action{{ID: "m1", Kind: "message", Text: "first words"}, {ID: "m2", Kind: "message", Text: "/compact"}} {
				if _, err := store.Queue(message); err != nil {
					t.Fatal(err)
				}
			}
			queued := actionOf(store, "m2")

			// Act
			pressed := sendNow(service, "", "m2")
			err := store.ProcessOne(context.Background(), service.execute)
			sent, words := actionOf(store, "m2"), actionOf(store, "m1")

			// Assert
			if !queued.CanSendNow || queued.SendNowWhy != "" {
				t.Errorf("the command behind his words = %+v; want Send now offered", queued)
			}
			if pressed.Code != 202 || err != nil || sent.Status != "succeeded" {
				t.Fatalf("Send now = %d %s, delivery = %v, command = %+v; want the command run at once", pressed.Code, pressed.Body, err, sent)
			}
			if input := terminal.waitForLines(t, 1+len(test.wantInput)); !slices.Equal(input[1:], test.wantInput) {
				t.Errorf("terminal input = %q; want %q after its registration", input, test.wantInput)
			}
			if words.Status != "queued" {
				t.Errorf("his words = %+v; want them still on the board in their undo window", words)
			}
		})
	}
}

// Claude Code's send-now chord takes its turn's queue from the head, so a
// command he sends now behind his words in that queue would wait out the turn
// they start (#2759). Send now takes those words back out of the queue first,
// runs the command at once, and types them into the turn again after it.
func TestSendNowRunsACommandAtOnceAheadOfHisWordsInClaudeCodesQueue(t *testing.T) {
	// Arrange
	store, service, terminal, _, _ := heldQueue(t, false, "claude-busy")
	if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "first words"}); err != nil {
		t.Fatal(err)
	}
	pastUndoWindow(t, store)
	tryNext(t, store, service, terminal, "claude")
	if _, err := store.Queue(Action{ID: "m2", Kind: "message", Text: "/compact"}); err != nil {
		t.Fatal(err)
	}
	tryNext(t, store, service, terminal, "claude")
	if words, command := actionOf(store, "m1"), actionOf(store, "m2"); !isHarnessHeld(words) || command.Status != "queued" || !command.CanSendNow {
		t.Fatalf("his words = %+v, the command = %+v; want the words in the turn's queue and the command held on the board with Send now", words, command)
	}

	// Act
	pressed := sendNow(service, "", "m2")
	back := actionOf(store, "m1")
	ran := store.ProcessOne(context.Background(), service.execute)
	command := actionOf(store, "m2")
	promptLineEmpties(t, terminal, "claude")
	again := store.ProcessOne(context.Background(), service.execute)
	words := actionOf(store, "m1")
	input := terminal.waitForLines(t, 7)

	// Assert
	if pressed.Code != 202 || back.Status != "queued" || back.Message != "Queued. It left its session's queue so the command he sent now runs first, and is typed there again after it." {
		t.Errorf("Send now = %d %s, his words = %+v; want it taken, and the words back on the board behind the command", pressed.Code, pressed.Body, back)
	}
	if ran != nil || command.Status != "succeeded" {
		t.Errorf("the command's try = %v, the command = %+v; want it run at once", ran, command)
	}
	if again != nil || !isHarnessHeld(words) {
		t.Errorf("the next try = %v, his words = %+v; want them typed into the turn again", again, words)
	}
	if want := []string{"queued first words", "took back first words", "cleared", "queued /compact", "sent now", "queued first words"}; !slices.Equal(input[1:], want) {
		t.Errorf("terminal input = %q; want %q after its registration", input, want)
	}
}

// pi gives its turn's queue back to Alt+Q, so a command he sends now behind
// his words in that queue goes ahead of them as on Claude Code (#2340,
// #2759): Send now takes those words back out of the queue first, pi's
// Escape then ends a turn whose queue no longer holds them, the command runs
// at the prompt line that turn's end leaves, and his words go after it.
func TestSendNowRunsACommandAtOnceAheadOfHisWordsInPisQueue(t *testing.T) {
	// Arrange
	store, service, terminal, _, _ := heldQueue(t, false, "pi-busy")
	if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "first words"}); err != nil {
		t.Fatal(err)
	}
	pastUndoWindow(t, store)
	tryNext(t, store, service, terminal, "pi")
	if _, err := store.Queue(Action{ID: "m2", Kind: "message", Text: "/review"}); err != nil {
		t.Fatal(err)
	}
	tryNext(t, store, service, terminal, "pi")
	offered := actionOf(store, "m2")

	// Act
	pressed := sendNow(service, "", "m2")
	back := actionOf(store, "m1")
	ran := store.ProcessOne(context.Background(), service.execute)
	command := actionOf(store, "m2")
	promptLineEmpties(t, terminal, "pi")
	again := store.ProcessOne(context.Background(), service.execute)
	words := actionOf(store, "m1")
	input := terminal.waitForLines(t, 7)

	// Assert
	if offered.Status != "queued" || !offered.CanSendNow || offered.Message != "Queued. "+commandWaits+" "+commandSendsNow {
		t.Errorf("the command behind his words = %+v; want it held on the board with Send now", offered)
	}
	if pressed.Code != 202 || back.Status != "queued" || back.Message != "Queued. It left its session's queue so the command he sent now runs first, and is typed there again after it." {
		t.Errorf("Send now = %d %s, his words = %+v; want it taken, and the words back on the board behind the command", pressed.Code, pressed.Body, back)
	}
	if ran != nil || command.Status != "succeeded" {
		t.Errorf("the command's try = %v, the command = %+v; want it run at once", ran, command)
	}
	if again != nil || words.Status != "succeeded" {
		t.Errorf("the next try = %v, his words = %+v; want them taken at the prompt line the turn's end left", again, words)
	}
	if want := []string{"queued first words", "took back first words", "cleared", "interrupted", "accepted /review", "accepted first words"}; !slices.Equal(input[1:], want) {
		t.Errorf("terminal input = %q; want %q after its registration", input, want)
	}
}

// Alt+Q would put his words back in pi's editor, but that editor shows only
// its last rows, at most 30% of its terminal's and no fewer than five: seven
// of the stand-in's 24, where five lines take nine. The board could not read
// them all back to take them off, so a command behind them in that queue
// cannot go ahead of them: it offers no Send now, and a press says it runs
// when the turn ends, with no key pressed in his turn (#2340).
func TestACommandBehindAQueueTallerThanPisEditorOffersNoSendNow(t *testing.T) {
	// Arrange
	store, service, terminal, _, _ := heldQueue(t, false, "pi-busy", "first words", "second words", "third words", "fourth words", "fifth words")
	if _, err := store.Queue(Action{ID: "m6", Kind: "message", Text: "/review"}); err != nil {
		t.Fatal(err)
	}
	pastUndoWindow(t, store)

	// Act
	tried := store.ProcessOne(context.Background(), service.execute)
	held := actionOf(store, "m6")
	pressed := sendNow(service, "", "m6")
	after := actionOf(store, "m6")

	// Assert
	if !errors.Is(tried, ErrDeferred) || held.Status != "queued" || held.CanSendNow || held.SendNowWhy != commandWaits || held.Message != "Queued. "+commandWaits {
		t.Errorf("its try = %v, the command = %+v; want it held for the turn's end with no Send now, saying so", tried, held)
	}
	if pressed.Code != 409 || !strings.Contains(pressed.Body.String(), commandWaits) || after.IsSentNow {
		t.Errorf("Send now = %d %s, the command = %+v; want it refused, saying the command runs when the turn ends", pressed.Code, pressed.Body, after)
	}
	for _, id := range []string{"m1", "m2", "m3", "m4", "m5"} {
		if words := actionOf(store, id); !isHarnessHeld(words) {
			t.Errorf("%s after the press = %+v; want it still held in pi's queue", id, words)
		}
	}
	if input := terminal.lines(t); slices.ContainsFunc(input, func(line string) bool {
		return strings.HasPrefix(line, "took back ") || line == "cleared" || line == "interrupted"
	}) {
		t.Errorf("terminal input = %q; want no key pressed", input)
	}
}

// His words he sent now leave Claude Code's queue at its send-now chord, and
// the board reads them taken only once the session's record shows it, which
// can come after he sends a command now behind them, as a record written on
// a loaded machine does (#2541). Send now on that command settles what the
// record shows first, and with nothing of his left in the queue it runs the
// command at once and takes nothing back (#2759).
func TestSendNowRunsACommandAtOnceBehindHisWordsTheirTurnTookBeforeTheBoardSawIt(t *testing.T) {
	// Arrange
	records := useConversations(t)
	store, home := testStore(t)
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}}}
	terminal := nativeComposer(t, home.State, "ceo", "claude-busy")
	if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "first words"}); err != nil {
		t.Fatal(err)
	}
	if pressed := sendNow(service, "", "m1"); pressed.Code != 202 {
		t.Fatalf("Send now on his words = %d %s; want it taken", pressed.Code, pressed.Body)
	}
	tryNext(t, store, service, terminal, "claude")
	if _, err := store.Queue(Action{ID: "m2", Kind: "message", Text: "/compact"}); err != nil {
		t.Fatal(err)
	}
	tryNext(t, store, service, terminal, "claude")
	takenInRecord(t, records, "claude", "session-1", "", "first words")
	if words, command := actionOf(store, "m1"), actionOf(store, "m2"); !isHarnessHeld(words) || command.Status != "queued" || !command.CanSendNow {
		t.Fatalf("his words = %+v, the command = %+v; want the words still read as in the turn's queue and the command held on the board with Send now", words, command)
	}

	// Act
	pressed := sendNow(service, "", "m2")
	words := actionOf(store, "m1")
	ran := store.ProcessOne(context.Background(), service.execute)
	command := actionOf(store, "m2")
	input := terminal.waitForLines(t, 5)

	// Assert
	if pressed.Code != 202 || words.Status != "succeeded" {
		t.Errorf("Send now = %d %s, his words = %+v; want it taken, and the words read as taken by the CEO", pressed.Code, pressed.Body, words)
	}
	if ran != nil || command.Status != "succeeded" {
		t.Errorf("the command's try = %v, the command = %+v; want it run at once", ran, command)
	}
	if want := []string{"queued first words", "sent now", "queued /compact", "sent now"}; !slices.Equal(input[1:], want) {
		t.Errorf("terminal input = %q; want %q after its registration", input, want)
	}
}

// The worker types his words to the CEO into its turn once their undo window
// ends, and records them in the turn's queue only once that try ends. Send
// now on a command he queued behind them, pressed while that try runs, waits
// it out rather than reading the queue empty and being spent by a try that
// then finds his words ahead of the command: it takes them back out of the
// queue once they are recorded there, and the command runs at once ahead of
// them (#2759).
func TestSendNowOnACommandWaitsOutTheTryOfHisWordsAheadOfItAndRunsItFirst(t *testing.T) {
	// Arrange
	useConversations(t)
	store, home := testStore(t)
	terminal := nativeCEOComposer(t, home.State, "claude-busy")
	screens, _ := harness.NativeScreens(harness.Claude)
	// The try of his words is held once they show typed on the prompt line,
	// before their submit key, until he has pressed Send now.
	typing, release, isHeldBack := make(chan struct{}), make(chan struct{}), false
	read := func(record host.Record) ([]string, error) {
		screen, err := host.ReadScreen(record)
		if err == nil && !isHeldBack && screens.ComposerHoldsAllOf(screen, "first words") {
			isHeldBack = true
			close(typing)
			<-release
		}
		return screen, err
	}
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State, ReadScreen: read}}}
	for _, message := range []Action{{ID: "m1", Kind: "message", Text: "first words"}, {ID: "m2", Kind: "message", Text: "/compact"}} {
		if _, err := store.Queue(message); err != nil {
			t.Fatal(err)
		}
	}
	pastUndoWindow(t, store)
	previous := tryWait
	tryWait = time.Minute
	t.Cleanup(func() { tryWait = previous })
	tried := make(chan error, 1)
	go func() { tried <- store.ProcessOne(context.Background(), service.execute) }()
	select {
	case <-typing:
	case err := <-tried:
		t.Fatalf("the try of his words = %v, which ended before they showed typed", err)
	}

	// Act
	answered := make(chan *httptest.ResponseRecorder, 1)
	go func() { answered <- sendNow(service, "", "m2") }()
	var early *httptest.ResponseRecorder
	select {
	case early = <-answered:
	case <-time.After(time.Second): // stall-accepted: the press is meant to stay waiting for the try it found under way, so a stall only makes the wait longer.
	}
	close(release)
	typedErr := <-tried
	pressed := early
	if pressed == nil {
		pressed = <-answered
	}
	back := actionOf(store, "m1")
	ran := store.ProcessOne(context.Background(), service.execute)
	command := actionOf(store, "m2")
	promptLineEmpties(t, terminal, "claude")
	again := store.ProcessOne(context.Background(), service.execute)
	words := actionOf(store, "m1")
	input := terminal.waitForLines(t, 7)

	// Assert
	if early != nil {
		t.Errorf("Send now = %d %s while the try of his words ran; want it to wait that try out", early.Code, early.Body)
	}
	if typedErr != nil || pressed.Code != 202 || back.Status != "queued" || back.Message != "Queued. It left its session's queue so the command he sent now runs first, and is typed there again after it." {
		t.Errorf("the try of his words = %v, Send now = %d %s, his words = %+v; want them typed into the turn, the press taken, and the words back on the board behind the command", typedErr, pressed.Code, pressed.Body, back)
	}
	if ran != nil || command.Status != "succeeded" {
		t.Errorf("the command's try = %v, the command = %+v; want it run at once", ran, command)
	}
	if again != nil || !isHarnessHeld(words) {
		t.Errorf("the next try = %v, his words = %+v; want them typed into the turn again", again, words)
	}
	if want := []string{"queued first words", "took back first words", "cleared", "queued /compact", "sent now", "queued first words"}; !slices.Equal(input[1:], want) {
		t.Errorf("terminal input = %q; want %q after its registration", input, want)
	}
}

// A delivery the worker tries types into its session's turn a line the board
// records in that turn's queue only once the try ends, as his words to the
// CEO are, or once its submit key went, as a goblin's are. So Send now on a
// command reads that queue only once no delivery to the session is being
// tried, and a press while one runs past the wait for it is refused, saying
// to press again in a moment, with nothing sent now and no key pressed,
// rather than taken with the queue unread and then spent by a try that finds
// his words queued ahead of the command (#2759).
func TestSendNowOnACommandIsRefusedWhileATryOfADeliveryToItsSessionOutlastsItsWait(t *testing.T) {
	for _, test := range []struct {
		name     string
		isGoblin bool
	}{
		{name: "the CEO's"},
		{name: "a goblin's", isGoblin: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, service, terminal, task, _ := heldQueue(t, test.isGoblin, "claude-busy")
			for _, message := range []Action{{ID: "m1", Kind: "message", TaskID: task, Text: "first words"}, {ID: "m2", Kind: "message", TaskID: task, Text: "/compact"}} {
				if _, err := store.Queue(message); err != nil {
					t.Fatal(err)
				}
			}
			if offered := actionOf(store, "m2"); !offered.CanSendNow {
				t.Fatalf("the command = %+v; want Send now offered while his words ahead of it offer it", offered)
			}
			store.mu.Lock()
			store.db.Actions[slices.IndexFunc(store.db.Actions, func(a Action) bool { return a.ID == "m1" })].Status = "running"
			store.trying = []string{"m1"}
			store.mu.Unlock()
			// The try of his words never ends here, so the press waits for it
			// only a moment.
			previous := tryWait
			tryWait = 200 * time.Millisecond
			t.Cleanup(func() { tryWait = previous })

			// Act
			pressed := sendNow(service, task, "m2")
			command := actionOf(store, "m2")

			// Assert
			if pressed.Code != 409 || !strings.Contains(pressed.Body.String(), "The board is typing for") || command.IsSentNow {
				t.Errorf("Send now = %d %s, the command = %+v; want it refused while the board tries his words, and not sent now", pressed.Code, pressed.Body, command)
			}
			if input := terminal.lines(t); len(input) != 1 {
				t.Errorf("terminal input = %q; want nothing after its registration", input)
			}
		})
	}
}

// Send now on a command that reads Not sent puts it back in the queue to go
// at once, so behind his words in Claude Code's queue it takes them back out
// of that queue first, runs the command at once and types them into the turn
// again after it, as for a command queued on the board (#2759).
func TestSendNowRunsACommandThatReadsNotSentAtOnceAheadOfHisWordsInClaudeCodesQueue(t *testing.T) {
	// Arrange
	store, service, terminal, _, _ := heldQueue(t, false, "claude-busy", "first words")
	promptLineEmpties(t, terminal, "claude")
	now := time.Now().UTC()
	store.mu.Lock()
	store.db.Actions = append(store.db.Actions, Action{ID: "m2", Kind: "message", Text: "/compact", Status: "failed", IsSentNow: true, Message: "Not sent. The answer was typed but never showed in the CEO's input. It was taken back off the CEO's prompt line, so nothing of it is left typed there.", CreatedAt: now, UpdatedAt: now})
	saved := store.save()
	store.mu.Unlock()
	if saved != nil {
		t.Fatal(saved)
	}
	if unsent := actionOf(store, "m2"); !isNotSent(unsent) {
		t.Fatalf("the command = %+v; want it to read Not sent", unsent)
	}

	// Act
	pressed := sendNow(service, "", "m2")
	back := actionOf(store, "m1")
	ran := store.ProcessOne(context.Background(), service.execute)
	command := actionOf(store, "m2")
	promptLineEmpties(t, terminal, "claude")
	again := store.ProcessOne(context.Background(), service.execute)
	words := actionOf(store, "m1")
	input := terminal.waitForLines(t, 7)

	// Assert
	if pressed.Code != 202 || back.Status != "queued" || back.Message != "Queued. It left its session's queue so the command he sent now runs first, and is typed there again after it." {
		t.Errorf("Send now = %d %s, his words = %+v; want it taken, and the words back on the board behind the command", pressed.Code, pressed.Body, back)
	}
	if ran != nil || command.Status != "succeeded" {
		t.Errorf("the command's try = %v, the command = %+v; want it run at once", ran, command)
	}
	if again != nil || !isHarnessHeld(words) {
		t.Errorf("the next try = %v, his words = %+v; want them typed into the turn again", again, words)
	}
	if want := []string{"queued first words", "took back first words", "cleared", "queued /compact", "sent now", "queued first words"}; !slices.Equal(input[1:], want) {
		t.Errorf("terminal input = %q; want %q after its registration", input, want)
	}
}

// Only a command he sends now goes ahead of his words in Claude Code's queue
// (#2759). Send now on his words behind them, in their undo window, types
// those words into the turn behind them and presses the send-now chord, which
// has the turn take its whole queue at once, and takes nothing back out of
// that queue.
func TestSendNowOnHisWordsBehindHisWordsInClaudeCodesQueueTakesNothingBack(t *testing.T) {
	// Arrange
	store, service, terminal, _, _ := heldQueue(t, false, "claude-busy", "first words")
	promptLineEmpties(t, terminal, "claude")
	if _, err := store.Queue(Action{ID: "m2", Kind: "message", Text: "more words"}); err != nil {
		t.Fatal(err)
	}

	// Act
	pressed := sendNow(service, "", "m2")
	first := actionOf(store, "m1")
	err := store.ProcessOne(context.Background(), service.execute)
	sent := actionOf(store, "m2")
	input := terminal.waitForLines(t, 4)

	// Assert
	if pressed.Code != 202 || !isHarnessHeld(first) {
		t.Errorf("Send now = %d %s, his first words = %+v; want it taken, and those words left in the turn's queue", pressed.Code, pressed.Body, first)
	}
	if err != nil || !isHarnessHeld(sent) || sent.Message != sentNowInTurn {
		t.Errorf("its try = %v, his words sent now = %+v; want them typed into the turn and sent now", err, sent)
	}
	if want := []string{"queued first words", "queued more words", "sent now"}; !slices.Equal(input[1:], want) {
		t.Errorf("terminal input = %q; want %q after its registration, nothing taken back", input, want)
	}
}

// His words wait for a command he sent now after them, which Claude Code's
// send-now chord would otherwise find queued behind them (#2759), but a
// command waits for none, so two commands he sends now to the CEO run in the
// order he sent them.
func TestTwoCommandsHeSendsNowToTheCEORunInTheOrderHeSentThem(t *testing.T) {
	// Arrange
	useConversations(t)
	store, home := testStore(t)
	terminal := nativeCEOComposer(t, home.State, "claude-busy")
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}}}
	for _, message := range []Action{{ID: "m1", Kind: "message", Text: "first words"}, {ID: "m2", Kind: "message", Text: "/compact"}, {ID: "m3", Kind: "message", Text: "/context"}} {
		if _, err := store.Queue(message); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"m2", "m3"} {
		if pressed := sendNow(service, "", id); pressed.Code != 202 {
			t.Fatalf("Send now on %s = %d %s; want it taken", id, pressed.Code, pressed.Body)
		}
	}

	// Act
	first := store.ProcessOne(context.Background(), service.execute)
	promptLineEmpties(t, terminal, "claude")
	second := store.ProcessOne(context.Background(), service.execute)
	input := terminal.waitForLines(t, 5)

	// Assert
	if first != nil || second != nil || actionOf(store, "m2").Status != "succeeded" || actionOf(store, "m3").Status != "succeeded" {
		t.Errorf("their tries = %v, %v, the commands = %+v, %+v; want both run at once", first, second, actionOf(store, "m2"), actionOf(store, "m3"))
	}
	if want := []string{"queued /compact", "sent now", "queued /context", "sent now"}; !slices.Equal(input[1:], want) {
		t.Errorf("terminal input = %q; want %q after its registration", input, want)
	}
}

// Codex gives its turn's queue back to no key, and its send-now key, Escape,
// ends its turn by handing the line queued there back onto its prompt line,
// where a command cannot then be typed (#2759). So a command behind his words
// in a Codex turn's queue offers no Send now once it is tried, and says it
// runs when that turn ends. A press that lands before that try, while the
// words between them on the board offer Send now in their undo window, is
// refused with that reason, and nothing is pressed in his turn.
func TestACommandBehindHisWordsInAQueueNoKeyGivesBackOffersNoSendNow(t *testing.T) {
	// Arrange
	store, service, terminal, _, _ := heldQueue(t, false, "busy", "first words")
	for _, message := range []Action{{ID: "m2", Kind: "message", Text: "more words"}, {ID: "m3", Kind: "message", Text: "/compact"}} {
		if _, err := store.Queue(message); err != nil {
			t.Fatal(err)
		}
	}
	if offered := actionOf(store, "m3"); !offered.CanSendNow {
		t.Fatalf("the command = %+v; want Send now offered while the words ahead of it on the board offer it", offered)
	}

	// Act
	pressed := sendNow(service, "", "m3")
	refused := actionOf(store, "m3")
	pastUndoWindow(t, store)
	tryNext(t, store, service, terminal, "codex")
	tried := store.ProcessOne(context.Background(), service.execute)
	held := actionOf(store, "m3")
	input := terminal.waitForLines(t, 3)

	// Assert
	if pressed.Code != 409 || !strings.Contains(pressed.Body.String(), commandWaits) || refused.IsSentNow {
		t.Errorf("Send now = %d %s, the command = %+v; want it refused, saying the command runs when the turn ends", pressed.Code, pressed.Body, refused)
	}
	if !errors.Is(tried, ErrDeferred) || held.Status != "queued" || held.CanSendNow || held.SendNowWhy != commandWaits || held.Message != "Queued. "+commandWaits {
		t.Errorf("its try = %v, the command = %+v; want it held for the turn's end with no Send now, saying so", tried, held)
	}
	if want := []string{"queued first words", "queued more words"}; !slices.Equal(input[1:], want) {
		t.Errorf("terminal input = %q; want %q after its registration, and no key pressed for the command", input, want)
	}
}

// A command that reads Not sent reached nobody, so Send now puts it back in
// the queue, as for any message that reads Not sent. Behind his words in a
// Codex turn's queue, which no key gives back, it cannot go ahead of them, and
// Codex's send-now key, Escape, would end that turn by handing them back onto
// its prompt line. So there it goes back in the queue with no Send now, to run
// when that turn ends, rather than being refused and left reading Not sent,
// and the press says so, with nothing pressed in his turn (#2759).
func TestSendNowPutsACommandThatReadsNotSentBackInTheQueueBehindHisWordsInAQueueNoKeyGivesBack(t *testing.T) {
	// Arrange
	store, service, terminal, _, _ := heldQueue(t, false, "busy", "first words")
	now := time.Now().UTC()
	store.mu.Lock()
	store.db.Actions = append(store.db.Actions, Action{ID: "m2", Kind: "message", Text: "/compact", Status: "failed", Message: "Not sent. The answer was typed but never showed in the CEO's input. It was taken back off the CEO's prompt line, so nothing of it is left typed there.", CreatedAt: now.Add(-time.Minute), UpdatedAt: now})
	saved := store.save()
	store.mu.Unlock()
	if saved != nil {
		t.Fatal(saved)
	}
	if unsent := actionOf(store, "m2"); !isNotSent(unsent) {
		t.Fatalf("the command = %+v; want it to read Not sent", unsent)
	}

	// Act
	pressed := sendNow(service, "", "m2")
	queued, words := actionOf(store, "m2"), actionOf(store, "m1")
	tried := store.ProcessOne(context.Background(), service.execute)
	held := actionOf(store, "m2")
	input := terminal.waitForLines(t, 2)

	// Assert
	if pressed.Code != 409 || !strings.Contains(pressed.Body.String(), commandWaits) || queued.Status != "queued" || queued.IsSentNow || queued.CanSendNow || !isHarnessHeld(words) {
		t.Errorf("Send now = %d %s, the command = %+v, his words = %+v; want it back in the queue, not sent now, saying it runs when the turn ends, and his words left in the turn's queue", pressed.Code, pressed.Body, queued, words)
	}
	if !errors.Is(tried, ErrDeferred) || held.Status != "queued" || held.CanSendNow || held.SendNowWhy != commandWaits || held.Message != "Queued. "+commandWaits {
		t.Errorf("its try = %v, the command = %+v; want it held for the turn's end with no Send now, saying so", tried, held)
	}
	if want := []string{"queued first words"}; !slices.Equal(input[1:], want) {
		t.Errorf("terminal input = %q; want %q after its registration, and no key pressed for the command", input, want)
	}
}

// Claude Code gives its turn's queue back to Up, but only his words there can
// wait on the board behind a command he sends now: an answer the board typed
// into that queue would be typed into the turn again ahead of the command
// (#2759). So a command behind his words and such an answer in Claude Code's
// queue is refused, saying it runs when that turn ends, with nothing taken out
// of the queue, and once it is tried it offers no Send now.
func TestACommandBehindAnAnswerInClaudeCodesQueueOffersNoSendNow(t *testing.T) {
	// Arrange
	store, service, terminal, _, _ := heldQueue(t, false, "claude-busy", "first words")
	identity := registrationIdentity(t, terminal.stateDir)
	question := Question{ID: "held-question", Identity: identity, Text: "Choose", CreatedAt: time.Now().UTC()}
	if err := store.acceptQuestion(question); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queue(Action{ID: "held-answer", Kind: "ceo_answer", Generation: identity, QuestionID: question.ID, Text: "Reply received"}); err != nil {
		t.Fatal(err)
	}
	tryNext(t, store, service, terminal, "claude")
	given := actionOf(store, "held-answer")
	if !isHarnessHeld(given) {
		t.Fatalf("the answer = %+v; want it in the turn's queue beside his words", given)
	}
	for _, message := range []Action{{ID: "m2", Kind: "message", Text: "more words"}, {ID: "m3", Kind: "message", Text: "/compact"}} {
		if _, err := store.Queue(message); err != nil {
			t.Fatal(err)
		}
	}
	if offered := actionOf(store, "m3"); !offered.CanSendNow {
		t.Fatalf("the command = %+v; want Send now offered while the words ahead of it on the board offer it", offered)
	}

	// Act
	pressed := sendNow(service, "", "m3")
	refused, words, answer := actionOf(store, "m3"), actionOf(store, "m1"), actionOf(store, "held-answer")
	pastUndoWindow(t, store)
	tryNext(t, store, service, terminal, "claude")
	tried := store.ProcessOne(context.Background(), service.execute)
	held := actionOf(store, "m3")
	input := terminal.waitForLines(t, 4)

	// Assert
	if pressed.Code != 409 || !strings.Contains(pressed.Body.String(), commandWaits) || refused.IsSentNow {
		t.Errorf("Send now = %d %s, the command = %+v; want it refused, saying the command runs when the turn ends", pressed.Code, pressed.Body, refused)
	}
	if !isHarnessHeld(words) || !isHarnessHeld(answer) {
		t.Errorf("after the press, his words = %+v, the answer = %+v; want both still in the turn's queue", words, answer)
	}
	if !errors.Is(tried, ErrDeferred) || held.Status != "queued" || held.CanSendNow || held.SendNowWhy != commandWaits || held.Message != "Queued. "+commandWaits {
		t.Errorf("its try = %v, the command = %+v; want it held for the turn's end with no Send now, saying so", tried, held)
	}
	if want := []string{"queued first words", "queued " + given.Awaiting.Typed, "queued more words"}; !slices.Equal(input[1:], want) {
		t.Errorf("terminal input = %q; want %q after its registration, and nothing taken back or pressed for the command", input, want)
	}
}

// Claude Code gives its turn's queue back to Up, but only his words there can
// wait on the board behind a command he sends now (#2759), and an answer the
// board typed into that queue cannot, with his words beside it or alone. So a
// command behind an answer alone in Claude Code's queue offers no Send now
// once it is tried, saying it runs when that turn ends, and a press is refused
// with that reason, with nothing pressed in the turn.
func TestACommandBehindAnAnswerAloneInClaudeCodesQueueOffersNoSendNow(t *testing.T) {
	// Arrange
	store, service, terminal, _, _ := heldQueue(t, false, "claude-busy")
	identity := registrationIdentity(t, terminal.stateDir)
	question := Question{ID: "held-question", Identity: identity, Text: "Choose", CreatedAt: time.Now().UTC()}
	if err := store.acceptQuestion(question); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queue(Action{ID: "held-answer", Kind: "ceo_answer", Generation: identity, QuestionID: question.ID, Text: "Reply received"}); err != nil {
		t.Fatal(err)
	}
	tryNext(t, store, service, terminal, "claude")
	given := actionOf(store, "held-answer")
	if !isHarnessHeld(given) {
		t.Fatalf("the answer = %+v; want it alone in the turn's queue", given)
	}
	if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "/compact"}); err != nil {
		t.Fatal(err)
	}
	pastUndoWindow(t, store)

	// Act
	tried := store.ProcessOne(context.Background(), service.execute)
	held := actionOf(store, "m1")
	pressed := sendNow(service, "", "m1")
	refused := actionOf(store, "m1")
	input := terminal.waitForLines(t, 2)

	// Assert
	if !errors.Is(tried, ErrDeferred) || held.Status != "queued" || held.CanSendNow || held.SendNowWhy != commandWaits || held.Message != "Queued. "+commandWaits {
		t.Errorf("its try = %v, the command = %+v; want it held for the turn's end with no Send now, saying so", tried, held)
	}
	if pressed.Code != 409 || !strings.Contains(pressed.Body.String(), commandWaits) || refused.IsSentNow {
		t.Errorf("Send now = %d %s, the command = %+v; want it refused, saying the command runs when the turn ends", pressed.Code, pressed.Body, refused)
	}
	if want := []string{"queued " + given.Awaiting.Typed}; !slices.Equal(input[1:], want) {
		t.Errorf("terminal input = %q; want %q after its registration, and nothing pressed for the command", input, want)
	}
}

// Claude Code's Up gives back its turn's whole queue, which can also hold a
// line typed there that no message on the board records: a notice the board
// told the CEO in that turn, or a line goblins send typed into a goblin's.
// The board cannot take its own lines back from beside such a line, and those
// it took back would wait on the prompt line with it, sent to no one. So Send
// now on a command behind his words in such a queue is refused, saying the
// command runs when the turn ends, and takes nothing back (#2759).
func TestSendNowOnACommandTakesNothingBackFromAQueueThatAlsoHoldsALineNoMessageRecords(t *testing.T) {
	for _, test := range []struct {
		name     string
		isGoblin bool
	}{
		{name: "a notice told to the CEO"},
		{name: "a line goblins send typed into a goblin", isGoblin: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, service, terminal, task, _ := heldQueue(t, test.isGoblin, "claude-busy", "first words")
			generation := ""
			if test.isGoblin {
				meta, err := state.ReadTaskMeta(terminal.stateDir, task)
				if err != nil {
					t.Fatal(err)
				}
				generation = meta.SpawnGen
				terminal.typeLine(t, "CEO: merge main first")
				store.mu.Lock()
				store.holdQueuedSend(BoardActivity{ID: "send-1", Kind: "message", TaskID: task, Generation: generation, State: "queued", Digest: monitor.TextDigest("CEO: merge main first"), At: time.Now().UTC()})
				saved := store.save()
				store.mu.Unlock()
				if saved != nil {
					t.Fatal(saved)
				}
			} else if err := service.tellCEO(context.Background(), "Run item r1 (build) finished with exit code 0.", false); err != nil {
				t.Fatal(err)
			}
			if lines := terminal.waitForLines(t, 3); len(lines) != 3 || !strings.HasPrefix(lines[2], "queued ") {
				t.Fatalf("terminal input = %q; want the line queued in the turn after his words", lines)
			}
			// Its try left the command on the board with Send now, as a try
			// does where Claude Code's send-now chord reaches the turn.
			now := time.Now().UTC()
			store.mu.Lock()
			store.db.Actions = append(store.db.Actions, Action{ID: "m2", Kind: "message", TaskID: task, Generation: generation, Text: "/compact", Status: "queued", CanSendNow: true, Message: "Queued. " + commandWaits + " " + commandSendsNow, CreatedAt: now, UpdatedAt: now})
			saved := store.save()
			store.mu.Unlock()
			if saved != nil {
				t.Fatal(saved)
			}

			// Act
			pressed := sendNow(service, task, "m2")
			command, words := actionOf(store, "m2"), actionOf(store, "m1")

			// Assert
			if pressed.Code != 409 || !strings.Contains(pressed.Body.String(), commandWaits) || command.IsSentNow {
				t.Errorf("Send now = %d %s, the command = %+v; want it refused, saying the command runs when the turn ends", pressed.Code, pressed.Body, command)
			}
			if !isHarnessHeld(words) {
				t.Errorf("his words after the press = %+v; want them still in the turn's queue", words)
			}
			if input := terminal.lines(t); slices.ContainsFunc(input, func(line string) bool { return strings.HasPrefix(line, "took back ") }) {
				t.Errorf("terminal input = %q; want no key pressed", input)
			}
		})
	}
}

// A notice the board told the CEO in a turn under way waits in its harness's
// queue, where a command he sends now cannot go ahead of it (#2759), until it
// can be there no longer: once the CEO's record shows it taken, once its
// terminal ended or started again, or once that terminal has shown no turn for
// deliveryQuiet, since a harness holds no queue between turns, and at the
// latest after deliveryForget. From then a command can go ahead again.
func TestANoticeHoldsACommandBackOnlyWhileItCanWaitInTheCEOsQueue(t *testing.T) {
	for _, test := range []struct {
		name       string
		look       terminalLook
		age, quiet time.Duration
		isAhead    bool
	}{
		{name: "taken", look: terminalLook{Took: true}, isAhead: true},
		{name: "its terminal gone", look: terminalLook{Gone: true}, isAhead: true},
		{name: "no turn shown for deliveryQuiet", age: deliveryQuiet, quiet: deliveryQuiet, isAhead: true},
		{name: "no turn shown for less", age: deliveryQuiet - time.Minute, quiet: deliveryQuiet - time.Minute},
		{name: "its turn under way", look: terminalLook{Working: true}, age: deliveryQuiet, quiet: deliveryQuiet},
		{name: "a turn under way for deliveryForget", look: terminalLook{Working: true}, age: deliveryForget, isAhead: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			now := time.Now().UTC()
			store.mu.Lock()
			store.db.HeldNotices = []Awaiting{{Host: "ceo", Harness: "claude", Since: now.Add(-test.age), QuietSince: now.Add(-test.quiet), Digest: monitor.TextDigest("Overlord: Run item r1 (build) finished with exit code 0."), IsHeld: true}}
			store.mu.Unlock()

			// Act
			err := store.settleDeliveries(now, func(Awaiting) terminalLook { return test.look })
			_, _, _, isAhead := (&Service{Store: store}).queueAhead("")

			// Assert
			if err != nil || isAhead != test.isAhead {
				t.Errorf("settle = %v, a command can go ahead = %t; want %t", err, isAhead, test.isAhead)
			}
		})
	}
}

// Claude Code's send-now chord takes its turn's queue from the head, so the
// board presses it over a command only while that queue holds nothing else.
// A Send now that finds lines ahead of the command that he typed into that
// queue in its terminal, which the board takes back for no one, is spent by
// that try, which says it could not run it at once, and the board presses
// nothing for it at a later try: one press of his is one try, never a
// standing order to interrupt whatever turn runs next (#1915). Those words
// stay until a new Send now, whose try says how it went, or until the turn
// ends and the command runs. The fixture's turn ends only on Escape, which
// hands his words back onto its prompt line, so he ends it here as he can in
// its terminal, with Escape and then Ctrl+U over those words, which leaves
// the board the empty, ready prompt line it finds at any turn's end.
func TestASendNowThatCannotRunAHeldCommandAtOnceIsSpentAndSaysSo(t *testing.T) {
	// Arrange
	useConversations(t)
	store, home := testStore(t)
	terminal := nativeCEOComposer(t, home.State, "claude-busy")
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}}}
	terminal.typeLine(t, "first words")
	terminal.waitForLines(t, 2)
	promptLineEmpties(t, terminal, "claude")
	if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "/compact"}); err != nil {
		t.Fatal(err)
	}
	tryNext(t, store, service, terminal, "claude")
	if command := actionOf(store, "m1"); command.Status != "queued" {
		t.Fatalf("the command = %+v; want it held on the board", command)
	}
	retry := func() error {
		store.mu.Lock()
		store.deferredUntil["m1"] = time.Now().Add(-time.Second)
		store.mu.Unlock()
		return store.ProcessOne(context.Background(), service.execute)
	}

	// Act
	pressed := sendNow(service, "", "m1")
	err := store.ProcessOne(context.Background(), service.execute)
	spent := actionOf(store, "m1")
	later := retry()
	kept := actionOf(store, "m1")
	pressedAgain := sendNow(service, "", "m1")
	renewed := actionOf(store, "m1")
	again := store.ProcessOne(context.Background(), service.execute)
	spentAgain := actionOf(store, "m1")
	terminal.typeKeys(t, "\x1b")
	terminal.waitForLines(t, 3)
	terminal.typeKeys(t, "\x15")
	terminal.waitForLines(t, 4)
	terminal.showsComposer(t, "claude")
	ended := retry()
	taken := actionOf(store, "m1")
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
	terminal.typeLine(t, "first words")
	terminal.waitForLines(t, 2)
	promptLineEmpties(t, terminal, "claude")
	for _, message := range []Action{{ID: "m1", Kind: "message", Text: "/compact"}, {ID: "m2", Kind: "message", Text: "/clear"}} {
		if _, err := store.Queue(message); err != nil {
			t.Fatal(err)
		}
	}
	tryNext(t, store, service, terminal, "claude")
	if ahead, behind := actionOf(store, "m1"), actionOf(store, "m2"); ahead.Status != "queued" || behind.Status != "queued" || !behind.CanSendNow {
		t.Fatalf("the command ahead = %+v, the one behind it = %+v; want both held on the board, the one behind with Send now", ahead, behind)
	}
	deferAhead := func(until time.Time) {
		store.mu.Lock()
		store.deferredUntil["m1"] = until
		store.mu.Unlock()
	}

	// Act
	deferAhead(time.Now().Add(time.Hour))
	pressed := sendNow(service, "", "m2")
	err := store.ProcessOne(context.Background(), service.execute)
	spent := actionOf(store, "m2")
	deferAhead(time.Now().Add(-time.Second))
	aheadTried := store.ProcessOne(context.Background(), service.execute)
	afterAhead := actionOf(store, "m2")
	if _, queueErr := store.Queue(Action{ID: "m3", Kind: "message", Text: "more words"}); queueErr != nil {
		t.Fatal(queueErr)
	}
	afterQueued := actionOf(store, "m2")

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
	terminal.typeLine(t, "first words")
	terminal.waitForLines(t, 2)
	promptLineEmpties(t, terminal, "claude")
	if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "/compact"}); err != nil {
		t.Fatal(err)
	}
	tryNext(t, store, service, terminal, "claude")
	if pressed := sendNow(service, "", "m1"); pressed.Code != 202 {
		t.Fatalf("Send now = %d %s; want it taken", pressed.Code, pressed.Body)
	}
	if err := store.ProcessOne(context.Background(), service.execute); !errors.Is(err, ErrDeferred) {
		t.Fatalf("its try = %v; want the command held", err)
	}
	if spent := actionOf(store, "m1"); spent.Message != "Queued. Send now could not run it at once, so it runs when its turn ends." {
		t.Fatalf("after its try = %+v; want words that say his Send now could not run it at once", spent)
	}
	retry := func() error {
		store.mu.Lock()
		store.deferredUntil["m1"] = time.Now().Add(-time.Second)
		store.mu.Unlock()
		return store.ProcessOne(context.Background(), service.execute)
	}

	// Act
	typeIntoCEO(t, home.State, "a draft", "a draft")
	blocked := retry()
	held := actionOf(store, "m1")
	typeIntoCEO(t, home.State, "\x15", "Press up to edit queued messages")
	back := retry()
	again := actionOf(store, "m1")

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
// a line he typed into that turn in its terminal back onto its prompt line
// leaves no empty prompt line, so the command is never typed and the Send now
// is spent and says so. A Claude Code that takes no Ctrl+X Ctrl+S leaves the
// command in its turn's queue, typed and sent now, for the CEO's record to
// show it taken. One whose turn asks a question of its own as the command
// goes into its queue is pressed no key at all, since the key would go to
// that question, and the command waits in the turn's queue, typed and not
// sent now, as his words there do.
func TestASendNowWhoseKeyDidNotRunTheCEOsCommandSaysWhereItWaits(t *testing.T) {
	for _, test := range []struct {
		name, mode  string
		harness     string
		typed       string
		wantErr     error
		wantStatus  string
		wantMessage string
		isAwaited   bool
		wantInput   []string
	}{
		{name: "a Codex that hands its queue back onto its prompt line", mode: "busy", harness: "codex", typed: "first words", wantErr: ErrDeferred, wantStatus: "queued", wantMessage: "Queued. Send now could not run it at once, so it runs when its turn ends.", wantInput: []string{"queued first words", "interrupted"}},
		{name: "a Claude Code that ignores its send-now chord", mode: "claude-busy-ignores-send-now", harness: "claude", wantStatus: "running", wantMessage: "Typed while it was working and sent now.", isAwaited: true, wantInput: []string{"queued /compact", "send now ignored"}},
		{name: "a Claude Code whose turn asks a question over its queue", mode: "claude-busy-asks", harness: "claude", wantStatus: "running", wantMessage: heldByCEO, isAwaited: true, wantInput: []string{"queued /compact"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, test.mode)
			service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}}}
			if test.typed != "" {
				terminal.typeLine(t, test.typed)
				terminal.waitForLines(t, 2)
				promptLineEmpties(t, terminal, test.harness)
			}
			if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "/compact"}); err != nil {
				t.Fatal(err)
			}
			tryNext(t, store, service, terminal, test.harness)
			if held := actionOf(store, "m1"); held.Status != "queued" || !held.CanSendNow {
				t.Fatalf("the command = %+v; want it held on the board with Send now", held)
			}

			// Act
			pressed := sendNow(service, "", "m1")
			err := store.ProcessOne(context.Background(), service.execute)
			sent := actionOf(store, "m1")
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

// A press reads what the board typed into a session's queue before it waits
// for the CEO's typing lock and reads that session's prompt line, and a
// delivery the worker runs meanwhile can join that queue, as his next words
// to the CEO do once their undo window ends. The board takes the queue back
// only while it holds just what the press read there: its key would bring the
// rest back onto the prompt line too, where none of it could be taken back.
func TestAQueueIsNotTakenBackOnceADeliveryJoinedItSinceThePressReadIt(t *testing.T) {
	for _, test := range []struct {
		name     string
		isGoblin bool
	}{
		{name: "the CEO's"},
		{name: "a goblin's", isGoblin: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, service, terminal, task, _ := heldQueue(t, test.isGoblin, "claude-busy", "first words", "second words")
			promptLineEmpties(t, terminal, "claude")
			held, record, screens, _ := service.queueAhead(task)
			if len(held) != 2 {
				t.Fatalf("the queue holds %+v; want both messages", held)
			}

			// Act
			err := service.takeQueueBack(held[:1], task, record, screens)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "The board is typing for") {
				t.Errorf("taking back what the press read = %v; want it refused while the queue holds more", err)
			}
			for _, id := range []string{"m1", "m2"} {
				if after := actionOf(store, id); !isHarnessHeld(after) {
					t.Errorf("%s after the press = %+v; want it still held in its harness's queue", id, after)
				}
			}
			if input := terminal.lines(t); slices.ContainsFunc(input, func(line string) bool { return strings.HasPrefix(line, "took back ") }) {
				t.Errorf("terminal input = %q; want no key pressed", input)
			}
		})
	}
}
