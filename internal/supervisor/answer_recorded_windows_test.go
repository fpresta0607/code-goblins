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
	meta, record, runner, connection := goblinFixture(t, store)
	servePipe(t, store, connection)
	q := surfaced(t, store, meta, record, connection)
	runner.busy = true

	// Act
	chosen, queued, err := connection.AnswerGoblin(context.Background(), q.ID, "SQLite", "")

	// Assert
	if err != nil || chosen != "SQLite" || !queued {
		t.Fatalf("answer = %q, queued %v, %v; want SQLite recorded as queued behind the goblin's turn", chosen, queued, err)
	}
	if len(runner.prompts) != 1 {
		t.Fatalf("goblin prompts = %q, want the answer submitted once", runner.prompts)
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
	if len(runner.prompts) != 1 {
		t.Fatalf("goblin prompts = %q, want only the CFO's answer", runner.prompts)
	}
}

// The eight questions were already answered, and their notifies acked, so
// the registered CFO closes them on the board without the goblin receiving
// the decision a second time.
func TestCFORecordsAnAnswerGivenOutOfBandWithoutSendingIt(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	primaryFixture(t, store)
	meta, record, runner, connection := goblinFixture(t, store)
	servePipe(t, store, connection)
	q := surfaced(t, store, meta, record, connection)
	if err := wake.AckThrough(h.State, record.Seq); err != nil {
		t.Fatal(err)
	}
	if err := store.supersedeQuestions(); err != nil {
		t.Fatal(err)
	}

	// Act
	chosen, err := connection.RecordGoblinAnswer(context.Background(), q.ID, "sqlite", "decided out of band")

	// Assert
	if err != nil || chosen != "SQLite" {
		t.Fatalf("record = %q, %v; want SQLite recorded", chosen, err)
	}
	if len(runner.prompts) != 0 {
		t.Fatalf("goblin prompts = %q, want nothing sent", runner.prompts)
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
			meta, record, runner, connection := goblinFixture(t, store)
			q := surfaced(t, store, meta, record, connection)
			if c.before != nil {
				c.before(t, store, record, q)
			}

			ref := q.ID
			if c.bySeq {
				ref = fmt.Sprint(record.Seq)
			}

			// Act
			_, err := connection.RecordGoblinAnswer(context.Background(), ref, "SQLite", "")

			// Assert
			if err == nil || !strings.Contains(err.Error(), c.refusal) || len(runner.prompts) != 0 {
				t.Fatalf("record = %v with %d prompts, want refused (%q) with nothing sent", err, len(runner.prompts), c.refusal)
			}
			if got := store.Snapshot().Questions[0]; got.AnsweredBy == "cfo" {
				t.Fatalf("question = %+v, want no CFO answer recorded", got)
			}
		})
	}
}
