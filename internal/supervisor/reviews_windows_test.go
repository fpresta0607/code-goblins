package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// A goblin's review stays open until it is cleared or withdrawn: its images
// are served from the copy after the worktree's files are gone, across a
// supervisor restart and after the goblin respawns.
func TestReviewItemOutlivesItsSourceTheSupervisorAndTheGoblin(t *testing.T) {
	store, h := testStore(t)
	meta, _, _, connection := goblinFixture(t, store)
	paths := []string{filepath.Join(meta.Worktree, "grid.png"), filepath.Join(meta.Worktree, "list.png")}
	var data [][]byte
	for _, path := range paths {
		data = append(data, writePNG(t, path))
	}
	ctx := context.Background()
	if err := PublishReview(ctx, h, connection.Herdr, meta.ID, "mockups-review-1", "Pick a task list layout", "http://127.0.0.1:4387/session/f26e", "", paths); err != nil {
		t.Fatal(err)
	}
	if err := PublishReview(ctx, h, connection.Herdr, meta.ID, "mockups-review-1", "Pick a task list layout", "http://127.0.0.1:4387/session/f26e", "", paths); err != nil {
		t.Fatalf("an unchanged republish was refused: %v", err)
	}
	if err := PublishReview(ctx, h, connection.Herdr, meta.ID, "mockups-review-1", "Another title", "", "", paths); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("a republish with other content = %v, want refused", err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	got := store.Snapshot().Reviews
	if len(got) != 1 || got[0].Task != meta.ID || got[0].Identity != goblinIdentity(meta) || got[0].State != "open" || len(got[0].ImageSums) != 2 {
		t.Fatalf("reviews = %+v, want the goblin's open item with two images", got)
	}
	s := &Service{Store: store}
	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	published, _ := json.Marshal(snapshot)
	if snapshot.Reviews[0].ImageCount != 2 || snapshot.Reviews[0].ImageSums != nil || bytes.Contains(published, []byte(got[0].ImageSums[0])) || bytes.Contains(published, []byte("grid.png")) {
		t.Fatalf("the board saw %+v, want the image count without digests or paths", snapshot.Reviews[0])
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	meta.SpawnGen = "g2"
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTP(&Service{Store: reopened}, "board.local", nil)
	get := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local"+path, nil))
		return response
	}
	for i := range paths {
		response := get(fmt.Sprintf("/api/reviews/mockups-review-1/images/%d", i))
		if response.Code != 200 || response.Header().Get("Content-Type") != "image/png" || !bytes.Equal(response.Body.Bytes(), data[i]) {
			t.Fatalf("image %d after its source, the supervisor and the goblin all went = %d", i, response.Code)
		}
	}
	for _, path := range []string{"/api/reviews/mockups-review-1/images/2", "/api/reviews/mockups-review-1/images/x", "/api/reviews/unknown-review/images/0"} {
		if response := get(path); response.Code != 404 {
			t.Fatalf("%s = %d, want 404", path, response.Code)
		}
	}
	if got := reopened.Snapshot().Reviews; len(got) != 1 || got[0].State != "open" {
		t.Fatalf("after a restart and a respawn = %+v, want the item still open", got)
	}
}

// Only the reporter that published an item can withdraw it, and its reason
// is kept.
func TestReviewWithdrawnOnlyByItsReporter(t *testing.T) {
	store, h := testStore(t)
	meta, _, _, connection := goblinFixture(t, store)
	ctx := context.Background()
	if err := PublishReview(ctx, h, connection.Herdr, meta.ID, "plan-review-1", "Read the plan", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := spoolReview(h.State, Review{ID: "plan-review-1", Identity: strings.Repeat("e", 64), State: "withdrawn", Reason: "not mine", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot(); got.Reviews[0].State != "open" || !strings.Contains(strings.Join(got.Issues, "\n"), "only the reporter") {
		t.Fatalf("a stranger's withdrawal = %+v %q, want it refused", got.Reviews[0], got.Issues)
	}
	if err := WithdrawReview(ctx, h, connection.Herdr, meta.ID, "plan-review-1", "Replaced by plan-review-2"); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Reviews[0]; got.State != "withdrawn" || got.Reason != "Replaced by plan-review-2" {
		t.Fatalf("review = %+v, want withdrawn with its reason", got)
	}
}

// cfo review withdraw refuses, recording nothing, an ID it does not know, an
// item already closed and an item another reporter published.
func TestReviewWithdrawalRefusedBeforeRecordingAnything(t *testing.T) {
	store, h := testStore(t)
	meta, _, _, connection := goblinFixture(t, store)
	ctx := context.Background()
	if err := PublishReview(ctx, h, connection.Herdr, meta.ID, "closed-review", "Read the plan", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.clearReview("closed-review", goblinIdentity(meta)); err != nil {
		t.Fatal(err)
	}
	if err := store.acceptReview(openReview("stranger-review", meta.ID)); err != nil {
		t.Fatal(err)
	}
	for id, says := range map[string]string{
		"unknown-review":  "no review with that ID",
		"closed-review":   "already cleared",
		"stranger-review": "only the reporter",
	} {
		if err := WithdrawReview(ctx, h, connection.Herdr, meta.ID, id, "Replaced"); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("withdrawing %s = %v, want refused naming %q", id, err, says)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(h.State, "reviews-inbox")); err != nil || len(entries) != 0 {
		t.Fatalf("refused withdrawals left %d inbox records: %v", len(entries), err)
	}
}

// A republish of an ID that ingest records while the reporter is looking
// never touches the recorded item's images, and leaves none of its own.
func TestReviewRepublishNeverTouchesTheRecordedImages(t *testing.T) {
	store, h := testStore(t)
	meta, _, _, connection := goblinFixture(t, store)
	grid, list := filepath.Join(meta.Worktree, "grid.png"), filepath.Join(meta.Worktree, "list.gif")
	want := writePNG(t, grid)
	if err := os.WriteFile(list, []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := PublishReview(ctx, h, connection.Herdr, meta.ID, "mockups-review-1", "Pick a layout", "", "", []string{grid}); err != nil {
		t.Fatal(err)
	}
	inbox := reviewInboxPath(h.State, "mockups-review-1", "open")
	data, err := os.ReadFile(inbox)
	if err != nil {
		t.Fatal(err)
	}
	var first Review
	if err := json.Unmarshal(data, &first); err != nil {
		t.Fatal(err)
	}
	// Ingest has taken the record out of the inbox but not yet saved it when
	// the republish looks, and saves it before the republish is spooled.
	if err := os.Remove(inbox); err != nil {
		t.Fatal(err)
	}
	if err := PublishReview(ctx, h, connection.Herdr, meta.ID, "mockups-review-1", "Pick a layout", "", "", []string{list}); err != nil {
		t.Fatal(err)
	}
	if err := store.acceptReview(first); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot(); len(got.Reviews) != 1 || !strings.Contains(strings.Join(got.Issues, "\n"), "already used") {
		t.Fatalf("reviews %+v, issues %q; want the first item kept and the republish refused", got.Reviews, got.Issues)
	}
	response := httptest.NewRecorder()
	NewHTTP(&Service{Store: store}, "board.local", nil).ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/reviews/mockups-review-1/images/0", nil))
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), want) {
		t.Fatalf("the recorded item's image = %d %q, want its own PNG", response.Code, response.Header().Get("Content-Type"))
	}
	if entries, err := os.ReadDir(filepath.Join(h.State, "reviews")); err != nil || len(entries) != 1 {
		t.Fatalf("state/reviews holds %d directories, want only the recorded item's: %v", len(entries), err)
	}
}

// A publication refused because the inbox is full copies no image.
func TestReviewRefusedByAFullInboxLeavesNoCopies(t *testing.T) {
	store, h := testStore(t)
	meta, _, _, connection := goblinFixture(t, store)
	grid := filepath.Join(meta.Worktree, "grid.png")
	writePNG(t, grid)
	inbox := filepath.Join(h.State, "reviews-inbox")
	if err := os.MkdirAll(inbox, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2*maxReviews; i++ {
		if err := os.WriteFile(filepath.Join(inbox, fmt.Sprintf("%03d.open.json", i)), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := PublishReview(context.Background(), h, connection.Herdr, meta.ID, "mockups-review-1", "Pick a layout", "", "", []string{grid}); err == nil || !strings.Contains(err.Error(), "inbox is full") {
		t.Fatalf("a publication into a full inbox = %v, want refused", err)
	}
	if entries, err := os.ReadDir(filepath.Join(h.State, "reviews")); !os.IsNotExist(err) && len(entries) != 0 {
		t.Fatalf("a refused publication left %d image copies: %v", len(entries), err)
	}
}

// The Overlord's answer reaches the item's reporter once: a live goblin in its
// own pane, or the CFO that reported it. An answer for a goblin that restarted
// goes to the CFO instead, and the item reads undelivered.
func TestReviewAnswerReachesItsReporterOnceOrElseTheCFO(t *testing.T) {
	for _, c := range []struct {
		name         string
		goblin       bool
		respawn      bool
		wantText     string
		wantDelivery bool
	}{
		{"a live goblin", true, false, "The Overlord answered your review item plan-review-1 (Read the plan): Go with the grid", true},
		{"a respawned goblin", true, true, "which came to you because task-1 restarted or ended: Go with the grid", false},
		{"the CFO", false, false, "Answer to your review item plan-review-1 (Read the plan): Go with the grid", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			store, h := testStore(t)
			_, _, runner, cfo := primaryFixture(t, store)
			meta, _, _, _ := goblinFixture(t, store)
			t.Setenv("CFO_SESSION_ID", "actual-primary")
			t.Setenv("CFO_SESSION_HARNESS", "codex")
			task := ""
			if c.goblin {
				task = meta.ID
			}
			ctx := context.Background()
			if err := PublishReview(ctx, h, cfo.Herdr, task, "plan-review-1", "Read the plan", "", "", nil); err != nil {
				t.Fatal(err)
			}
			if err := store.ingestReviews(); err != nil {
				t.Fatal(err)
			}
			r := store.Snapshot().Reviews[0]
			a := Action{ID: "answer-review-1", Kind: "review_answer", ReviewID: r.ID, Generation: r.Identity, Text: "Go with the grid"}
			for i := 0; i < 2; i++ {
				if _, err := store.Queue(a); err != nil {
					t.Fatal(err)
				}
			}
			if c.respawn {
				meta.SpawnGen = "g2"
				if err := state.WriteTaskMeta(h.State, meta); err != nil {
					t.Fatal(err)
				}
			}
			s := &Service{Store: store, Options: Options{CFO: cfo}}
			if err := store.ProcessOne(ctx, s.execute); err != nil {
				t.Fatal(err)
			}
			got := store.Snapshot()
			if len(runner.prompts) != 1 || !strings.Contains(runner.prompts[0], c.wantText) {
				t.Fatalf("prompts = %q, want exactly one carrying %q", runner.prompts, c.wantText)
			}
			if review := got.Reviews[0]; review.State != "answered" || review.Answer != "Go with the grid" || review.AnswerID != a.ID || review.Delivered != c.wantDelivery {
				t.Fatalf("review = %+v, want answered with delivered=%v", review, c.wantDelivery)
			}
			if action := got.Actions[len(got.Actions)-1]; action.Status != "succeeded" {
				t.Fatalf("answer action = %+v, want succeeded", action)
			}
			if _, err := store.Queue(Action{ID: "answer-review-2", Kind: "review_answer", ReviewID: r.ID, Generation: r.Identity, Text: "Actually the list"}); err == nil {
				t.Fatal("an answered review took a second answer")
			}
		})
	}
}

// The Overlord's board answer on a goblin's item reaches the goblin and, as a
// notice that asks nothing, the CFO; the CFO's own item needs no notice.
func TestABoardAnswerOnAGoblinsItemAlsoReachesTheCFO(t *testing.T) {
	for name, goblin := range map[string]bool{"a goblin's item": true, "the CFO's own item": false} {
		t.Run(name, func(t *testing.T) {
			store, h := testStore(t)
			_, _, _, cfo := primaryFixture(t, store)
			meta, _, _, _ := goblinFixture(t, store)
			t.Setenv("CFO_SESSION_ID", "actual-primary")
			t.Setenv("CFO_SESSION_HARNESS", "codex")
			task := ""
			if goblin {
				task = meta.ID
			}
			ctx := context.Background()
			if err := PublishReview(ctx, h, cfo.Herdr, task, "plan-review-1", "Read the plan", "", "", nil); err != nil {
				t.Fatal(err)
			}
			if err := store.ingestReviews(); err != nil {
				t.Fatal(err)
			}
			r := store.Snapshot().Reviews[0]
			if _, err := store.Queue(Action{ID: "answer-review-1", Kind: "review_answer", ReviewID: r.ID, Generation: r.Identity, Text: "Go with the grid"}); err != nil {
				t.Fatal(err)
			}

			if err := store.ProcessOne(ctx, (&Service{Store: store, Options: Options{CFO: cfo}}).execute); err != nil {
				t.Fatal(err)
			}

			if !goblin {
				records, err := wake.Pending(h.State)
				if err != nil {
					t.Fatal(err)
				}
				for _, record := range records {
					if record.Kind == "review" {
						t.Fatalf("review wake %+v, want none: the CFO answered its own item", record)
					}
				}
				return
			}
			wakes := reviewWakes(t, h.State, meta.ID)
			if len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "plan-review-1") || !strings.Contains(wakes[0].Detail, "Go with the grid") {
				t.Fatalf("review wakes = %+v, want one carrying the item and the answer", wakes)
			}
			if _, blocking := wake.BlockingNotify(wakes[0]); blocking {
				t.Fatalf("the notice %+v blocks the drain, want it to ask nothing", wakes[0])
			}
		})
	}
}

// With the list full and a publication waiting, an item the Overlord just
// answered keeps its place until its answer is delivered, and only then makes
// room for the waiting one.
func TestAnsweredReviewSurvivesAFullListUntilDelivered(t *testing.T) {
	store, h := testStore(t)
	_, _, runner, cfo := primaryFixture(t, store)
	t.Setenv("CFO_SESSION_ID", "actual-primary")
	t.Setenv("CFO_SESSION_HARNESS", "codex")
	ctx := context.Background()
	if err := PublishReview(ctx, h, cfo.Herdr, "", "plan-review-1", "Read the plan", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < maxReviews; i++ {
		if err := store.acceptReview(openReview(fmt.Sprintf("bulk-review-%03d", i), "task-1")); err != nil {
			t.Fatal(err)
		}
	}
	if err := spoolReview(h.State, openReview("waiting-review", "task-1")); err != nil {
		t.Fatal(err)
	}
	r := store.Snapshot().Reviews[0]
	if _, err := store.Queue(Action{ID: "answer-review-1", Kind: "review_answer", ReviewID: r.ID, Generation: r.Identity, Text: "Go with the grid"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Reviews; got[0].ID != r.ID || got[0].State != "answered" || got[len(got)-1].ID == "waiting-review" {
		t.Fatalf("reviews start with %s and end with %s; want the answered item kept and the publication still waiting", got[0].ID, got[len(got)-1].ID)
	}
	if err := store.ProcessOne(ctx, (&Service{Store: store, Options: Options{CFO: cfo}}).execute); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Reviews[0]; len(runner.prompts) != 1 || !strings.Contains(runner.prompts[0], "Go with the grid") || got.ID != r.ID || !got.Delivered {
		t.Fatalf("prompts = %q and review = %+v, want the answer delivered once", runner.prompts, got)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Reviews; got[0].ID == r.ID || got[len(got)-1].ID != "waiting-review" {
		t.Fatalf("reviews start with %s and end with %s; want the delivered item to make room for the waiting one", got[0].ID, got[len(got)-1].ID)
	}
}

// A wait on the Overlord reaches the Command Center as the item the goblin's
// next report withdraws.
func TestWaitOnTheOverlordIsTheItemItsNextReportWithdraws(t *testing.T) {
	store, h := testStore(t)
	meta, _, _, connection := goblinFixture(t, store)
	if err := state.AppendStatus(h.State, meta.ID, "waiting on overlord: log in to Stripe"); err != nil {
		t.Fatal(err)
	}
	if err := PublishWait(context.Background(), h, connection.Herdr, meta.ID, 7, "log in to Stripe", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	if err := store.retireWaits(); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Reviews; len(got) != 1 || got[0].ID != "waiting-task-1-7" || got[0].Title != "Waiting on you: log in to Stripe" || got[0].Task != meta.ID || got[0].State != "open" {
		t.Fatalf("reviews = %+v, want the open item waiting-task-1-7 for the current wait", got)
	}
	if err := state.AppendStatus(h.State, meta.ID, "working: charging the card"); err != nil {
		t.Fatal(err)
	}
	if err := store.retireWaits(); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Reviews[0]; got.State != "withdrawn" {
		t.Fatalf("the wait after the goblin reported again = %+v, want it withdrawn", got)
	}
}
