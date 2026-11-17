package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// ceoPromptLineEmpties waits until the CEO's prompt line reads empty to the
// screens of kind, its harness.
func ceoPromptLineEmpties(t *testing.T, stateDir, kind string) {
	t.Helper()
	record, err := host.ReadRecord(stateDir, "ceo")
	if err != nil {
		t.Fatal(err)
	}
	screens, _ := harness.NativeScreens(harness.Kind(kind))
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		screen, err := host.ReadScreen(record)
		if err == nil && screens.ComposerEmpty(screen) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the CEO's prompt line never read empty: %v\n%s", err, host.ScreenTail(screen, 8))
		}
	}
}

// pressOnQueued posts what the page posts for a press on queued message id to
// task: kind message_now for Send now, message_withdraw for Edit and Cancel.
func pressOnQueued(service *Service, kind, task, id, text string) *httptest.ResponseRecorder {
	return boardRequest(NewHTTP(service, "board.local", nil), "POST", "/api/actions", fmt.Sprintf(`{"id":%q,"kind":%q,"task_id":%q,"generation":"","target":%q,"text":%q}`, kind+"-"+id, kind, task, id, text))
}

// The Overlord, 2026-11-10: "queued messages in modern chat get stuck in input
// box in terminal chat and never send". A harness in a turn reads what is
// typed into it late: the live CEO's Claude Code left typed input unread for
// more than three seconds again and again that day, and a delivery that gave
// up on seeing its words after five left them on the prompt line unsubmitted,
// where every later message waited behind them for good (#2306). His message
// queued while the CEO works is typed once and submitted once its words show,
// here seven seconds late, so it reads Queued in the CEO's session with the
// prompt line empty, and it is delivered once the turn takes it.
func TestHisQueuedMessageReachesACEOThatReadsItLateExactlyOnce(t *testing.T) {
	for _, test := range []struct{ mode, kind string }{
		{mode: "claude-busy-stalls", kind: "claude"},
		{mode: "busy-stalls", kind: "codex"},
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
			stall := terminal.typed + ".stall"
			if err := os.WriteFile(stall, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			// stall-accepted: it is how long the harness reads nothing, which the test means to run out; the delivery waits for it in time the machine ran.
			reads := time.AfterFunc(nativeConfirm+2*time.Second, func() { _ = os.Remove(stall) })
			t.Cleanup(func() { reads.Stop() })

			// Act
			err := store.ProcessOne(context.Background(), service.execute)
			sent := actionOf(store, "m1")

			// Assert
			if err != nil || sent.Status != "running" || sent.Awaiting == nil || sent.Message != heldByCEO {
				t.Fatalf("delivery = %v, action = %+v; want it typed into the turn and reading Queued in the CEO's session", err, sent)
			}
			if lines := terminal.waitForLines(t, 2); len(lines) != 2 || lines[1] != "queued stop and rebase" {
				t.Fatalf("terminal input = %q; want his message submitted into the turn once", lines)
			}
			ceoPromptLineEmpties(t, home.State, test.kind)
			if err := os.WriteFile(terminal.typed+".accept", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			terminal.waitForLines(t, 3)
			takenInRecord(t, records, test.kind, "session-1", "", "stop and rebase")
			if err := store.settleDeliveries(time.Now().UTC(), service.lookAtTerminal); err != nil {
				t.Fatal(err)
			}
			if delivered := actionOf(store, "m1"); delivered.Status != "succeeded" || delivered.Awaiting != nil {
				t.Errorf("once the CEO's record shows it taken = %+v; want it delivered", delivered)
			}
			if input := strings.Join(terminal.lines(t), "\n"); strings.Count(input, "queued stop and rebase") != 1 || strings.Count(input, "accepted stop and rebase") != 1 {
				t.Errorf("terminal input = %q; want one submission and one take", input)
			}
		})
	}
}

// A message whose submit key its session never took reached nobody, so it
// reads Not sent rather than as one that may have arrived, and its words are
// taken back off the terminal's prompt line, where they would join whatever
// is typed next or hold it back (#2306). Cancel then takes it off the board.
func TestAMessageTheCEONeverTookAsSubmittedReadsNotSentAndLeavesItsPromptLineEmpty(t *testing.T) {
	for _, test := range []struct{ mode, kind string }{
		{mode: "claude-ignored-enter", kind: "claude"},
		{mode: "ignored-enter", kind: "codex"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			// Arrange
			useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, test.mode)
			service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}}}
			if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "stop and rebase"}); err != nil {
				t.Fatal(err)
			}
			pastUndoWindow(t, store)

			// Act
			err := store.ProcessOne(context.Background(), service.execute)
			unsent := actionOf(store, "m1")
			cancel := pressOnQueued(service, "message_withdraw", "", "m1", "stop and rebase")

			// Assert
			if err != nil || unsent.Status != "failed" || !strings.HasPrefix(unsent.Message, "Not sent") {
				t.Errorf("delivery = %v, action = %+v; want it to read Not sent", err, unsent)
			}
			ceoPromptLineEmpties(t, home.State, test.kind)
			if input := terminal.lines(t); !slices.Contains(input, "cleared") || slices.ContainsFunc(input, func(line string) bool {
				return strings.HasPrefix(line, "accepted ") || strings.HasPrefix(line, "queued ")
			}) {
				t.Errorf("terminal input = %q; want his words taken back off the prompt line and nothing submitted", input)
			}
			if cancel.Code != 202 || slices.ContainsFunc(store.Snapshot().Actions, func(a Action) bool { return a.ID == "m1" }) {
				t.Errorf("Cancel = %d %s, actions = %+v; want the unsent message taken off the board", cancel.Code, cancel.Body, store.Snapshot().Actions)
			}
		})
	}
}

// Send now on a message that reads Not sent puts that message back in the
// queue to go at once, where it once answered that the message had left the
// board.
func TestSendNowOnAMessageThatWasNotSentSendsItAgain(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	service := &Service{Store: store, Instance: "instance-1"}
	if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "stop and rebase"}); err != nil {
		t.Fatal(err)
	}
	pastUndoWindow(t, store)
	if err := store.ProcessOne(context.Background(), func(context.Context, Action) (Evaluation, error) {
		return Evaluation{}, fmt.Errorf("%w: the CEO's native terminal does not answer; nothing was sent", ErrRejected)
	}); err != nil {
		t.Fatal(err)
	}
	if unsent := actionOf(store, "m1"); unsent.Status != "failed" {
		t.Fatalf("the refused message = %+v; want it failed", unsent)
	}

	// Act
	pressed := sendNow(service, "", "m1")

	// Assert
	if again := actionOf(store, "m1"); pressed.Code != 202 || again.Status != "queued" || !again.IsSentNow {
		t.Errorf("Send now = %d %s, action = %+v; want the same message queued to go now", pressed.Code, pressed.Body, again)
	}
}

// A message that reads Not sent once its words were taken back off the CEO's
// prompt line can still carry the watch its first try left on them, and its
// Send now carried that watch into the try that sends it: a look at the CEO's
// terminal during that try called it never picked up, and the try's own
// outcome was dropped (#2541). Sent now, it is settled by how that try ends.
func TestAMessageSentNowOnceItWasNotSentIsSettledByItsNewTry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result Evaluation
		err    error
		want   string
	}{
		{name: "a try the CEO cannot take yet", result: Evaluation{Reason: "The CEO's input is not empty and ready. Your answer is queued; nothing was sent."}, err: ErrDeferred, want: "queued"},
		{name: "a try the CEO takes", result: Evaluation{Reason: "Taken by the CEO in its native terminal, as its hook reported."}, want: "succeeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			service := &Service{Store: store, Instance: "instance-1"}
			first := time.Now().UTC().Add(-time.Hour)
			watch := &Awaiting{Host: "ceo", Harness: "claude", Since: first, QuietSince: first, Digest: monitor.TextDigest("stop and rebase"), Session: "session-1", Typed: "stop and rebase"}
			store.db.Actions = append(store.db.Actions, Action{ID: "m1", Kind: "message", Text: "stop and rebase", Status: "uncertain", CreatedAt: first, UpdatedAt: first, Awaiting: watch, LeftTyped: watch})
			// Its words showed alone on the CEO's prompt line and were taken
			// back off it.
			if err := store.settleLeftTyped([]Action{store.db.Actions[0]}, nil, first); err != nil {
				t.Fatal(err)
			}
			if unsent := actionOf(store, "m1"); !isNotSent(unsent) || unsent.Awaiting == nil {
				t.Fatalf("once its words were taken back = %+v; want it Not sent, its first try's watch still on it", unsent)
			}
			if pressed := sendNow(service, "", "m1"); pressed.Code != 202 {
				t.Fatalf("Send now = %d %s; want it accepted", pressed.Code, pressed.Body)
			}

			// Act
			err := store.ProcessOne(context.Background(), func(context.Context, Action) (Evaluation, error) {
				// The supervisor looks at the CEO's terminal while the try
				// runs, and finds it idle with nothing taken.
				if err := store.settleDeliveries(time.Now().UTC(), func(Awaiting) terminalLook { return terminalLook{} }); err != nil {
					return Evaluation{}, err
				}
				return tc.result, tc.err
			})
			tried := actionOf(store, "m1")

			// Assert
			if !errors.Is(err, tc.err) {
				t.Fatalf("ProcessOne = %v; want %v", err, tc.err)
			}
			if tried.Status != tc.want || tried.Message != tc.result.Reason || tried.Awaiting != nil {
				t.Errorf("after its new try = %s %q, awaiting %+v; want it %s, as that try ended", tried.Status, tried.Message, tried.Awaiting, tc.want)
			}
		})
	}
}

// #1915: "A queued message keeps its "Queued" bubble after the session has
// already taken it." A message typed into a goblin's turn is its harness's
// from the moment its submit key went, while its delivery still waits a few
// seconds to see it taken, so from then it reads Queued in the goblin's
// session, with the controls that act on that queue (#2332), rather than
// Queued on the board, and a Cancel pressed meanwhile acts once that wait
// ends: here it says the goblin's harness gives its queue back to no key,
// rather than to press again.
func TestAMessageInAGoblinsTurnLeavesTheBoardsQueueTheMomentItsSubmitKeyWent(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta := makeNative(t, h.State, "task-1")
	gitWorktree(t, meta.Worktree)
	goblin := goblinTerminal(t, h.State, meta.ID)
	goblin.startTurn(t)
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: h.State}, Gate: fakeProgress{err: pipeline.ErrNoProgress}}}
	messageTo(t, store, meta, "message-1", "stop and rebase")
	delivered := make(chan error, 1)

	// Act
	go func() { delivered <- store.ProcessOne(context.Background(), service.execute) }()
	goblin.waitForLines(t, 1)
	var typed Action
	for deadline := time.Now().Add(time.Minute); typed.Awaiting == nil && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		typed = actionOf(store, "message-1")
	}
	isStillDelivering := false
	select {
	case <-delivered:
	case <-time.After(time.Second): // stall-accepted: nothing is meant to arrive, since the delivery still waits to see the message taken.
		isStillDelivering = true
	}
	cancel := pressOnQueued(service, "message_withdraw", meta.ID, "message-1", "stop and rebase")

	// Assert
	if !isStillDelivering || typed.Status != "running" || typed.Awaiting == nil || !typed.Awaiting.IsHeld || typed.Message != heldByGoblin {
		t.Errorf("still delivering = %t, action = %+v; want it to read Queued in the goblin's session while its delivery still waits to see it taken", isStillDelivering, typed)
	}
	if cancel.Code != 409 || !strings.Contains(cancel.Body.String(), "It waits in task-1's own queue, which its harness gives back to no key. Send now has it taken at once.") {
		t.Errorf("Cancel = %d %s; want it to say the goblin's harness gives its queue back to no key", cancel.Code, cancel.Body)
	}
	if isStillDelivering {
		if err := <-delivered; err != nil {
			t.Errorf("delivery = %v", err)
		}
	}
	if sent := actionOf(store, "message-1"); sent.Status != "running" || sent.Awaiting == nil || sent.Message != heldByGoblin {
		t.Errorf("after its delivery = %+v; want it still queued in the goblin's session, awaiting the goblin's next tool call", sent)
	}
	if lines := goblin.lines(t); len(lines) != 1 || !strings.Contains(lines[0], "stop and rebase") {
		t.Errorf("the goblin received %q, want his message once", lines)
	}
}

// #1915: "Send now, Edit and Cancel on a queued message do not work while a
// Claude turn runs". The board tries a queued message again and again, and a
// press that lands inside a try was turned away to "press again in a moment".
// It now waits for the try and acts on how it ended: Cancel, which Edit sends
// too, takes back a message the try left queued, and Send now answers with
// what the try found.
func TestAPressOnAQueuedMessageDuringATryActsOnHowTheTryEnded(t *testing.T) {
	for _, test := range []struct {
		press    string
		wantCode int
		wantSays string
		wantHeld bool
	}{
		{press: "message_withdraw", wantCode: 202},
		{press: "message_now", wantCode: 409, wantSays: "Its terminal is not ready to take it yet.", wantHeld: true},
	} {
		t.Run(test.press, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta := makeNative(t, h.State, "task-1")
			gitWorktree(t, meta.Worktree)
			goblinTerminal(t, h.State, meta.ID)
			service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: h.State}, Gate: fakeProgress{err: pipeline.ErrNoProgress}}}
			messageTo(t, store, meta, "message-1", "stop and rebase")
			trying, ends := make(chan struct{}), make(chan struct{})
			previous := typeIntoGoblin
			t.Cleanup(func() { typeIntoGoblin = previous })
			typeIntoGoblin = func(_ context.Context, _ string, meta state.TaskMeta, _ string, _, _, _ bool) error {
				close(trying)
				<-ends
				return fmt.Errorf("spawn: native terminal %s: %w", meta.ID, fleet.ErrNotTyped)
			}
			tried := make(chan error, 1)
			go func() { tried <- store.ProcessOne(context.Background(), service.execute) }()
			<-trying
			// stall-accepted: it ends the try once the press has landed; a press a stall lands after the try ended is answered the same.
			time.AfterFunc(300*time.Millisecond, func() { close(ends) })

			// Act
			pressed := pressOnQueued(service, test.press, meta.ID, "message-1", "stop and rebase")
			<-tried

			// Assert
			if pressed.Code != test.wantCode || !strings.Contains(pressed.Body.String(), test.wantSays) {
				t.Errorf("%s during a try = %d %s; want %d saying %q", test.press, pressed.Code, pressed.Body, test.wantCode, test.wantSays)
			}
			if isHeld := slices.ContainsFunc(store.Snapshot().Actions, func(a Action) bool { return a.ID == "message-1" && a.Status == "queued" }); isHeld != test.wantHeld {
				t.Errorf("still queued = %t, want %t: %+v", isHeld, test.wantHeld, store.Snapshot().Actions)
			}
		})
	}
}

// #1915 quotes what Send now answered on a message to a goblin that the board
// had not tried yet: "It cannot be typed into its session now". Such a message
// only waits its turn behind what the board is delivering, so Send now is
// kept for its try, which has its harness take it at once.
func TestSendNowOnAGoblinsMessageTheBoardHasNotTriedYetIsKeptForItsTry(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta := makeNative(t, h.State, "task-1")
	gitWorktree(t, meta.Worktree)
	goblinTerminal(t, h.State, meta.ID)
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: h.State}, Gate: fakeProgress{err: pipeline.ErrNoProgress}}}
	messageTo(t, store, meta, "message-1", "stop and rebase")
	isWorking := true
	received, sentNow := inTurnGoblin(t, &isWorking)

	// Act
	pressed := pressOnQueued(service, "message_now", meta.ID, "message-1", "")
	err := store.ProcessOne(context.Background(), service.execute)

	// Assert
	if pressed.Code != 202 {
		t.Errorf("Send now = %d %s; want it taken", pressed.Code, pressed.Body)
	}
	if err != nil || len(*received) != 1 || len(*sentNow) != 1 {
		t.Errorf("delivery = %v, typed %q, sent now %q; want his message typed once for its harness to take at once", err, *received, *sentNow)
	}
}
