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
	if err := PublishReview(ctx, h, connection.Herdr, meta.ID, "mockups-review-1", "Pick a task list layout", "http://127.0.0.1:4387/session/f26e", paths); err != nil {
		t.Fatal(err)
	}
	if err := PublishReview(ctx, h, connection.Herdr, meta.ID, "mockups-review-1", "Pick a task list layout", "http://127.0.0.1:4387/session/f26e", paths); err != nil {
		t.Fatalf("an unchanged republish was refused: %v", err)
	}
	if err := PublishReview(ctx, h, connection.Herdr, meta.ID, "mockups-review-1", "Another title", "", paths); err == nil || !strings.Contains(err.Error(), "already used") {
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
	if err := PublishReview(ctx, h, connection.Herdr, meta.ID, "plan-review-1", "Read the plan", "", nil); err != nil {
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
