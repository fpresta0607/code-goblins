package supervisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func openReview(id, task string, sums ...string) Review {
	now := time.Now().UTC()
	return Review{ID: id, Identity: strings.Repeat("a", 64), Task: task, Title: "Look at " + id, ImageSums: sums, State: "open", CreatedAt: now, UpdatedAt: now}
}

func TestValidReviewRefusesWhatTheBoardCannotShowSafely(t *testing.T) {
	sum := strings.Repeat("0", 64)
	tailnet := openReview("mockups-review-1", "task-1", sum)
	tailnet.Lavish = "http://sermon.tailcc4238.ts.net:4387/session/f26e"
	if err := validReview(tailnet); err != nil {
		t.Fatal(err)
	}
	page := func(task, link, file string) Review {
		r := openReview("page-review-1", task)
		r.Lavish, r.LavishPage = link, file
		return r
	}
	link, file := "http://127.0.0.1:4387/session/f26e", `C:\work\.lavish\plan.html`
	if err := validReview(page("task-1", link, file)); err != nil {
		t.Fatalf("a goblin's page beside its link was refused: %v", err)
	}
	if err := validReview(page("", link, file)); err != nil {
		t.Fatalf("the CFO's own page beside its link was refused: %v", err)
	}
	for name, r := range map[string]Review{
		"an ID with a slash":  openReview("mockups/review", "task-1"),
		"an ID too short":     openReview("short", "task-1"),
		"images from the CFO": openReview("cfo-review-1", "", sum),
		"thirteen images":     openReview("many-review", "task-1", strings.Split(strings.Repeat(sum+",", 13), ",")[:13]...),
		"a malformed digest":  openReview("digest-review", "task-1", "zz"),
		"an empty title":      func() Review { r := openReview("empty-review", "task-1"); r.Title = " "; return r }(),
		"a foreign plain http link": func() Review {
			r := openReview("link-review", "task-1")
			r.Lavish = "http://192.0.2.10:4387/session/x"
			return r
		}(),
		"a page without its link":  page("task-1", "", file),
		"a relative page":          page("task-1", link, `.lavish\plan.html`),
		"a page path left unclean": page("task-1", link, `C:\work\..\work\.lavish\plan.html`),
		"a page that is not HTML":  page("task-1", link, `C:\work\.lavish\plan.txt`),
	} {
		if err := validReview(r); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// Clearing closes an open review once, only for the identity the board saw,
// and a pending question can never be cleared.
func TestClearActionsCloseOnlyWhatTheOverlordMayClear(t *testing.T) {
	store, _ := testStore(t)
	r := openReview("mockups-review-1", "task-1")
	if err := store.acceptReview(r); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, q := range []Question{
		{ID: "question-superseded", Identity: strings.Repeat("c", 64), Text: "Ship it?", Options: []string{"Yes", "No"}, CreatedAt: now},
		{ID: "question-pending", Identity: strings.Repeat("c", 64), Text: "Merge it?", Options: []string{"Yes", "No"}, CreatedAt: now},
	} {
		if err := store.acceptQuestion(q); err != nil {
			t.Fatal(err)
		}
	}
	store.mu.Lock()
	store.db.Questions[0].Status = "superseded"
	store.mu.Unlock()
	for name, a := range map[string]Action{
		"a review for another identity": {ID: "clear-other", Kind: "review_clear", ReviewID: r.ID, Generation: strings.Repeat("b", 64)},
		"a review with text":            {ID: "clear-text", Kind: "review_clear", ReviewID: r.ID, Generation: r.Identity, Text: "why"},
		"a pending question":            {ID: "clear-pending", Kind: "question_clear", QuestionID: "question-pending", Generation: strings.Repeat("c", 64)},
	} {
		if _, err := store.Queue(a); err == nil {
			t.Errorf("clearing %s was queued", name)
		}
	}
	s := &Service{Store: store}
	for _, a := range []Action{
		{ID: "clear-review", Kind: "review_clear", ReviewID: r.ID, Generation: r.Identity},
		{ID: "clear-question", Kind: "question_clear", QuestionID: "question-superseded", Generation: strings.Repeat("c", 64)},
	} {
		for i := 0; i < 2; i++ {
			if _, err := store.Queue(a); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.ProcessOne(context.Background(), s.execute); err != nil {
			t.Fatal(err)
		}
	}
	got := store.Snapshot()
	if got.Reviews[0].State != "cleared" || got.Questions[0].Status != "cleared" || got.Questions[1].Status != "pending" {
		t.Fatalf("review %q, questions %q and %q; want cleared, cleared and still pending", got.Reviews[0].State, got.Questions[0].Status, got.Questions[1].Status)
	}
	if _, err := store.Queue(Action{ID: "clear-again", Kind: "review_clear", ReviewID: r.ID, Generation: r.Identity}); err == nil {
		t.Fatal("a cleared review was cleared again")
	}
}

// Closed items and their copied images go a set time after they closed; open
// items never do, and a full list of open items defers a new one.
func TestReviewsPruneClosedItemsAndNeverDropOpenOnes(t *testing.T) {
	store, h := testStore(t)
	old := time.Now().UTC().Add(-closedReviewRetention - time.Hour)
	closed, open := openReview("closed-review", "task-1", strings.Repeat("0", 64)), openReview("open-review", "task-1")
	for _, r := range []Review{closed, open} {
		if err := store.acceptReview(r); err != nil {
			t.Fatal(err)
		}
	}
	dir := reviewImageDir(h.State, closed)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(dir, "0"))
	store.mu.Lock()
	store.db.Reviews[0].State, store.db.Reviews[0].UpdatedAt = "cleared", old
	store.db.Reviews[1].UpdatedAt = old
	store.mu.Unlock()
	if err := store.pruneReviews(time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Reviews; len(got) != 1 || got[0].ID != open.ID {
		t.Fatalf("reviews = %+v, want only the open one", got)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the pruned item's images remain: %v", err)
	}
	for i := len(store.Snapshot().Reviews); i < maxReviews; i++ {
		if err := store.acceptReview(openReview(fmt.Sprintf("bulk-review-%03d", i), "task-1")); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.acceptReview(openReview("one-too-many", "task-1")); err != ErrDeferred {
		t.Fatalf("a review past a full list of open ones = %v, want deferred", err)
	}
}

// A withdrawal of a publication still waiting in the inbox waits with it
// instead of being rejected.
func TestReviewWithdrawalWaitsForItsDeferredPublication(t *testing.T) {
	store, h := testStore(t)
	for i := 0; i < maxReviews; i++ {
		if err := store.acceptReview(openReview(fmt.Sprintf("bulk-review-%03d", i), "task-1")); err != nil {
			t.Fatal(err)
		}
	}
	waiting := openReview("waiting-review", "task-1")
	for _, r := range []Review{waiting, {ID: waiting.ID, Identity: waiting.Identity, Task: "task-1", State: "withdrawn", Reason: "Replaced", UpdatedAt: time.Now().UTC()}} {
		if err := spoolReview(h.State, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(h.State, "reviews-inbox"))
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot(); len(entries) != 2 || len(got.Issues) != 0 {
		t.Fatalf("inbox holds %d records and issues are %q; want the publication and its withdrawal both waiting", len(entries), got.Issues)
	}
}

// A board answer that ended failed stays cleared once the Overlord clears it,
// in the same step and after a reload.
func TestClearedFailedQuestionStaysCleared(t *testing.T) {
	store, h := testStore(t)
	identity := strings.Repeat("c", 64)
	q := Question{ID: "question-failed", Identity: identity, Text: "Ship it?", Options: []string{"Yes", "No"}, CreatedAt: time.Now().UTC()}
	if err := store.acceptQuestion(q); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.db.Actions = append(store.db.Actions, Action{ID: "answer-failed", Kind: "goblin_answer", Generation: identity, QuestionID: q.ID, Text: "Yes", Status: "failed", Message: "the goblin is gone"})
	store.db.Questions[0].AnswerID, store.db.Questions[0].Status = "answer-failed", "failed"
	err := store.save()
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queue(Action{ID: "clear-failed", Kind: "question_clear", QuestionID: q.ID, Generation: identity}); err != nil {
		t.Fatal(err)
	}
	if err := store.ProcessOne(context.Background(), (&Service{Store: store}).execute); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Questions[0].Status; got != "cleared" {
		t.Fatalf("after clearing, the question is %q, want cleared", got)
	}
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Snapshot().Questions[0].Status; got != "cleared" {
		t.Fatalf("after a reload, the question is %q, want cleared", got)
	}
}

// With 128 questions held and none cleared or answered, a new question takes
// the oldest superseded one's place; a pending question is never dropped.
func TestFullQuestionListDropsOnlyTheOldestSuperseded(t *testing.T) {
	store, _ := testStore(t)
	start := time.Now().UTC().Add(-time.Hour)
	question := func(i int) Question {
		return Question{ID: fmt.Sprintf("question-%03d", i), Identity: strings.Repeat("c", 64), Text: "Ship it?", Options: []string{"Yes", "No"}, CreatedAt: start.Add(time.Duration(i) * time.Second)}
	}
	for i := 0; i < maxQuestions; i++ {
		if err := store.acceptQuestion(question(i)); err != nil {
			t.Fatal(err)
		}
	}
	store.mu.Lock()
	store.db.Questions[1].Status, store.db.Questions[2].Status = "superseded", "superseded"
	store.mu.Unlock()
	for n, dropped := range []string{"question-001", "question-002"} {
		if err := store.acceptQuestion(question(maxQuestions + n)); err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(store.Snapshot().Questions, func(q Question) bool { return q.ID == dropped }) {
			t.Fatalf("%s was kept; want the oldest superseded question dropped", dropped)
		}
	}
	got := store.Snapshot().Questions
	if len(got) != maxQuestions || got[0].ID != "question-000" || got[0].Status != "pending" {
		t.Fatalf("held %d questions, first %+v; want 128 with the oldest pending one kept", len(got), got[0])
	}
	if err := store.acceptQuestion(question(2 * maxQuestions)); err != ErrDeferred {
		t.Fatalf("a question past 128 pending ones = %v, want deferred", err)
	}
}

// A goblin's newer report says what it is doing: back at work after a
// question, or waiting on something. A newer question still wins, and a wait
// on another task clears once that task reports done.
func TestSnapshotReadsWorkingAndWaitingOnReports(t *testing.T) {
	store, h := testStore(t)
	s := &Service{Store: store}
	task := func() Task {
		t.Helper()
		snapshot, err := s.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range snapshot.Tasks {
			if candidate.ID == "task-1" {
				return candidate
			}
		}
		t.Fatal("task-1 missing from the snapshot")
		return Task{}
	}
	report := func(id, line string) {
		t.Helper()
		if err := state.AppendStatus(h.State, id, line); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := wake.Append(h.State, "notify", "task-1", "blocked: Which store?"); err != nil {
		t.Fatal(err)
	}
	report("task-1", "blocked: Which store?")
	if got := task(); got.Phase != "blocked" {
		t.Fatalf("a task holding a question = %+v, want blocked", got.Evaluation)
	}
	report("task-1", "working: wiring the store")
	if got := task(); got.Phase != "working" || got.Reason != "wiring the store" || got.Activity != "working: wiring the store" {
		t.Fatalf("a task back at work = %+v activity %q, want working", got.Evaluation, got.Activity)
	}
	report("task-1", "waiting on ci: PR 45 checks")
	if got := task(); got.Phase != "waiting" || got.WaitingOn != "ci" || got.Reason != "PR 45 checks" {
		t.Fatalf("a task waiting on CI = %+v, want waiting on ci", got.Evaluation)
	}
	report("task-1", "waiting on task-2: its API contract")
	if got := task(); got.Phase != "waiting" || got.WaitingOn != "task-2" {
		t.Fatalf("a task waiting on another = %+v, want waiting on task-2", got.Evaluation)
	}
	report("task-2", "done: PR https://github.com/example/repo/pull/7")
	if got := task(); got.Phase == "waiting" || got.WaitingOn != "" {
		t.Fatalf("a wait on a task that finished = %+v, want it cleared", got.Evaluation)
	}
	store.mu.Lock()
	store.db.Tasks["task-1"] = Evaluation{Phase: "blocked", Reason: "Pipeline decision required at review", Generation: "g1"}
	store.mu.Unlock()
	report("task-1", "working: polishing docs")
	if got := task(); got.Phase != "blocked" {
		t.Fatalf("a report under a gate parked for a decision = %+v, want the gate's blocked", got.Evaluation)
	}
	store.mu.Lock()
	delete(store.db.Tasks, "task-1")
	store.mu.Unlock()
	if _, err := wake.Append(h.State, "notify", "task-1", "blocked: Which port?"); err != nil {
		t.Fatal(err)
	}
	report("task-1", "blocked: Which port?")
	if got := task(); got.Phase != "blocked" {
		t.Fatalf("a newer question = %+v, want blocked", got.Evaluation)
	}
	// Done is per pull request: a goblin that asks about one PR and then
	// reports another done still owes the answer.
	report("task-1", "done: PR https://github.com/example/repo/pull/8")
	if got := task(); got.Phase != "blocked" || got.Reason != "Waiting on the CFO: Which port?" {
		t.Fatalf("a question followed by a done report = %+v, want blocked on it", got.Evaluation)
	}
	// A report older than a pending question never replaces it, even as the
	// last status line: the CFO's release can land between a goblin's status
	// line and its wake.
	older := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339) + " working: released by the CFO\n"
	if err := os.WriteFile(filepath.Join(h.State, "task-1.status"), []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := task(); got.Phase != "blocked" || got.Reason != "Waiting on the CFO: Which port?" {
		t.Fatalf("a question newer than the latest report = %+v, want blocked on it", got.Evaluation)
	}
}

// A wait on the Overlord stays in the Command Center while it is the goblin's
// latest report, and is withdrawn once the goblin reports anything newer.
func TestWaitOnTheOverlordRetiresWhenTheGoblinReportsAgain(t *testing.T) {
	store, h := testStore(t)
	if err := state.AppendStatus(h.State, "task-1", "waiting on overlord: log in to Stripe"); err != nil {
		t.Fatal(err)
	}
	wait := openReview("waiting-task-1-7", "task-1")
	wait.Title = "Waiting on you: log in to Stripe"
	if err := store.acceptReview(wait); err != nil {
		t.Fatal(err)
	}
	if err := store.retireItems(); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Reviews[0]; got.State != "open" {
		t.Fatalf("a current wait = %+v, want it open", got)
	}
	if err := state.AppendStatus(h.State, "task-1", "working: charging the card"); err != nil {
		t.Fatal(err)
	}
	if err := store.retireItems(); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Reviews[0]; got.State != "withdrawn" || got.Reason != "task-1 reported again: working: charging the card" {
		t.Fatalf("a wait the goblin moved past = %+v, want it withdrawn with the new report", got)
	}
}

// A goblin's own item, such as a page or images to look at, stays while the
// goblin keeps working, asks or waits, and closes once its task finishes after
// publishing it or is gone: nobody will act on an answer then.
func TestAGoblinsOwnItemClosesOnceItsTaskFinishesOrIsGone(t *testing.T) {
	store, h := testStore(t)
	earlier := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339) + " done: PR https://github.com/o/r/pull/6\n"
	if err := os.WriteFile(filepath.Join(h.State, "task-1.status"), []byte(earlier), 0o644); err != nil {
		t.Fatal(err)
	}
	item := openReview("task-1-mockups", "task-1")
	item.CreatedAt = time.Now().UTC().Add(-time.Hour)
	if err := store.acceptReview(item); err != nil {
		t.Fatal(err)
	}
	retire := func() Review {
		t.Helper()
		if err := store.retireItems(); err != nil {
			t.Fatal(err)
		}
		return store.Snapshot().Reviews[0]
	}
	if got := retire(); got.State != "open" {
		t.Fatalf("an item published after its task's last done report = %+v, want it open", got)
	}
	for _, report := range []string{"working: drawing the second pass", "blocked: which palette?", "waiting on overlord: the palette"} {
		if err := state.AppendStatus(h.State, "task-1", report); err != nil {
			t.Fatal(err)
		}
		if got := retire(); got.State != "open" {
			t.Fatalf("after %q the item = %+v, want it open", report, got)
		}
	}
	if err := state.AppendStatus(h.State, "task-1", "done: PR https://github.com/o/r/pull/7"); err != nil {
		t.Fatal(err)
	}
	if got := retire(); got.State != "withdrawn" || got.Reason != "task-1 finished: done: PR https://github.com/o/r/pull/7" {
		t.Fatalf("after its task finished the item = %+v, want it withdrawn with the report", got)
	}

	if err := store.acceptReview(openReview("task-9-plan", "task-9")); err != nil {
		t.Fatal(err)
	}
	if err := store.retireItems(); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Reviews[1]; got.State != "withdrawn" || got.Reason != "task-9 is gone" {
		t.Fatalf("an item whose task was cleaned up = %+v, want it withdrawn", got)
	}
}

// A CFO audit record in a task's status log is the CFO's word: the board
// still reads the goblin's own latest report, and a wait on the Overlord
// stays open.
func TestCFOAuditLinesNeverCountAsTheGoblinsReport(t *testing.T) {
	for _, audit := range []string{
		"pipeline-findings-accepted: step=review run=r1 round=2 findings=ask,bug by=cfo",
		"pipeline-policy-migrated: class=ordinary review_cycles=3 old=aaa new=bbb",
	} {
		t.Run(audit, func(t *testing.T) {
			store, h := testStore(t)
			s := &Service{Store: store}
			task := func() Task {
				t.Helper()
				snapshot, err := s.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				for _, candidate := range snapshot.Tasks {
					if candidate.ID == "task-1" {
						return candidate
					}
				}
				t.Fatal("task-1 missing from the snapshot")
				return Task{}
			}
			report := func(line string) {
				t.Helper()
				if err := state.AppendStatus(h.State, "task-1", line); err != nil {
					t.Fatal(err)
				}
			}
			report("working: wiring the store")
			report(audit)
			if got := task(); got.Phase != "working" || got.Reason != "wiring the store" || got.Activity != "working: wiring the store" {
				t.Fatalf("a working report under an audit line = %+v activity %q, want working", got.Evaluation, got.Activity)
			}
			report("waiting on overlord: log in to Stripe")
			wait := openReview("waiting-task-1-7", "task-1")
			if err := store.acceptReview(wait); err != nil {
				t.Fatal(err)
			}
			report(audit)
			if got := task(); got.Phase != "waiting" || got.WaitingOn != "overlord" || got.Reason != "log in to Stripe" {
				t.Fatalf("a wait under an audit line = %+v, want waiting on the overlord", got.Evaluation)
			}
			if err := store.retireItems(); err != nil {
				t.Fatal(err)
			}
			if got := store.Snapshot().Reviews[0]; got.State != "open" {
				t.Fatalf("a wait under an audit line = %+v, want it open", got)
			}
		})
	}
}
