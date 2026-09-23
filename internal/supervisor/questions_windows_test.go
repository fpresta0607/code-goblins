package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

func TestCFOQuestionDurableConflictAndOwnedPublication(t *testing.T) {
	store, _ := testStore(t)
	primary, _, _, cfo := primaryFixture(t, store)
	if err := cfo.PublishQuestion(context.Background(), "question-1", "Pick a layout", []string{"Board", "Tree"}, "Tree"); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestQuestions(); err != nil {
		t.Fatal(err)
	}
	if len(store.Snapshot().Questions) != 1 {
		t.Fatal("missing published question")
	}
	if err := cfo.PublishQuestion(context.Background(), "question-1", "Changed text", []string{"Other"}, ""); err == nil {
		t.Fatal("conflicting durable ID published")
	}
	if err := cfo.PublishQuestion(context.Background(), "question-1", "Pick a layout", []string{"Board", "Tree"}, "Tree"); err != nil {
		t.Fatal(err)
	}
	primary.Process.PID = 1
	data, _ := json.Marshal(primary)
	if err := os.WriteFile(filepath.Join(store.Home.State, "primary.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := cfo.PublishQuestion(context.Background(), "question-2", "Worker impersonation", nil, ""); err == nil {
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
	primary.Terminal = "new-terminal"
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
	_, identity, runner, cfo := primaryFixture(t, store)
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
	s := &Service{Store: store, Options: Options{CFO: cfo, Send: func(context.Context, string, string) error { t.Fatal("answer reached worker"); return nil }}}
	if err := store.ProcessOne(context.Background(), s.execute); err != nil {
		t.Fatal(err)
	}
	if len(runner.prompts) != 1 || !strings.Contains(runner.prompts[0], "Answer: One") || store.Snapshot().Questions[0].Status != "succeeded" {
		t.Fatal("answer missing")
	}
	if _, err := store.Queue(a); err != nil {
		t.Fatal(err)
	}
	if err := store.ProcessOne(context.Background(), s.execute); err != nil {
		t.Fatal(err)
	}
	if len(runner.prompts) != 1 {
		t.Fatal("duplicate answer replayed")
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
	_, identity, runner, cfo := primaryFixture(t, store)
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
	a := Action{ID: "other-answer-1", Kind: "cfo_answer", Generation: identity, QuestionID: q.ID, AnswerKind: "other", Text: "Use the folder with spaces: review 日本語"}
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
	s := &Service{Store: reopened, Options: Options{CFO: cfo, Send: func(context.Context, string, string) error { t.Fatal("answer reached worker"); return nil }}}
	if err := reopened.ProcessOne(context.Background(), s.execute); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Queue(a); err != nil {
		t.Fatal(err)
	}
	if err := reopened.ProcessOne(context.Background(), s.execute); err != nil {
		t.Fatal(err)
	}
	if len(runner.prompts) != 1 || !strings.Contains(runner.prompts[0], "Answer (Other): "+a.Text) {
		t.Fatal("typed answer not delivered exactly once", len(runner.prompts))
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
				q := Question{ID: id, Identity: identity, Text: "Deferred question", CreatedAt: created}
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
