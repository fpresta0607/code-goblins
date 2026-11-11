package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// leftTypedForCEO delivers message m1 to a CEO whose harness reads nothing
// for longer than the delivery waits to see its words, with m2 queued behind
// it, and returns once that delivery has ended and the harness has read the
// words onto its prompt line.
func leftTypedForCEO(t *testing.T, mode string) (*Service, hostedTerminal) {
	t.Helper()
	previous := nativeShown
	nativeShown = time.Second
	t.Cleanup(func() { nativeShown = previous })
	useConversations(t)
	store, home := testStore(t)
	terminal := nativeCEOComposer(t, home.State, mode)
	service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
	for id, text := range map[string]string{"m1": "stop and rebase", "m2": "then merge it"} {
		if _, err := store.Queue(Action{ID: id, Kind: "message", Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	slices.SortFunc(store.db.Actions, func(a, b Action) int { return strings.Compare(a.ID, b.ID) })
	pastUndoWindow(t, store)
	stall := terminal.typed + ".stall"
	if err := os.WriteFile(stall, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	if unread := actionOf(store, "m1"); unread.Status != "uncertain" || unread.LeftTyped == nil || unread.LeftTyped.Typed != "stop and rebase" {
		t.Fatalf("the delivery its harness never read = %+v; want it unconfirmed, its words remembered as left typed", unread)
	}
	if err := os.Remove(stall); err != nil {
		t.Fatal(err)
	}
	ceoShows(t, home.State, "stop and rebase")
	return service, terminal
}

// A harness that reads nothing for longer than a delivery waits shows the
// delivery's words on its prompt line once it reads again, unsubmitted, and
// every message after them waited behind them for good (#2306). The board
// takes them back as soon as they show, so that message reads Not sent and
// the next one goes.
func TestWordsACEOReadOnlyAfterTheirDeliveryEndedAreTakenBackAndTheNextMessageGoes(t *testing.T) {
	for _, test := range []struct{ mode, kind string }{
		{mode: "claude-busy-stalls", kind: "claude"},
		{mode: "busy-stalls", kind: "codex"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			// Arrange
			service, terminal := leftTypedForCEO(t, test.mode)
			store := service.Store
			held := store.ProcessOne(context.Background(), service.execute)

			// Act
			err := service.takeBackLeftTyped(context.Background())
			takenBack := actionOf(store, "m1")
			store.deferredUntil["m2"] = time.Now().Add(-time.Second)
			next := store.ProcessOne(context.Background(), service.execute)

			// Assert
			if !errors.Is(held, ErrDeferred) {
				t.Errorf("the next message behind the left words = %v; want it held back, nothing typed onto them", held)
			}
			if err != nil || takenBack.Status != "failed" || !strings.HasPrefix(takenBack.Message, "Not sent") || takenBack.LeftTyped != nil {
				t.Errorf("take back = %v, action = %+v; want it Not sent with nothing left to watch", err, takenBack)
			}
			if sent := actionOf(store, "m2"); next != nil || sent.Status != "running" || sent.Awaiting == nil {
				t.Errorf("the next message = %v, %+v; want it typed into the turn", next, sent)
			}
			input := terminal.waitForLines(t, 3)
			if !slices.Contains(input, "cleared") || !slices.Contains(input, "queued then merge it") || strings.Contains(strings.Join(input, "\n"), "stop and rebase") {
				t.Errorf("terminal input = %q; want the left words taken back and only the next message submitted", input)
			}
			ceoPromptLineEmpties(t, service.Store.Home.State, test.kind)
		})
	}
}

// Words of his own beside the ones a delivery left typed make the prompt line
// his, so nothing on it is taken back.
func TestWordsLeftTypedBesideHisOwnStayOnThePromptLine(t *testing.T) {
	// Arrange
	service, terminal := leftTypedForCEO(t, "claude-busy-stalls")
	typeIntoCEO(t, service.Store.Home.State, " and my own words", "stop and rebase and my own words")

	// Act
	err := service.takeBackLeftTyped(context.Background())

	// Assert
	if left := actionOf(service.Store, "m1"); err != nil || left.Status != "uncertain" || left.LeftTyped == nil {
		t.Errorf("take back = %v, action = %+v; want it still watched", err, left)
	}
	if input := terminal.lines(t); slices.Contains(input, "cleared") {
		t.Errorf("terminal input = %q; want his prompt line left as it is", input)
	}
}

// Words left typed in a terminal that is gone, or that ended and was started
// again since, can never show there, so they are watched no longer and the
// message keeps what it read. The same words on the new terminal's prompt
// line are his own, and stay.
func TestWordsLeftTypedInATerminalThatEndedAreWatchedNoLonger(t *testing.T) {
	for _, test := range []struct {
		name        string
		isRestarted bool
	}{
		{name: "a terminal that is gone"},
		{name: "a terminal started again since", isRestarted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, home := testStore(t)
			service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
			typedAt := time.Now().UTC()
			var terminal hostedTerminal
			if test.isRestarted {
				typedAt = typedAt.Add(-time.Hour)
				terminal = nativeCEOComposer(t, home.State, "claude-busy")
				typeIntoCEO(t, home.State, "stop and rebase", "stop and rebase")
			}
			if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "stop and rebase"}); err != nil {
				t.Fatal(err)
			}
			pastUndoWindow(t, store)
			if err := store.ProcessOne(context.Background(), func(context.Context, Action) (Evaluation, error) {
				return Evaluation{LeftTyped: &Awaiting{Host: "ceo", Harness: "claude", Since: typedAt, Typed: "stop and rebase"}}, errors.New("The answer was typed but never showed in the CEO's input.")
			}); err != nil {
				t.Fatal(err)
			}

			// Act
			err := service.takeBackLeftTyped(context.Background())

			// Assert
			if left := actionOf(store, "m1"); err != nil || left.Status != "uncertain" || left.LeftTyped != nil || !strings.Contains(left.Message, "never showed") {
				t.Errorf("take back = %v, action = %+v; want it unconfirmed as it read, watched no longer", err, left)
			}
			if test.isRestarted {
				if input := terminal.lines(t); slices.Contains(input, "cleared") {
					t.Errorf("terminal input = %q; want the words on the new terminal's prompt line left there", input)
				}
			}
		})
	}
}

// A message to a goblin whose words were taken back off its prompt line
// reads Not sent, and one whose words may still be typed there stays
// unconfirmed with those words remembered, to be taken back once they show.
func TestAGoblinsMessageThatWasTypedAndNeverSubmittedReadsAsItEnded(t *testing.T) {
	for _, test := range []struct {
		name       string
		ended      error
		wantStatus string
		wantSays   string
		wantLeft   bool
	}{
		{name: "taken back", ended: spawn.ErrTakenBack, wantStatus: "failed", wantSays: "Not sent"},
		{name: "left typed", ended: spawn.ErrLeftTyped, wantStatus: "uncertain", wantSays: "It was typed for task-1 and never submitted.", wantLeft: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta := makeNative(t, h.State, "task-1")
			gitWorktree(t, meta.Worktree)
			goblinTerminal(t, h.State, meta.ID)
			service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: h.State}, Gate: fakeProgress{err: pipeline.ErrNoProgress}}}
			messageTo(t, store, meta, "message-1", "stop and rebase")
			previous := typeIntoGoblin
			t.Cleanup(func() { typeIntoGoblin = previous })
			typeIntoGoblin = func(context.Context, string, state.TaskMeta, string, bool, bool, bool) error {
				return fmt.Errorf("spawn: the instruction typed into native terminal task-1 never showed in its composer, so it was not submitted: %w", test.ended)
			}

			// Act
			err := store.ProcessOne(context.Background(), service.execute)

			// Assert
			ended := actionOf(store, "message-1")
			if err != nil || ended.Status != test.wantStatus || !strings.HasPrefix(ended.Message, test.wantSays) {
				t.Errorf("delivery = %v, action = %+v; want it %s saying %q", err, ended, test.wantStatus, test.wantSays)
			}
			if isLeft := ended.LeftTyped != nil && ended.LeftTyped.Task == "task-1" && strings.Contains(ended.LeftTyped.Typed, "stop and rebase"); isLeft != test.wantLeft {
				t.Errorf("left typed = %+v, want remembered %t", ended.LeftTyped, test.wantLeft)
			}
		})
	}
}

// A message its session's own record shows taken while its delivery still
// waits to see that is delivered, and how the delivery's own wait ends later
// changes nothing.
func TestAMessageTakenWhileItsDeliveryStillWaitedStaysDelivered(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta := makeNative(t, h.State, "task-1")
	messageTo(t, store, meta, "message-1", "stop and rebase")
	typed := awaitingGoblinDelivery(meta, time.Now().UTC(), "stop and rebase")

	// Act
	err := store.ProcessOne(context.Background(), func(_ context.Context, a Action) (Evaluation, error) {
		if err := store.markTyped(a.ID, a.TaskID, typed); err != nil {
			return Evaluation{}, err
		}
		if err := store.settleDeliveries(time.Now().UTC(), func(Awaiting) terminalLook { return terminalLook{Working: true, Took: true} }); err != nil {
			return Evaluation{}, err
		}
		return Evaluation{Reason: sentToGoblin, Awaiting: typed}, nil
	})

	// Assert
	if taken := actionOf(store, "message-1"); err != nil || taken.Status != "succeeded" || taken.Awaiting != nil || !strings.HasPrefix(taken.Message, "Taken by the goblin") {
		t.Errorf("delivery = %v, action = %+v; want it delivered as its record showed", err, taken)
	}
}
