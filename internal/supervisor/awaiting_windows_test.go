package supervisor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// sentAnswer queues the Overlord's answer to a CFO question and delivers it
// as typed and submitted into the CFO's terminal at since, with no report
// from the CFO's hook yet.
func sentAnswer(t *testing.T, store *Store, since time.Time) Action {
	t.Helper()
	_, identity, _, _ := primaryFixture(t, store)
	q := Question{ID: "question-1", Identity: identity, Text: "Choose", Options: []string{"One", "Two"}, CreatedAt: since.Add(-time.Minute)}
	if err := store.acceptQuestion(q); err != nil {
		t.Fatal(err)
	}
	a := Action{ID: "answer-1", Kind: "cfo_answer", Generation: identity, QuestionID: q.ID, Text: "One"}
	if _, err := store.Queue(a); err != nil {
		t.Fatal(err)
	}
	deliver := func(context.Context, Action) (Evaluation, error) {
		return Evaluation{Reason: sentToCFO, Awaiting: &Awaiting{Host: "cfo-host", Since: since}}, nil
	}
	if err := store.ProcessOne(context.Background(), deliver); err != nil {
		t.Fatal(err)
	}
	return a
}

// tookPrompt records the CFO's hook reporting a prompt taken in its terminal.
func tookPrompt(t *testing.T, store *Store, at time.Time) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	store.db.Sessions["claude/cfo-1"] = Session{ID: "claude/cfo-1", NativeID: "cfo-1", Harness: "claude", Role: "cfo", Phase: "active", HostID: "cfo-host", PromptAt: at, UpdatedAt: at}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
}

// outcome is what the board shows of the answer and its question.
func outcome(store *Store) (Action, Question) {
	snapshot := store.Snapshot()
	return snapshot.Actions[0], snapshot.Questions[0]
}

var (
	idle    = func(Awaiting) terminalLook { return terminalLook{} }
	working = func(Awaiting) terminalLook { return terminalLook{Working: true} }
	gone    = func(Awaiting) terminalLook { return terminalLook{Gone: true} }
)

// The Overlord, 2026-10-01, on answers that had all arrived: "Delivery
// unconfirmed ... I get these command center blips and errors, fix". The CFO
// took each some seconds after the confirmation window. Such an answer reads
// sent, then delivered once the hook reports it, and never warns.
func TestAnAnswerTheCFOTakesLateReadsSentThenDelivered(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	since := time.Now().UTC().Add(-time.Minute)
	sentAnswer(t, store, since)

	// Act
	sent, asked := outcome(store)
	waiting := store.settleDeliveries(since.Add(20*time.Second), idle)
	still, _ := outcome(store)
	tookPrompt(t, store, since.Add(25*time.Second))
	settled := store.settleDeliveries(since.Add(26*time.Second), idle)
	delivered, answered := outcome(store)

	// Assert
	if waiting != nil || settled != nil {
		t.Fatal(waiting, settled)
	}
	if sent.Status != "running" || sent.Awaiting == nil || sent.Message != sentToCFO || asked.Status != "running" {
		t.Errorf("after the send: action %s %q, question %s; want it sent and on its way", sent.Status, sent.Message, asked.Status)
	}
	if still.Status != "running" {
		t.Errorf("twenty seconds on with no report: action %s %q, want it still sent", still.Status, still.Message)
	}
	if delivered.Status != "succeeded" || delivered.Awaiting != nil || !strings.Contains(delivered.Message, "hook reported") {
		t.Errorf("after the hook's report: action %s %q, want it delivered", delivered.Status, delivered.Message)
	}
	if answered.Status != "succeeded" || answered.AnsweredBy != "overlord" || answered.AnsweredOption != "One" {
		t.Errorf("question = %+v, want it answered by the Overlord with One", answered)
	}
}

// An answer whose terminal shows no turn for the quiet limit with still no
// report never arrived: only then does it warn, in words that say what to do,
// and a report that comes later still delivers it.
func TestAnAnswerThatNeverArrivesWarnsInPlainWordsAndALateReportStillDeliversIt(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	since := time.Now().UTC().Add(-time.Hour)
	sentAnswer(t, store, since)

	// Act
	early := store.settleDeliveries(since.Add(deliveryQuiet-time.Second), idle)
	patient, _ := outcome(store)
	late := store.settleDeliveries(since.Add(deliveryQuiet+time.Second), idle)
	warned, question := outcome(store)
	tookPrompt(t, store, since.Add(deliveryQuiet+time.Minute))
	after := store.settleDeliveries(since.Add(deliveryQuiet+2*time.Minute), idle)
	delivered, answered := outcome(store)

	// Assert
	if early != nil || late != nil || after != nil {
		t.Fatal(early, late, after)
	}
	if patient.Status != "running" {
		t.Errorf("just inside the quiet limit: action %s, want it still sent", patient.Status)
	}
	if warned.Status != "uncertain" || question.Status != "uncertain" || !strings.Contains(warned.Advice, "press Enter") || strings.Contains(warned.Advice, "queue") {
		t.Errorf("past the quiet limit: action %s %q, question %s; want a warning that says what to do", warned.Status, warned.Message, question.Status)
	}
	if delivered.Status != "succeeded" || delivered.Advice != "" || answered.Status != "succeeded" {
		t.Errorf("after a late report: action %s advice %q, question %s; want both delivered and no advice left", delivered.Status, delivered.Advice, answered.Status)
	}
}

// An answer waits behind a turn for as long as the terminal shows the turn:
// the quiet limit counts only from the last look that showed it working.
func TestAnAnswerBehindALongTurnNeverWarnsWhileItsTerminalShowsTheTurn(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	since := time.Now().UTC().Add(-2 * time.Hour)
	sentAnswer(t, store, since)

	// Act
	for elapsed := time.Minute; elapsed <= time.Hour; elapsed += time.Minute {
		if err := store.settleDeliveries(since.Add(elapsed), working); err != nil {
			t.Fatal(err)
		}
	}
	behind, _ := outcome(store)
	quiet := store.settleDeliveries(since.Add(time.Hour+deliveryQuiet-time.Second), idle)
	ended, _ := outcome(store)

	// Assert
	if quiet != nil {
		t.Fatal(quiet)
	}
	if behind.Status != "running" || ended.Status != "running" {
		t.Errorf("behind an hour-long turn: action %s, then %s just after it; want it sent throughout", behind.Status, ended.Status)
	}
}

// An answer whose terminal closed before any report can no longer arrive, so
// it warns at once.
func TestAnAnswerWhoseTerminalClosedWarnsAtOnce(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	since := time.Now().UTC().Add(-time.Minute)
	sentAnswer(t, store, since)

	// Act
	err := store.settleDeliveries(since.Add(10*time.Second), gone)
	warned, _ := outcome(store)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if warned.Status != "uncertain" || !strings.Contains(warned.Advice, "closed") {
		t.Errorf("action %s %q, want a warning that the terminal closed", warned.Status, warned.Message)
	}
}

// A supervisor restart interrupts nothing of an answer already typed and
// submitted: it stays sent and is settled as before.
func TestARestartKeepsASentAnswerSent(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	since := time.Now().UTC().Add(-time.Minute)
	sentAnswer(t, store, since)

	// Act
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	kept, question := outcome(reopened)
	tookPrompt(t, reopened, since.Add(30*time.Second))
	settled := reopened.settleDeliveries(since.Add(31*time.Second), idle)
	delivered, _ := outcome(reopened)

	// Assert
	if settled != nil {
		t.Fatal(settled)
	}
	if kept.Status != "running" || kept.Awaiting == nil || question.Status != "running" {
		t.Errorf("after the restart: action %s %q, question %s; want it still sent", kept.Status, kept.Message, question.Status)
	}
	if delivered.Status != "succeeded" {
		t.Errorf("after the hook's report: action %s, want it delivered", delivered.Status)
	}
}

// He is never left wondering whether to send again: while an answer is sent,
// a second answer to the same question is refused, so nothing is typed twice.
func TestASecondAnswerIsRefusedWhileTheFirstIsSent(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	first := sentAnswer(t, store, time.Now().UTC())
	second := first
	second.ID, second.Text = "answer-2", "Two"

	// Act
	_, err := store.Queue(second)

	// Assert
	if err == nil {
		t.Fatal("a second answer was queued while the first was sent")
	}
	if actions := store.Snapshot().Actions; len(actions) != 1 {
		t.Errorf("actions = %d, want only the first answer", len(actions))
	}
}

// A native goblin's harness reports taking what waited behind its turn, so
// its delivery is sent and then delivered; a goblin in a Herdr pane reports
// nothing the board can wait on, so its delivery reads as it did.
func TestOnlyANativeGoblinsDeliveryAwaitsItsHook(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := &Service{Store: store}
	since := time.Now().UTC()
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}

	// Act
	pane := s.behindGoblinsTurn("task-1", since, "Submitted while it was working.")
	meta.Backend = "native"
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	native := s.behindGoblinsTurn("task-1", since, "Submitted while it was working.")

	// Assert
	if pane.Awaiting != nil || pane.Reason != "Submitted while it was working." {
		t.Errorf("a Herdr goblin's delivery = %+v, want it to read as before", pane)
	}
	if native.Awaiting == nil || native.Awaiting.Task != "task-1" || native.Awaiting.Generation != meta.SpawnGen || native.Reason != sentToGoblin {
		t.Errorf("a native goblin's delivery = %+v, want it sent and awaiting the goblin's hook", native)
	}
}

// An answer to a goblin's review item that waits behind the goblin's turn is
// sent, not delivered: the item reads delivered only once the goblin's hook
// reports taking it.
func TestAReviewAnswerBehindAGoblinsTurnIsDeliveredOnlyWhenItsHookReports(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	since := time.Now().UTC().Add(-time.Minute)
	r := openReview("plan-task-1", "task-1")
	if err := store.acceptReview(r); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queue(Action{ID: "answer-review-1", Kind: "review_answer", ReviewID: r.ID, Generation: r.Identity, Text: "Go with the grid"}); err != nil {
		t.Fatal(err)
	}
	deliver := func(context.Context, Action) (Evaluation, error) {
		return Evaluation{Reason: sentToGoblin, Awaiting: &Awaiting{Task: "task-1", Generation: "g1", Since: since}}, nil
	}
	if err := store.ProcessOne(context.Background(), deliver); err != nil {
		t.Fatal(err)
	}

	// Act
	sent := store.Snapshot().Reviews[0]
	store.mu.Lock()
	store.db.Sessions["codex/worker-1"] = Session{ID: "codex/worker-1", NativeID: "worker-1", Harness: "codex", Role: "goblin", Phase: "active", TaskID: "task-1", Generation: "g1", PromptAt: since.Add(40 * time.Second)}
	store.mu.Unlock()
	settled := store.settleDeliveries(since.Add(41*time.Second), idle)
	delivered := store.Snapshot().Reviews[0]

	// Assert
	if settled != nil {
		t.Fatal(settled)
	}
	if sent.State != "answered" || sent.Delivered {
		t.Errorf("behind the goblin's turn: review %s delivered=%t, want it answered and not yet delivered", sent.State, sent.Delivered)
	}
	if !delivered.Delivered || store.Snapshot().Actions[0].Status != "succeeded" {
		t.Errorf("after the goblin's hook reported: delivered=%t action %s, want it delivered", delivered.Delivered, store.Snapshot().Actions[0].Status)
	}
}

// A review answer awaited on the CFO's terminal delivers the item only when
// the CFO is its reporter: a goblin's item handed to the CFO because the
// goblin restarted or ended never reached the goblin, so it stays undelivered.
func TestAReviewAnswerAwaitedOnTheCFOIsDeliveredOnlyToItsOwnReporter(t *testing.T) {
	tests := []struct {
		name      string
		task      string
		delivered bool
	}{
		{"a goblin's item handed to the CFO", "task-1", false},
		{"the CFO's own item", "", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: the CFO runs, since a delivery to a closed one waits.
			store, _ := testStore(t)
			writeRegistration(t, store.Home.State, thisProcess(t))
			since := time.Now().UTC().Add(-time.Minute)
			r := openReview("plan-review-1", test.task)
			if err := store.acceptReview(r); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Queue(Action{ID: "answer-review-1", Kind: "review_answer", ReviewID: r.ID, Generation: r.Identity, Text: "Go with the grid"}); err != nil {
				t.Fatal(err)
			}
			deliver := func(context.Context, Action) (Evaluation, error) {
				return Evaluation{Reason: sentToCFO, Awaiting: &Awaiting{Host: "cfo-host", Since: since}}, nil
			}
			if err := store.ProcessOne(context.Background(), deliver); err != nil {
				t.Fatal(err)
			}

			// Act
			tookPrompt(t, store, since.Add(40*time.Second))
			settled := store.settleDeliveries(since.Add(41*time.Second), idle)
			snapshot := store.Snapshot()

			// Assert
			if settled != nil {
				t.Fatal(settled)
			}
			if snapshot.Actions[0].Status != "succeeded" {
				t.Errorf("after the CFO's hook reported: action %s, want it succeeded", snapshot.Actions[0].Status)
			}
			if snapshot.Reviews[0].Delivered != test.delivered {
				t.Errorf("after the CFO's hook reported: delivered=%t, want %t", snapshot.Reviews[0].Delivered, test.delivered)
			}
		})
	}
}
