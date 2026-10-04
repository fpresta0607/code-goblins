package supervisor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/nativehook"
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
		t.Fatalf("answer = %q, queued %v, %v; want SQLite recorded as queued behind the goblin's turn", chosen, queued, err)
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
	if got := store.Snapshot().Questions[0]; got.Status != "succeeded" || got.AnsweredBy != "cfo" || got.AnsweredOption != "SQLite" {
		t.Fatalf("question = %+v, want it closed as answered by the CFO", got)
	}
	if _, err := store.Queue(Action{ID: "board-answer", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "Postgres"}); err == nil {
		t.Fatal("the board queued a second answer to a question the CFO had answered")
	}
	if typed := goblin.lines(t); len(typed) != 1 {
		t.Fatalf("the goblin received %q, want only the CFO's answer", typed)
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
			if err := connection.PublishQuestion("herdr-strays-20260929", "May I stop the 4 stray Herdr panes left from yesterday?", []string{"Stop them", "Leave them"}, "Stop them"); err != nil {
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
			if err := connection.PublishQuestion("herdr-strays-20260929", "May I stop the stray panes?", []string{"Stop them", "Leave them"}, ""); err != nil {
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
			if err := connection.PublishQuestion("herdr-strays-20260929", "May I stop the stray panes?", []string{"Stop them", "Leave them"}, ""); err != nil {
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
