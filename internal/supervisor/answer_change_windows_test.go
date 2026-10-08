package supervisor

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// cfoAnswered is the goblin fixture's question as the CFO answered it with
// cfo answer, SQLite, before the goblin reported anything since.
func cfoAnswered(t *testing.T) (*Store, state.TaskMeta, hostedTerminal, *CFOConnection, Question) {
	t.Helper()
	store, _ := testStore(t)
	primaryFixture(t, store)
	meta, record, goblin, connection := goblinFixture(t, store)
	servePipe(t, store, connection)
	q := surfaced(t, store, meta, record, connection)
	if _, _, err := connection.AnswerGoblin(context.Background(), q.ID, "SQLite", "keep it local"); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Questions[0]; got.AnsweredBy != "cfo" || got.AnsweredOption != "SQLite" {
		t.Fatalf("question = %+v, want it answered by the CFO", got)
	}
	return store, meta, goblin, connection, store.Snapshot().Questions[0]
}

// reportLater writes a report into the goblin's status log stamped after the
// CFO's answer, as the goblin's next cfo notify would.
func reportLater(t *testing.T, store *Store, meta state.TaskMeta) {
	t.Helper()
	file, err := os.OpenFile(state.StatusPath(store.Home.State, meta.ID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(time.Now().Add(2*time.Second).UTC().Format(time.RFC3339) + " working: started on SQLite\n"); err != nil {
		t.Fatal(err)
	}
}

// The Overlord's word outranks the CFO's: on a goblin's question the CFO
// answered, while the goblin has reported nothing since, he changes the
// answer from the Command Center. The goblin is told the new answer replaces
// the CFO's, whether it takes it now or at its next tool call, and the
// question reads as his answer, keeping the CFO's it replaced.
func TestTheOverlordChangesAnAnswerTheCFOGave(t *testing.T) {
	for name, isBusy := range map[string]bool{"an idle goblin": false, "a goblin in a turn": true} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, _, goblin, connection, q := cfoAnswered(t)
			if isBusy {
				goblin.startTurn(t)
			}
			s := &Service{Store: store, Options: Options{CFO: connection}}

			// Act
			_, err := store.Queue(Action{ID: "change-1", Kind: "answer_change", Generation: q.Identity, QuestionID: q.ID, Text: "Postgres", AnswerKind: "option"})
			if err == nil {
				err = store.ProcessOne(context.Background(), s.execute)
			}

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			typed := goblin.waitForLines(t, 2)
			if len(typed) != 2 || !strings.Contains(typed[1], "Postgres") || !strings.Contains(typed[1], "replaces the CFO's answer") {
				t.Fatalf("the goblin received %q, want the CFO's answer and then the Overlord's replacing it", typed)
			}
			got := store.Snapshot().Questions[0]
			if got.AnsweredBy != "overlord" || got.AnsweredOption != "Postgres" || got.Answer != "Postgres" || got.ReplacedAnswer != "SQLite" || got.AnsweredAway || got.AnsweredAt == nil {
				t.Fatalf("question = %+v, want it his answer, Postgres, replacing the CFO's SQLite", got)
			}
		})
	}
}

// Only an answer the CFO gave, to a goblin that is still the one that asked
// and has reported nothing since, can change, and only to another of its
// choices or a written answer. Anything else is refused before the goblin
// hears a word.
func TestAnAnswerChangeIsRefusedBeforeTheGoblinHearsAnything(t *testing.T) {
	for name, test := range map[string]struct {
		before  func(t *testing.T, store *Store, meta state.TaskMeta, q Question)
		text    string
		refusal string
	}{
		"the CFO's own choice":                 {text: "SQLite", refusal: "already the CFO's answer"},
		"a choice the question does not offer": {text: "Redis", refusal: "one of its choices"},
		"a goblin that reported since": {text: "Postgres", refusal: "reported since", before: func(t *testing.T, store *Store, meta state.TaskMeta, _ Question) {
			reportLater(t, store, meta)
		}},
		"a goblin that restarted": {text: "Postgres", refusal: "restarted or ended", before: func(t *testing.T, store *Store, meta state.TaskMeta, _ Question) {
			meta.SpawnGen = "g2"
			if err := state.WriteTaskMeta(store.Home.State, meta); err != nil {
				t.Fatal(err)
			}
		}},
		"a change already on its way": {text: "MySQL", refusal: "already on its way", before: func(t *testing.T, store *Store, _ state.TaskMeta, q Question) {
			if _, err := store.Queue(Action{ID: "change-0", Kind: "answer_change", Generation: q.Identity, QuestionID: q.ID, Text: "Postgres", AnswerKind: "option"}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, meta, goblin, _, q := cfoAnswered(t)
			if test.before != nil {
				test.before(t, store, meta, q)
			}

			// Act
			_, err := store.Queue(Action{ID: "change-1", Kind: "answer_change", Generation: q.Identity, QuestionID: q.ID, Text: test.text, AnswerKind: "option"})

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.refusal) {
				t.Fatalf("queue = %v, want it refused (%q)", err, test.refusal)
			}
			if typed := goblin.lines(t); len(typed) != 1 {
				t.Fatalf("the goblin received %q, want only the CFO's answer", typed)
			}
			if got := store.Snapshot().Questions[0]; got.AnsweredBy != "cfo" || got.AnsweredOption != "SQLite" {
				t.Fatalf("question = %+v, want the CFO's answer to stand", got)
			}
		})
	}
}

// A question still waiting on him, or one he answered himself, has no CFO
// answer to change.
func TestOnlyAnAnswerTheCFOGaveCanChange(t *testing.T) {
	for name, isAnsweredByHim := range map[string]bool{"a question still waiting on him": false, "a question he answered": true} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			primaryFixture(t, store)
			meta, record, _, connection := goblinFixture(t, store)
			servePipe(t, store, connection)
			q := surfaced(t, store, meta, record, connection)
			if isAnsweredByHim {
				s := &Service{Store: store, Options: Options{CFO: connection}}
				if _, err := store.Queue(Action{ID: "board-answer", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "SQLite"}); err != nil {
					t.Fatal(err)
				}
				if err := store.ProcessOne(context.Background(), s.execute); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			_, err := store.Queue(Action{ID: "change-1", Kind: "answer_change", Generation: q.Identity, QuestionID: q.ID, Text: "Postgres", AnswerKind: "option"})

			// Assert
			if err == nil || !strings.Contains(err.Error(), "only an answer the CFO gave") {
				t.Fatalf("queue = %v, want it refused as no CFO answer to change", err)
			}
		})
	}
}

// The goblin can report between his click and the delivery. The supervisor
// checks again under the question's answer lock, so the change fails with
// why, the goblin hears nothing more, and the CFO's answer stands.
func TestAnAnswerChangeFailsWhenTheGoblinReportsBeforeItIsDelivered(t *testing.T) {
	// Arrange
	store, meta, goblin, connection, q := cfoAnswered(t)
	s := &Service{Store: store, Options: Options{CFO: connection}}
	if _, err := store.Queue(Action{ID: "change-1", Kind: "answer_change", Generation: q.Identity, QuestionID: q.ID, Text: "Postgres", AnswerKind: "option"}); err != nil {
		t.Fatal(err)
	}
	reportLater(t, store, meta)

	// Act
	err := store.ProcessOne(context.Background(), s.execute)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	action := store.Snapshot().Actions[len(store.Snapshot().Actions)-1]
	if action.ID != "change-1" || action.Status != "failed" || !strings.Contains(action.Message, "reported since") {
		t.Fatalf("change = %+v, want it failed because the goblin reported since", action)
	}
	if typed := goblin.lines(t); len(typed) != 1 {
		t.Fatalf("the goblin received %q, want only the CFO's answer", typed)
	}
	if got := store.Snapshot().Questions[0]; got.AnsweredBy != "cfo" || got.AnsweredOption != "SQLite" || got.ReplacedAnswer != "" {
		t.Fatalf("question = %+v, want the CFO's answer to stand", got)
	}
	if pending, err := wake.Pending(store.Home.State); err != nil || len(pending) != 1 || pending[0].AnsweredBy != wake.AnsweredByCFO {
		t.Fatalf("notify = %+v (%v), want it still marked answered by the CFO", pending, err)
	}
}
