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
