package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestCFOQuestionDurableConflictAndOwnedPublication(t *testing.T) {
	store, _ := testStore(t)
	primary, _, _, cfo := primaryFixture(t, store)
	servePipe(t, store, cfo)
	if err := cfo.PublishQuestion("question-1", "Pick a layout", []string{"Board", "Tree"}, "Tree"); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestQuestions(); err != nil {
		t.Fatal(err)
	}
	if len(store.Snapshot().Questions) != 1 {
		t.Fatal("missing published question")
	}
	if err := cfo.PublishQuestion("question-1", "Changed text", []string{"Other"}, ""); err == nil {
		t.Fatal("conflicting durable ID published")
	}
	if err := cfo.PublishQuestion("question-1", "Pick a layout", []string{"Board", "Tree"}, "Tree"); err != nil {
		t.Fatal(err)
	}
	primary.Process.PID = 1
	data, _ := json.Marshal(primary)
	if err := os.WriteFile(filepath.Join(store.Home.State, "primary.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := cfo.PublishQuestion("question-2", "Worker impersonation", nil, ""); err == nil {
		t.Fatal("non-CFO published a user modal")
	}
}

func TestQuestionPoisonCapacityAndReplacementCannotBlockHooks(t *testing.T) {
	store, h := testStore(t)
	primary, identity, _, _ := primaryFixture(t, store)
	q := Question{ID: "question-1", Identity: identity, Text: "Choose", Options: []string{"One", "Two"}, CreatedAt: time.Now().UTC()}
	if err := store.acceptQuestion(q); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.State, "questions-inbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	q.Text = "Conflicting"
	data, _ := json.Marshal(q)
	for name, body := range map[string][]byte{"bad.json": []byte("{"), "conflict.json": data, "oversize.json": []byte(strings.Repeat("x", 13<<10))} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := nativehook.Spool(h.State, event(t, h, "SessionStart", "native-start", "", time.Now())); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: store, work: make(chan struct{}, 1)}
	s.cycle(context.Background(), false)
	if len(store.Snapshot().Sessions) != 1 || len(store.Snapshot().Issues) != 3 {
		t.Fatal("question poison blocked native ingestion", store.Snapshot())
	}
	for i := 1; i < maxQuestions; i++ {
		q.ID = fmt.Sprintf("question-%d", i+1)
		if err := store.acceptQuestion(q); err != nil {
			t.Fatal(err)
		}
	}
	q.ID = "question-overflow"
	data, _ = json.Marshal(q)
	_ = os.WriteFile(filepath.Join(dir, "overflow.json"), data, 0600)
	if err := nativehook.Spool(h.State, event(t, h, "SessionStart", "next-native", "", time.Now())); err != nil {
		t.Fatal(err)
	}
	s.cycle(context.Background(), false)
	if len(store.Snapshot().Sessions) != 2 {
		t.Fatal("capacity blocked unrelated native event")
	}
	primary.Agent = "claude"
	data, _ = json.Marshal(primary)
	_ = os.WriteFile(filepath.Join(h.State, "primary.json"), data, 0600)
	if err := store.supersedeQuestions(); err != nil {
		t.Fatal(err)
	}
	for _, question := range store.Snapshot().Questions {
		if question.Status != "superseded" {
			t.Fatal("obsolete question still pops up")
		}
	}
	if err := store.ingestQuestions(); err != nil {
		t.Fatal("superseded questions did not retire", err)
	}
	if len(store.Snapshot().Questions) != maxQuestions {
		t.Fatal("question retention unbounded")
	}
}

func TestQuestionAnswerDeliveredOnlyOnceToCFOAndCrashUncertain(t *testing.T) {
	store, h := testStore(t)
	_, identity, terminal, cfo := primaryFixture(t, store)
	q := Question{ID: "question-1", Identity: identity, Text: "Choose", Options: []string{"One", "Two"}, CreatedAt: time.Now().UTC()}
	if err := store.acceptQuestion(q); err != nil {
		t.Fatal(err)
	}
	a := Action{ID: "answer-1", Kind: "cfo_answer", Generation: identity, QuestionID: q.ID, Text: "One"}
	bad := a
	bad.Text = "Invented"
	if _, err := store.Queue(bad); err == nil {
		t.Fatal("unreported option accepted")
	}
	if _, err := store.Queue(a); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queue(a); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: store, Options: Options{CFO: cfo}}
	if err := store.ProcessOne(context.Background(), s.execute); err != nil {
		t.Fatal(err)
	}
	if typed := terminal.lines(t); len(typed) != 1 || !strings.Contains(typed[0], "Answer: One") || store.Snapshot().Questions[0].Status != "succeeded" {
		t.Fatalf("the CFO's terminal received %q, want the answer once on one line", typed)
	}
	if _, err := store.Queue(a); err != nil {
		t.Fatal(err)
	}
	if err := store.ProcessOne(context.Background(), s.execute); err != nil {
		t.Fatal(err)
	}
	if typed := terminal.lines(t); len(typed) != 1 {
		t.Fatalf("the CFO's terminal received %q, want no duplicate answer replayed", typed)
	}
	store.db.Actions[0].Status = "running"
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Snapshot().Questions[0].Status != "uncertain" {
		t.Fatal("interrupted answer reopened or replayed")
	}
}

// An answer the board submits to a goblin or the CFO that is busy inside a
// long turn waits in its input until the turn ends, so it is delivered, not
// unconfirmed: the Overlord sees it arrive, and a goblin's notify reads
// answered so nobody asks it again.
func TestABoardAnswerToABusyAskerIsDeliveredNotUnconfirmed(t *testing.T) {
	for _, asker := range []string{"goblin", "cfo"} {
		t.Run(asker, func(t *testing.T) {
			store, h := testStore(t)
			var terminal hostedTerminal
			var s *Service
			var a Action
			if asker == "goblin" {
				meta, record, goblin, connection := goblinFixture(t, store)
				q := surfaced(t, store, meta, record, connection)
				terminal, s = goblin, &Service{Store: store, Options: Options{CFO: connection}}
				a = Action{ID: "answer-1", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "SQLite"}
			} else {
				_, identity, cfo, connection := primaryFixture(t, store)
				q := Question{ID: "question-1", Identity: identity, Text: "Choose", Options: []string{"One", "Two"}, CreatedAt: time.Now().UTC()}
				if err := store.acceptQuestion(q); err != nil {
					t.Fatal(err)
				}
				terminal, s = cfo, &Service{Store: store, Options: Options{CFO: connection}}
				a = Action{ID: "answer-1", Kind: "cfo_answer", Generation: identity, QuestionID: q.ID, Text: "One"}
			}
			terminal.startTurn(t)
			if _, err := store.Queue(a); err != nil {
				t.Fatal(err)
			}

			if err := store.ProcessOne(context.Background(), s.execute); err != nil {
				t.Fatal(err)
			}

			if typed := terminal.lines(t); len(typed) != 1 {
				t.Fatalf("the terminal received %q, want the answer submitted once", typed)
			}
			snapshot := store.Snapshot()
			if got := snapshot.Actions[len(snapshot.Actions)-1]; got.Status != "succeeded" || !strings.Contains(got.Message, "current turn") {
				t.Errorf("answer action = %+v, want it delivered while the %s's turn runs", got, asker)
			}
			if got := snapshot.Questions[0]; got.Status != "succeeded" {
				t.Errorf("question = %+v, want it answered", got)
			}
			if asker == "goblin" {
				pending, err := wake.Pending(h.State)
				if err != nil {
					t.Fatal(err)
				}
				if len(pending) != 1 || pending[0].Answered != "SQLite" {
					t.Errorf("notify = %+v, want it marked answered", pending)
				}
			}
		})
	}
}

// The shared senders keep reporting a delivery to a busy asker as waiting
// behind its turn, not as delivered, so run results and review requests read
// exactly what they read before; only the answer paths, the board's and cfo
// answer's, count it as delivered themselves.
func TestASendToABusyAskerOutsideABoardAnswerStaysUnconfirmed(t *testing.T) {
	for _, asker := range []string{"goblin", "cfo"} {
		t.Run(asker, func(t *testing.T) {
			store, _ := testStore(t)
			var terminal hostedTerminal
			var send func() (Evaluation, error)
			if asker == "goblin" {
				meta, record, goblin, connection := goblinFixture(t, store)
				q := surfaced(t, store, meta, record, connection)
				terminal = goblin
				send = func() (Evaluation, error) {
					return connection.SendGoblin(context.Background(), meta.ID, q.Identity, "SQLite")
				}
			} else {
				_, identity, cfo, connection := primaryFixture(t, store)
				terminal = cfo
				send = func() (Evaluation, error) { return connection.Send(context.Background(), identity, "Run finished") }
			}
			terminal.startTurn(t)

			_, err := send()

			if !errors.Is(err, fleet.ErrQueuedBehindTurn) {
				t.Fatalf("err = %v, want the busy %s's delivery reported as waiting behind its turn", err, asker)
			}
			if typed := terminal.lines(t); len(typed) != 1 {
				t.Fatalf("the terminal received %q, want the text submitted once", typed)
			}
		})
	}
}

func TestQuestionStorageFailurePreservesRetry(t *testing.T) {
	store, _ := testStore(t)
	_, identity, _, _ := primaryFixture(t, store)
	q := Question{ID: "question-1", Identity: identity, Text: "Choose", CreatedAt: time.Now().UTC()}
	restore := blockStoreWrites(t, store)
	if err := store.acceptQuestion(q); err == nil {
		t.Fatal("expected write failure")
	}
	restore()
	if len(store.Snapshot().Questions) != 0 {
		t.Fatal("failed question left memory changed")
	}
	if err := store.acceptQuestion(q); err != nil {
		t.Fatal(err)
	}
}

func TestQuestionRecommendationAndOtherAnswer(t *testing.T) {
	store, h := testStore(t)
	_, identity, terminal, cfo := primaryFixture(t, store)
	q := Question{ID: "question-other", Identity: identity, Text: "Where should we work?", Options: []string{"Current checkout", "Isolated worktree", "Wait"}, Recommended: "Isolated worktree", CreatedAt: time.Now().UTC()}
	bad := q
	bad.Recommended = "Invented choice"
	if err := store.acceptQuestion(bad); err == nil {
		t.Fatal("unreported recommendation accepted")
	}
	if err := store.acceptQuestion(q); err != nil {
		t.Fatal(err)
	}
	bad = q
	bad.Recommended = "Wait"
	if err := store.acceptQuestion(bad); err == nil {
		t.Fatal("same ID changed recommendation")
	}
	a := Action{ID: "other-answer-1", Kind: "cfo_answer", Generation: identity, QuestionID: q.ID, AnswerKind: "other", Text: "Use the folder with spaces:\nreview 日本語"}
	badAnswer := a
	badAnswer.AnswerKind = "option"
	if _, err := store.Queue(badAnswer); err == nil {
		t.Fatal("invented option accepted")
	}
	if _, err := store.Queue(a); err != nil {
		t.Fatal(err)
	}
	badAnswer = a
	badAnswer.AnswerKind = "option"
	if _, err := store.Queue(badAnswer); err == nil {
		t.Fatal("request identity ignored answer kind")
	}
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: reopened, Options: Options{CFO: cfo}}
	if err := reopened.ProcessOne(context.Background(), s.execute); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Queue(a); err != nil {
		t.Fatal(err)
	}
	if err := reopened.ProcessOne(context.Background(), s.execute); err != nil {
		t.Fatal(err)
	}
	if typed := terminal.lines(t); len(typed) != 1 || !strings.Contains(typed[0], "Answer (Other): Use the folder with spaces: review 日本語") {
		t.Fatalf("the CFO's terminal received %q, want the typed answer once on one line", typed)
	}
	got := reopened.Snapshot().Questions[0]
	if got.Answer != a.Text || got.AnswerKind != "other" || got.Status != "succeeded" || got.Recommended != q.Recommended {
		t.Fatal("durable receipt lost context", got)
	}
}

func TestDeferredQuestionsSurviveHistoryRollover(t *testing.T) {
	for _, sameTime := range []bool{false, true} {
		t.Run(fmt.Sprint("same_timestamp=", sameTime), func(t *testing.T) {
			store, h := testStore(t)
			_, identity, _, _ := primaryFixture(t, store)
			base := time.Now().UTC().Add(-time.Hour)
			for i := 0; i < maxQuestions; i++ {
				q := Question{ID: fmt.Sprintf("history-%03d", i), Identity: identity, Text: "Existing question", CreatedAt: base}
				if err := store.acceptQuestion(q); err != nil {
					t.Fatal(err)
				}
			}
			store.db.Questions[0].Status = "succeeded"
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(h.State, "questions-inbox")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			ids := []string{"deferred-A", "deferred-B", "deferred-C"}
			filename := func(id string) string { return fmt.Sprintf("%x.json", sha256.Sum256([]byte(id))) }
			slices.SortFunc(ids, func(a, b string) int { return strings.Compare(filename(a), filename(b)) })
			// The newest publication has the first hash filename.
			for i, id := range ids {
				created := base.Add(time.Duration(10-i) * time.Second)
				if sameTime {
					created = base.Add(time.Second)
				}
				q := Question{ID: id, Identity: identity, Text: "Deferred question", CreatedAt: created, Task: "task-1", Generation: "g1", Seq: i + 1}
				data, _ := json.Marshal(q)
				if err := os.WriteFile(filepath.Join(dir, filename(id)), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			seen := map[string]bool{}
			for round := 0; round < len(ids); round++ {
				if err := store.ingestQuestions(); err != nil {
					t.Fatal(err)
				}
				for i := range store.db.Questions {
					q := &store.db.Questions[i]
					if slices.Contains(ids, q.ID) {
						seen[q.ID] = true
						q.Status = "succeeded"
					}
				}
				if err := store.save(); err != nil {
					t.Fatal(err)
				}
				inbox, err := os.ReadDir(dir)
				if err != nil {
					t.Fatal(err)
				}
				if len(seen)+len(inbox) != len(ids) {
					t.Fatalf("unseen question lost: seen=%v pending=%d", seen, len(inbox))
				}
				if len(store.Snapshot().Questions) != maxQuestions {
					t.Fatal("question history exceeded its bound")
				}
				reopened, err := Open(h)
				if err != nil {
					t.Fatal(err)
				}
				store = reopened
			}
			if len(seen) != len(ids) {
				t.Fatalf("deferred questions not delivered: %v", seen)
			}
		})
	}
}

// goblinFixture is a live native goblin, task-1, blocked on a notify that
// offers two choices. This test process stands in for the program of its
// terminal, which shows Claude Code's composer and records what is typed
// into it.
func goblinFixture(t *testing.T, store *Store) (state.TaskMeta, wake.Record, hostedTerminal, *CFOConnection) {
	t.Helper()
	meta := makeNative(t, store.Home.State, "task-1")
	meta.Harness = "claude"
	if err := state.WriteTaskMeta(store.Home.State, meta); err != nil {
		t.Fatal(err)
	}
	record, err := wake.Append(store.Home.State, "notify", meta.ID, "blocked: Which store? options: Postgres | SQLite (Recommended) | MySQL (Recommended)")
	if err != nil {
		t.Fatal(err)
	}
	return meta, record, goblinTerminal(t, store.Home.State, meta.ID), &CFOConnection{State: store.Home.State}
}

// goblinTerminal hosts native goblin id's terminal, showing Claude Code's
// composer, with this test process standing in for its program.
func goblinTerminal(t *testing.T, stateDir, id string) hostedTerminal {
	t.Helper()
	goblin := hostTerminal(t, stateDir, id)
	goblin.typeLine(t, "harness")
	goblin.standIn(t)
	return goblin
}

// surfaced publishes the fixture's notify on the board and returns the
// question the supervisor ingested.
func surfaced(t *testing.T, store *Store, meta state.TaskMeta, record wake.Record, connection *CFOConnection) Question {
	t.Helper()
	if err := SurfaceNotify(store.Home.State, meta.ID, record, record.Detail, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestQuestions(); err != nil {
		t.Fatal(err)
	}
	questions := store.Snapshot().Questions
	if len(questions) != 1 {
		t.Fatalf("questions = %+v, want the goblin's one question", questions)
	}
	return questions[0]
}

// A goblin's blocked notify with choices becomes a Command Center question
// labelled with the goblin, and the Overlord's answer reaches that goblin's
// own terminal exactly once. The CFO's notify then reads answered.
func TestGoblinQuestionAnsweredOnceInItsOwnTerminal(t *testing.T) {
	store, h := testStore(t)
	meta, record, goblin, connection := goblinFixture(t, store)
	if err := SurfaceNotify(h.State, meta.ID, record, record.Detail, nil); err != nil {
		t.Fatal(err)
	}
	q := surfaced(t, store, meta, record, connection)
	if q.Task != meta.ID || q.Generation != meta.SpawnGen || q.Seq != record.Seq || q.Text != "Which store?" || !slices.Equal(q.Options, []string{"Postgres", "SQLite", "MySQL"}) || q.Recommended != "SQLite" {
		t.Fatalf("surfaced question = %+v", q)
	}
	if _, err := store.Queue(Action{ID: "cfo-route", Kind: "cfo_answer", Generation: q.Identity, QuestionID: q.ID, Text: "SQLite"}); err == nil {
		t.Fatal("a goblin's question accepted an answer routed to the CFO")
	}
	cfoQuestion := Question{ID: "question-cfo", Identity: strings.Repeat("c", 64), Text: "Ship it?", Options: []string{"Yes", "No"}, CreatedAt: time.Now().UTC()}
	if err := store.acceptQuestion(cfoQuestion); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queue(Action{ID: "goblin-route", Kind: "goblin_answer", Generation: cfoQuestion.Identity, QuestionID: cfoQuestion.ID, Text: "Yes"}); err == nil {
		t.Fatal("the CFO's question accepted an answer routed to a goblin")
	}
	a := Action{ID: "answer-1", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "SQLite"}
	s := &Service{Store: store, Options: Options{CFO: connection}}
	for i := 0; i < 2; i++ {
		if _, err := store.Queue(a); err != nil {
			t.Fatal(err)
		}
		if err := store.ProcessOne(context.Background(), s.execute); err != nil {
			t.Fatal(err)
		}
	}
	if typed := goblin.lines(t); len(typed) != 1 || !strings.Contains(typed[0], "Question: Which store?") || !strings.Contains(typed[0], "Answer: SQLite") {
		t.Fatalf("the goblin received %q, want the answer delivered once on one line", typed)
	}
	if got := store.Snapshot().Questions[0]; got.Status != "succeeded" || got.Answer != "SQLite" || got.AnsweredOption != "SQLite" || got.AnsweredBy != "overlord" || got.AnsweredAt == nil {
		t.Fatalf("question = %+v, want it answered on the board by the Overlord with SQLite", got)
	}
	if got := store.Snapshot().Questions[1]; got.Status != "pending" || got.AnswerID != "" {
		t.Fatalf("the CFO's question = %+v, want it untouched", got)
	}
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Answered != "SQLite" || pending[0].AnsweredBy != wake.AnsweredByOverlord {
		t.Fatalf("notify = %+v, want it marked answered on the board", pending)
	}
}

// A goblin's question keeps its own line breaks on the board, so its bullets
// read as bullets, while the answer it gets back still reaches its terminal as
// one line, since a line break there would submit the prompt early.
func TestGoblinQuestionKeepsItsLinesOnTheBoardAndItsAnswerIsOneLine(t *testing.T) {
	store, h := testStore(t)
	meta, _, goblin, connection := goblinFixture(t, store)
	asked := "blocked: Ship the report?\n- **Verdict:** not yet\n- one blocking defect options: Ship now | Hold (Recommended)"
	// The queue holds the one-line form cfo notify records.
	record, err := wake.Append(h.State, "notify", meta.ID, state.NormalizeStatusDetail(asked))
	if err != nil {
		t.Fatal(err)
	}

	if err := SurfaceNotify(h.State, meta.ID, record, asked, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestQuestions(); err != nil {
		t.Fatal(err)
	}
	questions := store.Snapshot().Questions
	if len(questions) != 1 {
		t.Fatalf("questions = %+v, want the goblin's one question", questions)
	}
	q := questions[0]
	a := Action{ID: "answer-lines", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "Hold"}
	if _, err := store.Queue(a); err != nil {
		t.Fatal(err)
	}
	if err := store.ProcessOne(context.Background(), (&Service{Store: store, Options: Options{CFO: connection}}).execute); err != nil {
		t.Fatal(err)
	}

	if q.Text != "Ship the report?\n- **Verdict:** not yet\n- one blocking defect" || !slices.Equal(q.Options, []string{"Ship now", "Hold"}) || q.Recommended != "Hold" {
		t.Fatalf("surfaced question = %+v, want its lines and choices as asked", q)
	}
	if typed := goblin.lines(t); len(typed) != 1 || !strings.Contains(typed[0], "Question: Ship the report? - **Verdict:** not yet - one blocking defect Answer: Hold") {
		t.Fatalf("the goblin received %q, want the answer on one line with the question flattened", typed)
	}
}

// An answer follows the goblin that asked, never its successor, and never
// arrives after the CFO has already handled the question itself.
func TestGoblinAnswerRefusedAfterRespawnOrCFOAck(t *testing.T) {
	respawn := func(t *testing.T, h string, meta state.TaskMeta) {
		meta.SpawnGen = "g2"
		if err := state.WriteTaskMeta(h, meta); err != nil {
			t.Fatal(err)
		}
	}
	ack := func(t *testing.T, h string, record wake.Record) {
		if err := wake.AckThrough(h, record.Seq); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("respawn before the answer", func(t *testing.T) {
		store, h := testStore(t)
		meta, record, goblin, connection := goblinFixture(t, store)
		q := surfaced(t, store, meta, record, connection)
		respawn(t, h.State, meta)
		if _, err := store.Queue(Action{ID: "answer-1", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "SQLite"}); err == nil {
			t.Fatal("an answer for the previous generation was queued")
		}
		if err := store.supersedeQuestions(); err != nil {
			t.Fatal(err)
		}
		if got, typed := store.Snapshot().Questions[0], goblin.lines(t); got.Status != "superseded" || len(typed) != 0 {
			t.Fatalf("question = %+v, the goblin received %q", got, typed)
		}
	})
	for name, change := range map[string]func(*testing.T, string, state.TaskMeta, wake.Record){
		"respawn while queued": func(t *testing.T, h string, meta state.TaskMeta, _ wake.Record) { respawn(t, h, meta) },
		"CFO ack while queued": func(t *testing.T, h string, _ state.TaskMeta, record wake.Record) { ack(t, h, record) },
	} {
		t.Run(name, func(t *testing.T) {
			store, h := testStore(t)
			meta, record, goblin, connection := goblinFixture(t, store)
			q := surfaced(t, store, meta, record, connection)
			if _, err := store.Queue(Action{ID: "answer-1", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "SQLite"}); err != nil {
				t.Fatal(err)
			}
			change(t, h.State, meta, record)
			s := &Service{Store: store, Options: Options{CFO: connection}}
			if err := store.ProcessOne(context.Background(), s.execute); err != nil {
				t.Fatal(err)
			}
			if got, typed := store.Snapshot().Questions[0], goblin.lines(t); got.Status != "failed" || !strings.Contains(got.Message, "nothing was sent") || len(typed) != 0 {
				t.Fatalf("question = %+v, the goblin received %q", got, typed)
			}
		})
	}
	t.Run("CFO ack before the answer", func(t *testing.T) {
		store, h := testStore(t)
		meta, record, _, connection := goblinFixture(t, store)
		surfaced(t, store, meta, record, connection)
		ack(t, h.State, record)
		if err := store.supersedeQuestions(); err != nil {
			t.Fatal(err)
		}
		if got := store.Snapshot().Questions[0]; got.Status != "superseded" || !strings.Contains(got.Message, "CFO already handled") {
			t.Fatalf("question = %+v", got)
		}
	})
}

// Only a notify that offers choices opens the modal, and only a process in
// the goblin's own terminal can surface it.
func TestSurfaceNotifyNeedsChoicesAndTheGoblinsOwnTerminal(t *testing.T) {
	store, h := testStore(t)
	meta, record, goblin, _ := goblinFixture(t, store)
	plain, err := wake.Append(h.State, "notify", meta.ID, "blocked: Should I merge this?")
	if err != nil {
		t.Fatal(err)
	}
	if err := SurfaceNotify(h.State, meta.ID, plain, plain.Detail, nil); err != nil {
		t.Fatal(err)
	}
	failed, err := wake.Append(h.State, "notify", meta.ID, "failed: Tests fail. options: Retry (Recommended) | Abandon")
	if err != nil {
		t.Fatal(err)
	}
	if err := SurfaceNotify(h.State, meta.ID, failed, failed.Detail, nil); err != nil {
		t.Fatal(err)
	}
	// The System process is live and never the ancestor of a test.
	goblin.runs(t, 4)
	if err := SurfaceNotify(h.State, meta.ID, record, record.Detail, nil); err == nil {
		t.Fatal("a process outside the goblin's terminal surfaced its question")
	}
	if err := store.ingestQuestions(); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Questions; len(got) != 0 {
		t.Fatalf("questions = %+v, want none", got)
	}
}

// The CFO answers a goblin's question with cfo answer: the choice reaches the
// goblin once, the way cfo send types, its notify reads answered, and the
// board records the choice, that the CFO gave it and when, even when the CFO
// drains the notify before the supervisor takes the answer.
func TestCFOAnswerDeliversOnceAndTheBoardRecordsIt(t *testing.T) {
	store, h := testStore(t)
	primaryFixture(t, store)
	meta, record, goblin, connection := goblinFixture(t, store)
	servePipe(t, store, connection)
	q := surfaced(t, store, meta, record, connection)
	chosen, _, err := connection.AnswerGoblin(context.Background(), fmt.Sprint(record.Seq), "sqlite", "keep it local")
	if err != nil || chosen != "SQLite" {
		t.Fatalf("answer = %q, %v; want SQLite, chosen by its first word", chosen, err)
	}
	want := fmt.Sprintf("CFO: decision %d: SQLite. keep it local", record.Seq)
	if typed := goblin.lines(t); len(typed) != 1 || typed[0] != want {
		t.Fatalf("the goblin received %q, want %q once", typed, want)
	}
	if _, _, err := connection.AnswerGoblin(context.Background(), q.ID, "Postgres", ""); err == nil || len(goblin.lines(t)) != 1 {
		t.Fatalf("a second answer = %v with the goblin holding %q, want refused and nothing sent", err, goblin.lines(t))
	}
	pending, err := wake.Pending(h.State)
	if err != nil || len(pending) != 1 || pending[0].Answered != "SQLite. keep it local" || pending[0].AnsweredBy != wake.AnsweredByCFO {
		t.Fatalf("notify = %+v (%v), want it marked answered", pending, err)
	}
	if err := wake.AckThrough(h.State, record.Seq); err != nil {
		t.Fatal(err)
	}
	if err := store.supersedeQuestions(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nativehook.SpoolDir(h.State), 0700); err != nil {
		t.Fatal(err)
	}
	(&Service{Store: store, work: make(chan struct{}, 1)}).cycle(context.Background(), false)
	got := store.Snapshot().Questions[0]
	if got.Status != "succeeded" || got.Answer != "SQLite. keep it local" || got.AnsweredOption != "SQLite" || got.AnsweredBy != "cfo" || got.AnsweredAt == nil {
		t.Fatalf("question = %+v, want it closed as answered by the CFO with SQLite", got)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var board map[string]any
	if err := json.Unmarshal(data, &board); err != nil {
		t.Fatal(err)
	}
	if board["answered_option"] != "SQLite" || board["answered_by"] != "cfo" || board["answered_at"] == nil {
		t.Fatalf("board question = %v, want answered_option, answered_by and answered_at", board)
	}
}

// cfo answer refuses, sending nothing and leaving nothing for the board, a
// caller that is not the registered CFO, a choice the question does not
// offer, a label that names two choices, a notify with no choices, one the
// CFO already handled, and a goblin that restarted since it asked.
func TestCFOAnswerRefusesBeforeSendingAnything(t *testing.T) {
	for _, c := range []struct {
		name     string
		register bool
		notify   string
		option   string
		before   func(t *testing.T, store *Store, meta state.TaskMeta, record wake.Record)
		refusal  string
	}{
		{name: "a caller that is not the registered CFO", option: "SQLite", refusal: "not registered"},
		{name: "a choice the question does not offer", register: true, option: "Redis", refusal: "is not one of the choices"},
		{name: "a label that names two choices", register: true, notify: "blocked: Which plan? options: a keep it | a drop it", option: "a", refusal: "names more than one choice"},
		{name: "a notify with no choices", register: true, notify: "blocked: What next?", option: "a", refusal: "answer it with cfo send"},
		{name: "a notify the CFO already handled", register: true, option: "SQLite", refusal: "is not waiting", before: func(t *testing.T, store *Store, _ state.TaskMeta, record wake.Record) {
			if err := wake.AckThrough(store.Home.State, record.Seq); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a goblin that restarted", register: true, option: "SQLite", refusal: "restarted or ended", before: func(t *testing.T, store *Store, meta state.TaskMeta, _ wake.Record) {
			meta.SpawnGen = "g2"
			if err := state.WriteTaskMeta(store.Home.State, meta); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a question the Overlord is answering on the board", register: true, option: "SQLite", refusal: "the Overlord is answering", before: func(t *testing.T, store *Store, _ state.TaskMeta, _ wake.Record) {
			q := store.Snapshot().Questions[0]
			if _, err := store.Queue(Action{ID: "board-answer", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "Postgres"}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			store, h := testStore(t)
			if c.register {
				primaryFixture(t, store)
			}
			meta, record, goblin, connection := goblinFixture(t, store)
			if c.notify != "" {
				var err error
				if record, err = wake.Append(h.State, "notify", meta.ID, c.notify); err != nil {
					t.Fatal(err)
				}
			}
			if c.notify == "" || strings.Contains(c.notify, "options:") {
				surfaced(t, store, meta, record, connection)
			}
			if c.before != nil {
				c.before(t, store, meta, record)
			}
			_, _, err := connection.AnswerGoblin(context.Background(), fmt.Sprint(record.Seq), c.option, "")
			if typed := goblin.lines(t); err == nil || !strings.Contains(err.Error(), c.refusal) || len(typed) != 0 {
				t.Fatalf("answer = %v with the goblin receiving %q, want refused (%q) with nothing sent", err, typed, c.refusal)
			}
			if entries, err := os.ReadDir(filepath.Join(h.State, answersInbox)); !os.IsNotExist(err) && len(entries) != 0 {
				t.Fatalf("a refused answer left %d records for the board", len(entries))
			}
		})
	}
}

// A goblin's question can wait in the CFO's queue without ever reaching the
// board, when its notify could not show it there (cg-native-desktop's 3593 on
// 2026-10-01, refused by the board's proof). Once the question has had its
// moment to arrive, cfo answer delivers the choice to the goblin that asked
// and marks the notify answered, instead of asking the CFO to try again for a
// question that never comes. One raised a moment ago may still be on its
// way, and one asked before the goblin restarted reaches nobody.
func TestCFOAnswersAQuestionThatNeverReachedTheBoard(t *testing.T) {
	for _, c := range []struct {
		name      string
		grace     time.Duration
		restarted bool
		refusal   string
	}{
		{name: "a question past its moment to arrive", grace: 0},
		{name: "a question raised a moment ago", grace: time.Hour, refusal: "has not reached the board yet"},
		{name: "a question asked before the goblin restarted", grace: 0, restarted: true, refusal: "restarted"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			defer func(grace time.Duration) { questionArrival = grace }(questionArrival)
			questionArrival = c.grace
			store, h := testStore(t)
			primaryFixture(t, store)
			meta, record, goblin, connection := goblinFixture(t, store)
			servePipe(t, store, connection)
			spawned := record.Time.Add(-time.Hour)
			if c.restarted {
				spawned = record.Time.Add(time.Second)
			}
			meta.SpawnGen = fmt.Sprintf("s%d", spawned.UnixNano())
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}

			// Act
			chosen, _, err := connection.AnswerGoblin(context.Background(), fmt.Sprint(record.Seq), "sqlite", "")

			// Assert
			if c.refusal != "" {
				if typed := goblin.lines(t); err == nil || !strings.Contains(err.Error(), c.refusal) || len(typed) != 0 {
					t.Fatalf("answer = %q, %v with the goblin receiving %q, want refused (%q) with nothing sent", chosen, err, typed, c.refusal)
				}
				return
			}
			want := fmt.Sprintf("CFO: decision %d: SQLite", record.Seq)
			if typed := goblin.lines(t); err != nil || chosen != "SQLite" || len(typed) != 1 || typed[0] != want {
				t.Fatalf("answer = %q, %v with the goblin receiving %q; want SQLite delivered once as %q", chosen, err, typed, want)
			}
			pending, err := wake.Pending(h.State)
			if err != nil || len(pending) != 1 || pending[0].Answered != "SQLite" || pending[0].AnsweredBy != wake.AnsweredByCFO {
				t.Fatalf("notify = %+v (%v), want it marked answered by the CFO", pending, err)
			}
		})
	}
}

// The CFO answers first, and the supervisor records that answer as it takes
// it over the pipe, so the Overlord's board answer that comes after is
// refused before anything is queued or sent. The question shows the choice
// the goblin received from the CFO, never the refused one, and keeps it after
// a reload.
func TestCFOAnswerStandsWhenTheBoardAnswerIsRefused(t *testing.T) {
	store, h := testStore(t)
	primaryFixture(t, store)
	meta, record, goblin, connection := goblinFixture(t, store)
	servePipe(t, store, connection)
	q := surfaced(t, store, meta, record, connection)
	if err := os.MkdirAll(nativehook.SpoolDir(h.State), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := connection.AnswerGoblin(context.Background(), q.ID, "SQLite", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queue(Action{ID: "board-answer", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "Postgres"}); err == nil {
		t.Fatal("the board answer was queued after the CFO's answer was recorded, want it refused")
	}
	if typed := goblin.lines(t); len(typed) != 1 {
		t.Fatalf("the goblin received %q, want only the CFO's answer", typed)
	}
	(&Service{Store: store, work: make(chan struct{}, 1)}).cycle(context.Background(), false)
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]Question{"after the pass": store.Snapshot().Questions[0], "after a reload": reopened.Snapshot().Questions[0]} {
		if got.Status != "succeeded" || got.AnsweredBy != "cfo" || got.AnsweredOption != "SQLite" || got.Answer != "SQLite" || got.AnsweredAt == nil {
			t.Fatalf("%s, question = %+v, want it answered by the CFO with SQLite", name, got)
		}
	}
}

// A board answer that reaches the goblin's terminal while cfo answer is still
// delivering waits for it, then sees the CFO's answer and is refused with
// nothing sent, so the goblin receives one decision.
func TestBoardAnswerWaitsForAnInFlightCFOAnswer(t *testing.T) {
	store, h := testStore(t)
	primaryFixture(t, store)
	meta, record, goblin, connection := goblinFixture(t, store)
	servePipe(t, store, connection)
	q := surfaced(t, store, meta, record, connection)
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}}}
	done := make(chan error, 1)
	whileDelivering(t, func() {
		if _, err := store.Queue(Action{ID: "board-answer", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "Postgres"}); err != nil {
			t.Fatal(err)
		}
		go func() { done <- store.ProcessOne(context.Background(), service.execute) }()
		time.Sleep(500 * time.Millisecond)
	})
	if _, _, err := connection.AnswerGoblin(context.Background(), q.ID, "SQLite", ""); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if typed := goblin.lines(t); len(typed) != 1 || !strings.Contains(typed[0], "SQLite") {
		t.Fatalf("the goblin received %q; want only the CFO's decision", typed)
	}
	if action := store.Snapshot().Actions[len(store.Snapshot().Actions)-1]; action.Status != "failed" || !strings.Contains(action.Message, "nothing was sent") {
		t.Fatalf("board answer = %+v, want refused with nothing sent", action)
	}
}

// Answers to different goblins never wait on each other: while the CFO's
// answer to goblin A is still being delivered, holding A's question, the
// Overlord's board answer to goblin B goes straight through, and both goblins
// receive their decision.
func TestCFOAndBoardAnswersToDifferentGoblinsBothDeliver(t *testing.T) {
	store, h := testStore(t)
	primaryFixture(t, store)
	metaA, recordA, goblinA, connectionA := goblinFixture(t, store)
	servePipe(t, store, connectionA)
	metaB := metaA
	metaB.ID = "task-2"
	if err := state.WriteTaskMeta(h.State, metaB); err != nil {
		t.Fatal(err)
	}
	goblinB := goblinTerminal(t, h.State, metaB.ID)
	recordB, err := wake.Append(h.State, "notify", metaB.ID, "blocked: Which cache? options: Redis | Memcached")
	if err != nil {
		t.Fatal(err)
	}
	connectionB := &CFOConnection{State: h.State}
	if err := SurfaceNotify(h.State, metaA.ID, recordA, recordA.Detail, nil); err != nil {
		t.Fatal(err)
	}
	if err := SurfaceNotify(h.State, metaB.ID, recordB, recordB.Detail, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestQuestions(); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(store.Snapshot().Questions, func(q Question) bool { return q.Task == metaB.ID })
	if i < 0 {
		t.Fatalf("questions = %+v, want goblin B's", store.Snapshot().Questions)
	}
	questionB := store.Snapshot().Questions[i]

	service := &Service{Store: store, Options: Options{CFO: connectionB}}
	var boardErr error
	whileDelivering(t, func() {
		if _, err := store.Queue(Action{ID: "board-b", Kind: "goblin_answer", Generation: questionB.Identity, QuestionID: questionB.ID, Text: "Redis"}); err != nil {
			t.Fatal(err)
		}
		boardErr = store.ProcessOne(context.Background(), service.execute)
	})
	if _, _, err := connectionA.AnswerGoblin(context.Background(), fmt.Sprint(recordA.Seq), "SQLite", ""); err != nil {
		t.Fatal(err)
	}
	if boardErr != nil {
		t.Fatal(boardErr)
	}
	i = slices.IndexFunc(store.Snapshot().Questions, func(q Question) bool { return q.ID == questionB.ID })
	if got, typedA, typedB := store.Snapshot().Questions[i], goblinA.lines(t), goblinB.lines(t); got.Status != "succeeded" || len(typedA) != 1 || len(typedB) != 1 {
		t.Fatalf("goblin B's board answer, given while the CFO's answer to goblin A was in flight, = %s (%s); goblin A got %q and goblin B %q, want one decision each", got.Status, got.Message, typedA, typedB)
	}
}

// whileDelivering runs act once, as the next delivery to a goblin begins and
// before anything is typed, then lets that delivery and every later one go on
// as usual.
func whileDelivering(t *testing.T, act func()) {
	t.Helper()
	deliver := typeIntoGoblin
	t.Cleanup(func() { typeIntoGoblin = deliver })
	typeIntoGoblin = func(ctx context.Context, stateDir string, meta state.TaskMeta, text string) error {
		typeIntoGoblin = deliver
		act()
		return deliver(ctx, stateDir, meta, text)
	}
}
