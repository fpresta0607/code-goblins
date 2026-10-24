package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The Command Center kept eight questions the CFO had already answered: each
// goblin was busy, so cfo answer's text waited in its composer, SendGoblin
// reported the delivery unconfirmed, and cfo answer returned before it
// recorded the answer on the board or marked the notify answered. A queued
// delivery is a delivery: it is recorded and the notify reads answered, so
// the board's own answer path refuses a second, possibly different decision.
func TestCFOAnswerToABusyGoblinIsRecordedSoTheBoardRefusesAnother(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	primaryFixture(t, store)
	meta, record, goblin, connection := goblinFixture(t, store)
	servePipe(t, store, connection)
	q := surfaced(t, store, meta, record, connection)
	goblin.startTurn(t)

	// Act
	chosen, queued, err := connection.AnswerGoblin(context.Background(), q.ID, "SQLite", "")

	// Assert
	if err != nil || chosen != "SQLite" || !queued {
		t.Fatalf("answer = %q, queued %v, %v; want SQLite recorded as queued for the goblin's next tool call", chosen, queued, err)
	}
	if typed := goblin.lines(t); len(typed) != 1 {
		t.Fatalf("the goblin received %q, want the answer submitted once", typed)
	}
	pending, err := wake.Pending(h.State)
	if err != nil || len(pending) != 1 || pending[0].Answered != "SQLite" || pending[0].AnsweredBy != wake.AnsweredByCFO {
		t.Fatalf("notify = %+v (%v), want it marked answered by the CFO", pending, err)
	}
	if err := os.MkdirAll(nativehook.SpoolDir(h.State), 0700); err != nil {
		t.Fatal(err)
	}
	(&Service{Store: store, work: make(chan struct{}, 1)}).cycle(context.Background(), false)
	onItsWay := store.Snapshot().Questions[0]
	if onItsWay.Status != "running" || onItsWay.AnsweredBy != "cfo" || onItsWay.AnsweredOption != "SQLite" || onItsWay.AnswerID == "" {
		t.Fatalf("question = %+v, want it recorded as answered by the CFO and still on its way", onItsWay)
	}
	if actions := store.Snapshot().Actions; len(actions) != 1 || actions[0].Status != "running" || actions[0].Awaiting == nil {
		t.Fatalf("actions = %+v, want the queued answer watched as one on its way", actions)
	}
	if _, err := store.Queue(Action{ID: "board-answer", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "Postgres"}); err == nil {
		t.Fatal("the board queued a second answer to a question the CFO had answered")
	}
	if typed := goblin.lines(t); len(typed) != 1 {
		t.Fatalf("the goblin received %q, want only the CFO's answer", typed)
	}
	// Once the goblin's own record of the conversation shows it took the
	// answer, the question closes as the CFO's.
	if err := store.settleDeliveries(time.Now().UTC(), took); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Questions[0]; got.Status != "succeeded" || got.AnsweredBy != "cfo" || got.AnsweredOption != "SQLite" {
		t.Fatalf("question = %+v, want it closed as answered by the CFO once the goblin took it", got)
	}
}

// A queued CFO answer is a delivery on its way, not a delivered one: the
// question stays open until the goblin's own record of the conversation shows
// it took the answer, so the decision does not vanish from supervision while
// the goblin is stuck in a long tool call with no next tool call. Issue 1453.
func TestAQueuedCFOAnswerStaysOnItsWayUntilTheGoblinTakesIt(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	primaryFixture(t, store)
	meta, record, goblin, connection := goblinFixture(t, store)
	servePipe(t, store, connection)
	q := surfaced(t, store, meta, record, connection)
	goblin.startTurn(t)
	since := time.Now().UTC()

	// Act
	chosen, queued, err := connection.AnswerGoblin(context.Background(), q.ID, "SQLite", "")

	// Assert
	if err != nil || chosen != "SQLite" || !queued {
		t.Fatalf("answer = %q, queued %v, %v; want SQLite queued for the goblin's next tool call", chosen, queued, err)
	}
	if typed := goblin.lines(t); len(typed) != 1 {
		t.Fatalf("the goblin received %q, want the answer submitted once", typed)
	}
	if pending, err := wake.Pending(store.Home.State); err != nil || len(pending) != 1 || pending[0].Answered != "SQLite" || pending[0].AnsweredBy != wake.AnsweredByCFO {
		t.Fatalf("notify = %+v (%v), want it marked answered by the CFO", pending, err)
	}
	if onItsWay := store.Snapshot().Questions[0]; onItsWay.Status != "running" || onItsWay.AnsweredBy != "cfo" || onItsWay.AnsweredOption != "SQLite" || onItsWay.AnswerID == "" {
		t.Fatalf("question = %+v, want it answered by the CFO and still on its way", onItsWay)
	}
	// A long tool call keeps it on its way: the quiet limit counts only from
	// the last look that showed the turn.
	for elapsed := time.Minute; elapsed <= time.Hour; elapsed += time.Minute {
		if err := store.settleDeliveries(since.Add(elapsed), working); err != nil {
			t.Fatal(err)
		}
	}
	if behind, _ := outcome(store); behind.Status != "running" {
		t.Fatalf("behind an hour-long turn: question %s, want it still on its way", behind.Status)
	}
	if err := store.settleDeliveries(since.Add(time.Hour+time.Second), took); err != nil {
		t.Fatal(err)
	}
	delivered := store.Snapshot().Questions[0]
	if delivered.Status != "succeeded" || delivered.AnsweredBy != "cfo" || delivered.AnsweredOption != "SQLite" {
		t.Fatalf("once taken: question = %+v, want it closed as answered by the CFO", delivered)
	}
	if _, err := store.Queue(Action{ID: "board-answer", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "Postgres"}); err == nil {
		t.Fatal("the board queued a second answer to a question the CFO had answered")
	}
	if typed := goblin.lines(t); len(typed) != 1 {
		t.Fatalf("the goblin received %q, want only the CFO's answer", typed)
	}
}

// A queued CFO answer whose goblin never takes it is reported stuck, in words
// that say what to do, and a take that comes later still delivers it once.
func TestAQueuedCFOAnswerThatNeverArrivesWarnsAndALateTakeStillDeliversIt(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	primaryFixture(t, store)
	meta, record, goblin, connection := goblinFixture(t, store)
	servePipe(t, store, connection)
	q := surfaced(t, store, meta, record, connection)
	goblin.startTurn(t)
	since := time.Now().UTC()
	if _, _, err := connection.AnswerGoblin(context.Background(), q.ID, "SQLite", ""); err != nil {
		t.Fatal(err)
	}

	// Act
	early := store.settleDeliveries(since.Add(deliveryQuiet-time.Second), idle)
	patient := store.Snapshot().Questions[0]
	late := store.settleDeliveries(since.Add(deliveryQuiet+time.Second), idle)
	warned, question := outcome(store)
	after := store.settleDeliveries(since.Add(deliveryQuiet+2*time.Minute), took)
	delivered, answered := outcome(store)

	// Assert
	if early != nil || late != nil || after != nil {
		t.Fatal(early, late, after)
	}
	if patient.Status != "running" {
		t.Errorf("just inside the quiet limit: question %s, want it still on its way", patient.Status)
	}
	if warned.Status != "uncertain" || question.Status != "uncertain" || !strings.Contains(warned.Advice, "press Enter") {
		t.Errorf("past the quiet limit: action %s %q, question %s; want a warning that says what to do", warned.Status, warned.Message, question.Status)
	}
	if delivered.Status != "succeeded" || delivered.Advice != "" || answered.Status != "succeeded" || answered.AnsweredBy != "cfo" {
		t.Errorf("after a late take: action %s advice %q, question %+v; want both delivered", delivered.Status, delivered.Advice, answered)
	}
}

// A queued CFO answer is accountable only through the delivery record that
// watches it. When no room can be made for that record without evicting work
// still being watched, the answer must not close its question as answered: it
// is kept, an ack cannot close it, and a restart records and watches it once
// room frees. Issue 1453.
func TestAQueuedCFOAnswerWithNoRoomForItsDeliveryIsKeptAndTrackedLater(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	primaryFixture(t, store)
	meta, record, goblin, connection := goblinFixture(t, store)
	servePipe(t, store, connection)
	q := surfaced(t, store, meta, record, connection)
	goblin.startTurn(t)
	now := time.Now().UTC()
	store.mu.Lock()
	for i := 0; i < maxActions; i++ {
		store.db.Actions = append(store.db.Actions, Action{ID: fmt.Sprintf("busy-%d", i), Kind: "message", TaskID: meta.ID, Status: "running", CreatedAt: now, UpdatedAt: now})
	}
	store.mu.Unlock()

	// Act
	chosen, queued, err := connection.AnswerGoblin(context.Background(), q.ID, "SQLite", "")

	// Assert
	if err != nil || chosen != "SQLite" || !queued {
		t.Fatalf("answer = %q, queued %v, %v; want SQLite sent once", chosen, queued, err)
	}
	if typed := goblin.lines(t); len(typed) != 1 {
		t.Fatalf("the goblin received %q, want the answer sent once", typed)
	}
	if pending, err := wake.Pending(h.State); err != nil || len(pending) != 1 || pending[0].Answered != "SQLite" || pending[0].AnsweredBy != wake.AnsweredByCFO {
		t.Fatalf("notify = %+v (%v), want it marked answered by the CFO", pending, err)
	}
	if kept := store.Snapshot().Questions[0]; kept.Status != "pending" || kept.AnsweredBy != "" || kept.AnswerID != "" {
		t.Fatalf("question = %+v, want it unchanged until its delivery record can exist", kept)
	}
	// An ack cannot close it as answered while its delivery has no record.
	if err := wake.AckThrough(h.State, record.Seq); err != nil {
		t.Fatal(err)
	}
	if err := store.supersedeQuestions(); err != nil {
		t.Fatal(err)
	}
	if acked := store.Snapshot().Questions[0]; acked.Status != "pending" {
		t.Fatalf("after the ack: question %s, want it still waiting for its delivery record", acked.Status)
	}
	// A restart keeps the delivery, and the room it frees lets the record be
	// made and watched.
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.ingestAnswers(); err != nil {
		t.Fatal(err)
	}
	onItsWay := reopened.Snapshot().Questions[0]
	if onItsWay.Status != "running" || onItsWay.AnsweredBy != "cfo" || onItsWay.AnswerID == "" {
		t.Fatalf("after the restart: question = %+v, want it on its way with a delivery record", onItsWay)
	}
	watched := false
	for _, a := range reopened.Snapshot().Actions {
		if a.ID == onItsWay.AnswerID {
			watched = a.Status == "running" && a.Awaiting != nil
		}
	}
	if !watched {
		t.Fatalf("actions = %+v, want %s watched while it is on its way", reopened.Snapshot().Actions, onItsWay.AnswerID)
	}
	if typed := goblin.lines(t); len(typed) != 1 {
		t.Fatalf("the goblin received %q, want never a second send", typed)
	}
}

// The delivery is tracked against the recipient submission verified, never a
// record read after the text was typed: a record that disappears or is
// replaced while the text is in flight must neither leave the question
// answered with no delivery, nor bind the text to a generation that never
// took it. Issue 1453.
func TestAQueuedCFOAnswerBindsToTheRecipientItWasSentTo(t *testing.T) {
	for _, c := range []struct {
		name   string
		during func(t *testing.T, stateDir string, meta state.TaskMeta)
	}{
		{name: "its record disappears while the text is in flight", during: func(t *testing.T, stateDir string, meta state.TaskMeta) {
			if err := os.Remove(state.TaskMetaPath(stateDir, meta.ID)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "its record is replaced by another generation while the text is in flight", during: func(t *testing.T, stateDir string, meta state.TaskMeta) {
			replacement := meta
			replacement.SpawnGen = "s1792799999999999999"
			if err := state.WriteTaskMeta(stateDir, replacement); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			primaryFixture(t, store)
			meta, record, _, connection := goblinFixture(t, store)
			servePipe(t, store, connection)
			q := surfaced(t, store, meta, record, connection)
			delivery := typeIntoGoblin
			t.Cleanup(func() { typeIntoGoblin = delivery })
			sends := 0
			typeIntoGoblin = func(_ context.Context, directory string, task state.TaskMeta, _ string) error {
				sends++
				c.during(t, directory, task)
				return fleet.ErrQueuedForToolCall
			}

			// Act
			chosen, queued, err := connection.AnswerGoblin(context.Background(), q.ID, "SQLite", "")

			// Assert
			if err != nil || chosen != "SQLite" || !queued || sends != 1 {
				t.Fatalf("answer = %q, queued %v, sends %d, %v; want one queued send", chosen, queued, sends, err)
			}
			onItsWay := store.Snapshot().Questions[0]
			if onItsWay.Status != "running" || onItsWay.AnsweredBy != "cfo" || onItsWay.AnswerID == "" {
				t.Fatalf("question = %+v, want it on its way against the recipient it was sent to", onItsWay)
			}
			actions := store.Snapshot().Actions
			if len(actions) != 1 || actions[0].Awaiting == nil || actions[0].Awaiting.Generation != meta.SpawnGen {
				t.Fatalf("actions = %+v, want the delivery bound to the generation %s it was sent to", actions, meta.SpawnGen)
			}
			// Its terminal is gone, so the delivery reads as never arrived,
			// never as answered.
			service := &Service{Store: store}
			if err := store.settleDeliveries(time.Now().UTC().Add(time.Second), service.lookAtTerminal); err != nil {
				t.Fatal(err)
			}
			if settled := store.Snapshot().Questions[0]; settled.Status != "uncertain" {
				t.Fatalf("after settling: question %s, want it uncertain, never answered without a delivery", settled.Status)
			}
			// An ack and a restart send nothing again.
			if err := wake.AckThrough(h.State, record.Seq); err != nil {
				t.Fatal(err)
			}
			if err := store.supersedeQuestions(); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(h); err != nil {
				t.Fatal(err)
			}
			if sends != 1 {
				t.Fatalf("sends = %d, want never a second send", sends)
			}
		})
	}
}

// No valid recipient evidence means nothing is typed: the answer is refused
// before any submission, so no report of it can close as answered while its
// text is unsent and unrecorded, and the wake stays unanswered for a real try.
// Issue 1453.
func TestACFOAnswerWithNoValidRecipientEvidenceSendsNothing(t *testing.T) {
	for _, c := range []struct {
		name string
		read func(meta state.TaskMeta) (state.TaskMeta, error)
	}{
		{name: "its record cannot be read", read: func(state.TaskMeta) (state.TaskMeta, error) {
			return state.TaskMeta{}, errors.New("the task record is unreadable")
		}},
		{name: "its record is another generation", read: func(meta state.TaskMeta) (state.TaskMeta, error) {
			meta.SpawnGen = "s1792799999999999999"
			return meta, nil
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			primaryFixture(t, store)
			meta, record, _, connection := goblinFixture(t, store)
			servePipe(t, store, connection)
			q := surfaced(t, store, meta, record, connection)
			reader := readAnswerRecipient
			t.Cleanup(func() { readAnswerRecipient = reader })
			readAnswerRecipient = func(string, string) (state.TaskMeta, error) { return c.read(meta) }
			sends := 0
			delivery := typeIntoGoblin
			t.Cleanup(func() { typeIntoGoblin = delivery })
			typeIntoGoblin = func(context.Context, string, state.TaskMeta, string) error {
				sends++
				return fleet.ErrQueuedForToolCall
			}

			// Act
			_, _, err := connection.AnswerGoblin(context.Background(), q.ID, "SQLite", "")

			// Assert
			if err == nil {
				t.Fatal("the answer was taken with no valid recipient evidence")
			}
			if sends != 0 {
				t.Fatalf("sends = %d, want nothing typed", sends)
			}
			if pending, err := wake.Pending(h.State); err != nil || len(pending) != 1 || pending[0].Answered != "" {
				t.Fatalf("notify = %+v (%v), want it still unanswered for a real try", pending, err)
			}
			if kept := store.Snapshot().Questions[0]; kept.Status != "pending" || kept.AnsweredBy != "" || kept.AnswerID != "" {
				t.Fatalf("question = %+v, want it untouched and unanswered", kept)
			}
			if actions := store.Snapshot().Actions; len(actions) != 0 {
				t.Fatalf("actions = %+v, want no delivery record", actions)
			}
		})
	}
}

// Acknowledgement preserves delivery accountability: acking the notify and
// restarting the supervisor leaves the queued answer on its way, refuses a
// second decision, and never replays the text.
func TestAQueuedCFOAnswerSurvivesAcknowledgementAndRestart(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	primaryFixture(t, store)
	meta, record, goblin, connection := goblinFixture(t, store)
	servePipe(t, store, connection)
	q := surfaced(t, store, meta, record, connection)
	goblin.startTurn(t)
	since := time.Now().UTC()
	if _, _, err := connection.AnswerGoblin(context.Background(), q.ID, "SQLite", ""); err != nil {
		t.Fatal(err)
	}

	// Act: the CFO acks the notify, as cfo drain does, and the supervisor
	// restarts.
	if err := wake.AckThrough(h.State, record.Seq); err != nil {
		t.Fatal(err)
	}
	if err := store.supersedeQuestions(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	kept, question := outcome(reopened)
	if kept.Status != "running" || kept.Awaiting == nil || question.Status != "running" || question.AnsweredBy != "cfo" {
		t.Fatalf("after the ack and restart: action %s %q, question %+v; want it still on its way", kept.Status, kept.Message, question)
	}
	if _, err := reopened.Queue(Action{ID: "board-answer", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "Postgres"}); err == nil {
		t.Fatal("the board queued a second answer after the restart")
	}
	if typed := goblin.lines(t); len(typed) != 1 {
		t.Fatalf("the goblin received %q, want only the CFO's answer, never a replay", typed)
	}
	if err := reopened.settleDeliveries(since.Add(time.Minute), took); err != nil {
		t.Fatal(err)
	}
	if delivered := reopened.Snapshot().Questions[0]; delivered.Status != "succeeded" || delivered.AnsweredBy != "cfo" {
		t.Fatalf("once taken: question = %+v, want it delivered with the CFO's attribution", delivered)
	}
}

// The eight questions were already answered, and their notifies acked, so
// the registered CFO closes them on the board without the goblin receiving
// the decision a second time.
func TestCFORecordsAnAnswerGivenOutOfBandWithoutSendingIt(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	primaryFixture(t, store)
	meta, record, goblin, connection := goblinFixture(t, store)
	servePipe(t, store, connection)
	q := surfaced(t, store, meta, record, connection)
	if err := wake.AckThrough(h.State, record.Seq); err != nil {
		t.Fatal(err)
	}
	if err := store.supersedeQuestions(); err != nil {
		t.Fatal(err)
	}

	// Act
	chosen, err := connection.RecordAnswer(q.ID, "sqlite", "decided out of band", "")

	// Assert
	if err != nil || chosen != "SQLite" {
		t.Fatalf("record = %q, %v; want SQLite recorded", chosen, err)
	}
	if typed := goblin.lines(t); len(typed) != 0 {
		t.Fatalf("the goblin received %q, want nothing sent", typed)
	}
	if err := os.MkdirAll(nativehook.SpoolDir(h.State), 0700); err != nil {
		t.Fatal(err)
	}
	(&Service{Store: store, work: make(chan struct{}, 1)}).cycle(context.Background(), false)
	if got := store.Snapshot().Questions[0]; got.Status != "succeeded" || got.AnsweredBy != "cfo" || got.AnsweredOption != "SQLite" || got.Answer != "SQLite. decided out of band" {
		t.Fatalf("question = %+v, want it closed as answered by the CFO with SQLite", got)
	}
	if _, err := store.Queue(Action{ID: "board-answer", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "Postgres"}); err == nil {
		t.Fatal("the board queued an answer to a question the CFO had recorded")
	}
}

// A record-only close never stands in for an answer: a notify still waiting
// unanswered is answered with cfo answer, which delivers it; a question the
// Overlord is answering, or already answered, keeps its answer; only the
// registered CFO may record one; and it names the question by its ID, never
// by a bare wake sequence.
func TestCFORecordOnlyRefusesAQuestionItMustNotClose(t *testing.T) {
	for _, c := range []struct {
		name     string
		register bool
		bySeq    bool
		before   func(t *testing.T, store *Store, record wake.Record, q Question)
		refusal  string
	}{
		{name: "a bare wake sequence", register: true, bySeq: true, refusal: "takes the question's ID", before: func(t *testing.T, store *Store, record wake.Record, _ Question) {
			if err := wake.AckThrough(store.Home.State, record.Seq); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a notify still waiting unanswered", register: true, refusal: "still waiting unanswered"},
		{name: "a question the Overlord is answering", register: true, refusal: "the Overlord is answering", before: func(t *testing.T, store *Store, record wake.Record, q Question) {
			if _, err := store.Queue(Action{ID: "board-answer", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "Postgres"}); err != nil {
				t.Fatal(err)
			}
			if err := wake.AckThrough(store.Home.State, record.Seq); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a caller that is not the registered CFO", refusal: "not registered", before: func(t *testing.T, store *Store, record wake.Record, _ Question) {
			if err := wake.AckThrough(store.Home.State, record.Seq); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			if c.register {
				primaryFixture(t, store)
			}
			meta, record, goblin, connection := goblinFixture(t, store)
			q := surfaced(t, store, meta, record, connection)
			if c.before != nil {
				c.before(t, store, record, q)
			}

			ref := q.ID
			if c.bySeq {
				ref = fmt.Sprint(record.Seq)
			}

			// Act
			_, err := connection.RecordAnswer(ref, "SQLite", "", "")

			// Assert
			if typed := goblin.lines(t); err == nil || !strings.Contains(err.Error(), c.refusal) || len(typed) != 0 {
				t.Fatalf("record = %v with the goblin receiving %q, want refused (%q) with nothing sent", err, typed, c.refusal)
			}
			if got := store.Snapshot().Questions[0]; got.AnsweredBy == "cfo" {
				t.Fatalf("question = %+v, want no CFO answer recorded", got)
			}
		})
	}
}

// On 2026-09-29 the Overlord answered the CFO's question herdr-strays-20260929
// in chat, and the CFO had no way to close its card: cfo question takes no
// answer, and cfo answer --record-only took only a goblin's question. The CFO
// now records an answer the Overlord gave elsewhere, naming where, for its own
// question or a goblin's: the card closes as his answer there, recorded by the
// CFO, and nothing is sent to anyone.
func TestCFORecordsAnAnswerTheOverlordGaveElsewhere(t *testing.T) {
	for _, c := range []struct {
		name string
		ask  func(t *testing.T, store *Store, connection *CFOConnection) (Question, *hostedTerminal)
		pick string
		want string
	}{
		{name: "the CFO's own question", pick: "stop", want: "Stop them", ask: func(t *testing.T, store *Store, connection *CFOConnection) (Question, *hostedTerminal) {
			if err := connection.PublishQuestion("herdr-strays-20260929", "May I stop the 4 stray Herdr panes left from yesterday?", []string{"Stop them", "Leave them"}, "Stop them", "", nil); err != nil {
				t.Fatal(err)
			}
			if err := store.ingestQuestions(); err != nil {
				t.Fatal(err)
			}
			return store.Snapshot().Questions[0], nil
		}},
		{name: "a goblin's question the CFO relayed", pick: "sqlite", want: "SQLite", ask: func(t *testing.T, store *Store, _ *CFOConnection) (Question, *hostedTerminal) {
			meta, record, goblin, connection := goblinFixture(t, store)
			q := surfaced(t, store, meta, record, connection)
			if err := wake.AckThrough(store.Home.State, record.Seq); err != nil {
				t.Fatal(err)
			}
			return q, &goblin
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			_, _, cfo, connection := primaryFixture(t, store)
			servePipe(t, store, connection)
			q, goblin := c.ask(t, store, connection)

			// Act
			chosen, err := connection.RecordAnswer(q.ID, c.pick, "", "chat")

			// Assert
			if err != nil || chosen != c.want {
				t.Fatalf("record = %q, %v; want %s recorded", chosen, err, c.want)
			}
			if err := os.MkdirAll(nativehook.SpoolDir(h.State), 0700); err != nil {
				t.Fatal(err)
			}
			(&Service{Store: store, work: make(chan struct{}, 1)}).cycle(context.Background(), false)
			got := store.Snapshot().Questions[0]
			if got.Status != "succeeded" || got.AnsweredBy != "overlord" || got.AnsweredIn != "chat" || got.AnsweredOption != c.want {
				t.Fatalf("question = %+v, want it closed as the Overlord's answer in chat", got)
			}
			typed := cfo.lines(t)
			if goblin != nil {
				typed = append(typed, goblin.lines(t)...)
			}
			if len(typed) != 0 {
				t.Fatalf("the CFO and the goblin received %q, want nothing sent", typed)
			}
		})
	}
}

// Only the Overlord answers the CFO's own question, so recording its answer
// names where he gave it, in a few plain words.
func TestCFORecordOfItsOwnQuestionNamesWhereTheOverlordAnswered(t *testing.T) {
	for _, c := range []struct {
		name, in, refusal string
	}{
		{name: "no place", in: "", refusal: "--in"},
		{name: "a place that is not plain words", in: "chat\nAnswer: Leave them", refusal: "--in"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			_, _, _, connection := primaryFixture(t, store)
			servePipe(t, store, connection)
			if err := connection.PublishQuestion("herdr-strays-20260929", "May I stop the stray panes?", []string{"Stop them", "Leave them"}, "", "", nil); err != nil {
				t.Fatal(err)
			}
			if err := store.ingestQuestions(); err != nil {
				t.Fatal(err)
			}

			// Act
			_, err := connection.RecordAnswer("herdr-strays-20260929", "Stop them", "", c.in)

			// Assert
			if err == nil || !strings.Contains(err.Error(), c.refusal) {
				t.Fatalf("record = %v, want it refused (%q)", err, c.refusal)
			}
			if got := store.Snapshot().Questions[0]; got.Status != "pending" {
				t.Fatalf("question = %+v, want it still pending", got)
			}
		})
	}
}

// The Overlord dismisses a question he answered elsewhere or no longer needs
// from the Command Center himself: it closes as dismissed, and the CFO is told,
// since the CFO asked it or holds the goblin's notify that did.
func TestTheOverlordDismissesAQuestionHeNoLongerNeeds(t *testing.T) {
	for _, c := range []struct {
		name  string
		ask   func(t *testing.T, store *Store, connection *CFOConnection) Question
		names string
	}{
		{name: "the CFO's own question", names: "your question herdr-strays-20260929", ask: func(t *testing.T, store *Store, connection *CFOConnection) Question {
			if err := connection.PublishQuestion("herdr-strays-20260929", "May I stop the stray panes?", []string{"Stop them", "Leave them"}, "", "", nil); err != nil {
				t.Fatal(err)
			}
			if err := store.ingestQuestions(); err != nil {
				t.Fatal(err)
			}
			return store.Snapshot().Questions[0]
		}},
		{name: "a goblin's question", names: "task-1's question", ask: func(t *testing.T, store *Store, _ *CFOConnection) Question {
			meta, record, _, connection := goblinFixture(t, store)
			return surfaced(t, store, meta, record, connection)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			_, _, cfo, connection := primaryFixture(t, store)
			servePipe(t, store, connection)
			q := c.ask(t, store, connection)
			s := &Service{Store: store, Options: Options{CFO: connection}}

			// Act
			_, queued := store.Queue(Action{ID: "dismiss-1", Kind: "question_clear", QuestionID: q.ID, Generation: q.Identity})
			if queued == nil {
				queued = store.ProcessOne(context.Background(), s.execute)
			}

			// Assert
			if queued != nil {
				t.Fatalf("dismiss = %v, want it taken", queued)
			}
			if got := store.Snapshot().Questions[0]; got.Status != "cleared" || got.Message != "You dismissed it: answered elsewhere or no longer needed." {
				t.Fatalf("question = %+v, want it dismissed", got)
			}
			if typed := cfo.waitForLines(t, 1); len(typed) != 1 || !strings.Contains(typed[0], c.names) || !strings.Contains(typed[0], "dismissed") {
				t.Fatalf("the CFO got %q, want one message naming %s as dismissed", typed, c.names)
			}
		})
	}
}
