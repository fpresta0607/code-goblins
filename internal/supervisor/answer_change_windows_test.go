package supervisor

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// The Overlord, 2026-10-11 about 01:00Z: "when i go back in command center it
// should show the selcted answer if you ewent ahead where you can alwasy slect
// a enw one and change as needed safely and smoothly indempotentlty". Every
// answered question takes a new answer at any time, whoever gave the first.
// The change reaches whoever acts on the answer once, as a change: what the
// answer was, what it is now, and that he changed it.

// cfoAnswered is the goblin fixture's question as the CFO answered it with
// cfo answer, SQLite, before the goblin reported anything since. cfo is the
// CFO's own terminal.
func cfoAnswered(t *testing.T) (*Store, state.TaskMeta, hostedTerminal, hostedTerminal, *CFOConnection, Question) {
	t.Helper()
	store, _ := testStore(t)
	_, _, cfo, _ := primaryFixture(t, store)
	meta, record, goblin, connection := goblinFixture(t, store)
	servePipe(t, store, connection)
	q := surfaced(t, store, meta, record, connection)
	if _, _, err := connection.AnswerGoblin(context.Background(), q.ID, "SQLite", "keep it local"); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Questions[0]; got.AnsweredBy != "cfo" || got.AnsweredOption != "SQLite" {
		t.Fatalf("question = %+v, want it answered by the CFO", got)
	}
	return store, meta, goblin, cfo, connection, store.Snapshot().Questions[0]
}

// overlordAnswered is the goblin fixture's question as he answered it himself
// on the board, SQLite, delivered.
func overlordAnswered(t *testing.T) (*Store, state.TaskMeta, hostedTerminal, hostedTerminal, *CFOConnection, Question) {
	t.Helper()
	store, _ := testStore(t)
	_, _, cfo, _ := primaryFixture(t, store)
	meta, record, goblin, connection := goblinFixture(t, store)
	servePipe(t, store, connection)
	q := surfaced(t, store, meta, record, connection)
	s := &Service{Store: store, Options: Options{CFO: connection}}
	if _, err := store.Queue(Action{ID: "board-answer", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "SQLite", AnswerKind: "option"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ProcessOne(context.Background(), s.execute); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Questions[0]; got.AnsweredBy != "overlord" || got.AnsweredOption != "SQLite" || got.Status != "succeeded" {
		t.Fatalf("question = %+v, want his answer delivered", got)
	}
	return store, meta, goblin, cfo, connection, store.Snapshot().Questions[0]
}

// reportLater writes a report into the goblin's status log stamped after the
// first answer, as the goblin's next cfo notify would.
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

// change queues his change of q's answer to choice from the board as request
// id.
func change(store *Store, id string, q Question, choice string) (Action, error) {
	return store.Queue(Action{ID: id, Kind: "answer_change", Generation: q.Identity, QuestionID: q.ID, Text: choice, AnswerKind: "option"})
}

// runAll runs every action that can run now.
func runAll(t *testing.T, store *Store, s *Service) {
	t.Helper()
	for range maxActions {
		if !store.HasRunnable() {
			return
		}
		if err := store.ProcessOne(context.Background(), s.execute); err != nil {
			t.Fatal(err)
		}
	}
}

// changeLines is what of lines tells of a change to an answer.
func changeLines(lines []string) []string {
	return slices.DeleteFunc(slices.Clone(lines), func(line string) bool { return !strings.Contains(line, "changed") })
}

// wantTheChange fails unless line tells, as one change, that the Overlord
// changed the answer from was to now, and asks for what the first answer
// started to be undone or redone.
func wantTheChange(t *testing.T, line, was, now string) {
	t.Helper()
	for _, part := range []string{"Overlord changed", "Was: " + was + ".", "Now: " + now + ".", "undo or redo what the first answer started"} {
		if !strings.Contains(line, part) {
			t.Errorf("the change reads %q, want it to say %q", line, part)
		}
	}
}

// wantHisAnswer fails unless q reads as his answer, now, replacing was.
func wantHisAnswer(t *testing.T, q Question, now, was string) {
	t.Helper()
	if q.AnsweredBy != "overlord" || q.AnsweredOption != now || q.Answer != now || q.ReplacedAnswer != was || q.AnsweredAway || q.AnsweredAt == nil || q.Status != "succeeded" {
		t.Errorf("question = %+v, want it his answer, %s, replacing %s", q, now, was)
	}
}

func TestHeChangesAGoblinsAnswerAtAnyTimeWhoeverGaveIt(t *testing.T) {
	for name, test := range map[string]struct {
		answered func(t *testing.T) (*Store, state.TaskMeta, hostedTerminal, hostedTerminal, *CFOConnection, Question)
		was      string
		isBusy   bool
		reported bool
	}{
		"the CFO's answer to an idle goblin":            {answered: cfoAnswered, was: "SQLite"},
		"the CFO's answer to a goblin in a turn":        {answered: cfoAnswered, was: "SQLite", isBusy: true},
		"the CFO's answer to a goblin that acted on it": {answered: cfoAnswered, was: "SQLite", reported: true},
		"his own answer": {answered: overlordAnswered, was: "SQLite"},
		"his own answer to a goblin that acted on it": {answered: overlordAnswered, was: "SQLite", reported: true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, meta, goblin, cfo, connection, q := test.answered(t)
			if test.isBusy {
				goblin.startTurn(t)
			}
			if test.reported {
				reportLater(t, store, meta)
			}
			s := &Service{Store: store, Options: Options{CFO: connection}}

			// Act
			_, err := change(store, "change-1", q, "Postgres")
			if err == nil {
				runAll(t, store, s)
			}

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			typed := goblin.waitForLines(t, 2)
			if changes := changeLines(typed); len(changes) != 1 {
				t.Fatalf("the goblin received %q, want its first answer and then one change", typed)
			}
			wantTheChange(t, typed[1], test.was, "Postgres")
			wantHisAnswer(t, store.Snapshot().Questions[0], "Postgres", test.was)
			if told := changeLines(cfo.lines(t)); len(told) != 0 {
				t.Errorf("the CFO was told %q, want the change for the goblin that acts on it alone", told)
			}
		})
	}
}

// A goblin that restarted or ended cannot act on a change, so the CFO does:
// the change goes to it, naming the goblin, and the goblin's successor hears
// nothing meant for its predecessor.
func TestAChangeForAGoblinThatEndedGoesToTheCFO(t *testing.T) {
	// Arrange
	store, meta, goblin, cfo, connection, q := cfoAnswered(t)
	meta.SpawnGen = "g2"
	if err := state.WriteTaskMeta(store.Home.State, meta); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: store, Options: Options{CFO: connection}}

	// Act
	_, err := change(store, "change-1", q, "Postgres")
	if err == nil {
		runAll(t, store, s)
	}

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	told := changeLines(cfo.waitForLines(t, 1))
	if len(told) != 1 || !strings.Contains(told[0], "task-1") || !strings.Contains(told[0], q.ID) {
		t.Fatalf("the CFO was told %q, want one change naming task-1 and its question", told)
	}
	wantTheChange(t, told[0], "SQLite", "Postgres")
	if typed := goblin.lines(t); len(typed) != 1 {
		t.Errorf("the goblin received %q, want only the CFO's answer", typed)
	}
	wantHisAnswer(t, store.Snapshot().Questions[0], "Postgres", "SQLite")
}

// The CFO's own question, answered by him, takes a new answer too: the CFO
// acts on it, so the CFO is told.
func TestHeChangesHisAnswerToTheCFOsOwnQuestion(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	_, identity, cfo, connection := primaryFixture(t, store)
	if err := store.acceptQuestion(Question{ID: "pp-merge-125-20261010", Identity: identity, Text: "Merge PocketPiggies 125?", Options: []string{"Merge it", "Keep it open"}, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: store, Options: Options{CFO: connection}}
	if _, err := store.Queue(Action{ID: "answer-1", Kind: "cfo_answer", Generation: identity, QuestionID: "pp-merge-125-20261010", Text: "Merge it", AnswerKind: "option"}); err != nil {
		t.Fatal(err)
	}
	runAll(t, store, s)
	q := store.Snapshot().Questions[0]

	// Act
	_, err := change(store, "change-1", q, "Keep it open")
	if err == nil {
		runAll(t, store, s)
	}

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	typed := cfo.waitForLines(t, 2)
	told := changeLines(typed)
	if len(told) != 1 || !strings.Contains(told[0], "pp-merge-125-20261010") {
		t.Fatalf("the CFO received %q, want his answer and then one change to it", typed)
	}
	wantTheChange(t, told[0], "Merge it", "Keep it open")
	wantHisAnswer(t, store.Snapshot().Questions[0], "Keep it open", "Merge it")
}

// Choosing the answer already chosen sends nothing, whether the board shows it
// chosen or a second tab sends what a change still on its way already chose.
func TestChoosingTheAnswerAlreadyChosenSendsNothing(t *testing.T) {
	for name, before := range map[string]func(t *testing.T, store *Store, q Question){
		"the answer as it stands": nil,
		"the answer a change on its way chose": func(t *testing.T, store *Store, q Question) {
			if _, err := change(store, "change-0", q, "Postgres"); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, _, goblin, _, connection, q := cfoAnswered(t)
			chosen := "SQLite"
			if before != nil {
				before(t, store, q)
				chosen = "Postgres"
			}
			s := &Service{Store: store, Options: Options{CFO: connection}}

			// Act
			sent, err := change(store, "change-1", q, chosen)
			runAll(t, store, s)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if sent.Status != "succeeded" || !strings.Contains(sent.Message, "already") {
				t.Errorf("the change = %+v, want it done at once with nothing sent", sent)
			}
			wanted := 1
			if before != nil {
				wanted = 2
			}
			if typed := goblin.waitForLines(t, wanted); len(typed) != wanted {
				t.Errorf("the goblin received %q, want %d lines", typed, wanted)
			}
			if got := store.Snapshot().Questions[0]; got.AnsweredOption != chosen {
				t.Errorf("question = %+v, want %s to stand", got, chosen)
			}
		})
	}
}

// A double submit and a retry after a dropped connection carry the same
// request ID, so his change to his own answer is sent once, whether the retry
// comes before the change ran or after.
func TestADoubleSubmitOrARetrySendsOneChange(t *testing.T) {
	for name, isRunFirst := range map[string]bool{"a double submit": false, "a retry after a dropped connection": true} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, _, goblin, _, connection, q := overlordAnswered(t)
			s := &Service{Store: store, Options: Options{CFO: connection}}
			first, err := change(store, "change-1", q, "Postgres")
			if err != nil {
				t.Fatal(err)
			}
			if isRunFirst {
				runAll(t, store, s)
			}

			// Act
			again, err := change(store, "change-1", q, "Postgres")
			runAll(t, store, s)

			// Assert
			if err != nil || again.ID != first.ID {
				t.Fatalf("the second submit = %+v (%v), want the first change", again, err)
			}
			typed := goblin.waitForLines(t, 2)
			if changes := changeLines(typed); len(changes) != 1 {
				t.Errorf("the goblin received %q, want one change", typed)
			}
			if n := len(slices.DeleteFunc(store.Snapshot().Actions, func(a Action) bool { return a.Kind != "answer_change" })); n != 1 {
				t.Errorf("the board holds %d changes, want one", n)
			}
		})
	}
}

// A change made while the first answer is still on its way waits for it, so
// the asker gets the first answer and then the change, never the change first
// and never only one of them.
func TestAChangeWhileTheFirstAnswerIsOnItsWayFollowsIt(t *testing.T) {
	t.Run("his answer to a goblin, held back", func(t *testing.T) {
		// Arrange
		store, _ := testStore(t)
		primaryFixture(t, store)
		meta, record, goblin, connection := goblinFixture(t, store)
		servePipe(t, store, connection)
		q := surfaced(t, store, meta, record, connection)
		s := &Service{Store: store, Options: Options{CFO: connection}}
		if _, err := store.Queue(Action{ID: "board-answer", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "SQLite", AnswerKind: "option"}); err != nil {
			t.Fatal(err)
		}
		holdBack(store, "board-answer", time.Hour)

		// Act
		_, err := change(store, "change-1", store.Snapshot().Questions[0], "Postgres")
		runAll(t, store, s)
		early := goblin.lines(t)
		holdBack(store, "board-answer", 0)
		runAll(t, store, s)

		// Assert
		if err != nil {
			t.Fatal(err)
		}
		if len(early) != 0 {
			t.Errorf("the goblin received %q while its first answer was held back, want nothing yet", early)
		}
		typed := goblin.waitForLines(t, 2)
		if len(typed) != 2 || !strings.Contains(typed[0], "SQLite") || strings.Contains(typed[0], "changed") {
			t.Fatalf("the goblin received %q, want the first answer and then the change", typed)
		}
		wantTheChange(t, typed[1], "SQLite", "Postgres")
		wantHisAnswer(t, store.Snapshot().Questions[0], "Postgres", "SQLite")
	})
	t.Run("his answer to the CFO, held back", func(t *testing.T) {
		// Arrange
		store, home := testStore(t)
		terminal := nativeCFOComposer(t, home.State, "claude-ready")
		identity := registrationIdentity(t, home.State)
		s := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: home.State}}, work: make(chan struct{}, 1)}
		cfoAsks(t, store, identity, sitting[0])
		heAnswers(t, store, identity, sitting[0])
		holdBack(store, "answer-"+sitting[0].id, time.Hour)
		q := store.Snapshot().Questions[0]

		// Act
		_, err := change(store, "change-1", q, "Not now")
		s.process(context.Background())
		early := overlordMessages(terminal.lines(t))
		holdBack(store, "answer-"+sitting[0].id, 0)
		s.process(context.Background())

		// Assert
		if err != nil {
			t.Fatal(err)
		}
		if len(early) != 0 {
			t.Errorf("the CFO received %q while his answer was held back, want nothing yet", early)
		}
		typed := overlordMessages(terminal.lines(t))
		if len(typed) != 2 || typed[0] != "Overlord: "+sitting[0].line() {
			t.Fatalf("the CFO received %q, want his answer and then the change", typed)
		}
		wantTheChange(t, typed[1], sitting[0].answer, "Not now")
	})
}

// holdBack keeps queued action id from running for d, as the supervisor does
// with a delivery that must wait; zero lets it run.
func holdBack(store *Store, id string, d time.Duration) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.deferredUntil == nil {
		store.deferredUntil = map[string]time.Time{}
	}
	store.deferredUntil[id] = time.Now().Add(d)
}

// Only an answered question changes, and only to one of its choices or a
// written answer. Anything else is refused before anyone hears a word.
func TestAnAnswerChangeIsRefusedBeforeAnyoneHearsAnything(t *testing.T) {
	for name, test := range map[string]struct {
		question func(t *testing.T) (*Store, hostedTerminal, Question)
		text     string
		refusal  string
	}{
		"a choice the question does not offer": {text: "Redis", refusal: "one of its choices", question: func(t *testing.T) (*Store, hostedTerminal, Question) {
			store, _, goblin, _, _, q := cfoAnswered(t)
			return store, goblin, q
		}},
		"a question still waiting on an answer": {text: "Postgres", refusal: "has no answer yet", question: func(t *testing.T) (*Store, hostedTerminal, Question) {
			store, _ := testStore(t)
			primaryFixture(t, store)
			meta, record, goblin, connection := goblinFixture(t, store)
			servePipe(t, store, connection)
			return store, goblin, surfaced(t, store, meta, record, connection)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, goblin, q := test.question(t)
			before := len(goblin.lines(t))

			// Act
			_, err := change(store, "change-1", q, test.text)

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.refusal) {
				t.Fatalf("change = %v, want it refused (%q)", err, test.refusal)
			}
			if typed := goblin.lines(t); len(typed) != before {
				t.Errorf("the goblin received %q, want nothing more", typed)
			}
		})
	}
}
