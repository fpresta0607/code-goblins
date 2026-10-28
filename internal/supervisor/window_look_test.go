package supervisor

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/waiting"
)

// The desktop window looks every three seconds for what newly waits on the
// Overlord, and on 2026-10-28 it read the whole snapshot at each look, 984 KB
// of the fleet for a handful of items, which also built a snapshot whenever
// the fleet had changed since the last. Its look answers the items alone,
// read from the store's memory, with each goblin named as the newest
// snapshot built names it, so looks build no snapshot however much changes
// between them (#1716).
func TestTheWindowsLookAnswersWhatWaitsWithoutBuildingASnapshot(t *testing.T) {
	// Arrange: the CFO's question and a goblin's question the CFO has left,
	// on a board whose snapshot builds are counted.
	store, _ := testStore(t)
	asked := time.Now().UTC().Add(-11 * time.Minute).Truncate(time.Second)
	if err := store.acceptQuestion(Question{ID: "pick-a-store", Identity: strings.Repeat("c", 64), Text: "Which store?", CreatedAt: asked}); err != nil {
		t.Fatal(err)
	}
	if err := store.acceptQuestion(Question{ID: "notify-olive-1", Identity: strings.Repeat("d", 64), Task: "olive-task", Generation: "1", Seq: 1, Text: "Which port?", CreatedAt: asked}); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: store, Instance: "test-instance", subscribers: map[chan struct{}]struct{}{}, done: make(chan struct{})}
	var builds atomic.Int32
	s.buildSnapshot = func() (Snapshot, error) {
		builds.Add(1)
		return Snapshot{Instance: s.Instance, Tasks: []Task{{ID: "olive-task", Title: "Olive"}}}, nil
	}
	handler := NewHTTP(s, "127.0.0.1:4310", nil)
	look := func() (int, waiting.Look) {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "http://127.0.0.1:4310/api/waiting", nil))
		var answer waiting.Look
		if response.Code == 200 {
			if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil {
				t.Fatalf("the look answered %s: %v", response.Body, err)
			}
		}
		return response.Code, answer
	}

	// Act: the window looks, the fleet changes three times, and it looks again.
	look()
	for range 3 {
		s.notify()
	}
	status, answer := look()

	// Assert
	at := asked.Format(time.RFC3339)
	want := []waiting.Item{
		{ID: "question:pick-a-store@" + at, Asker: "CFO", Text: "The CFO asks: Which store?"},
		{ID: "question:notify-olive-1@" + at, Asker: "Olive", Text: "Olive asks: Which port?", IsQuiet: true},
	}
	if status != 200 || answer.Instance != "test-instance" || !slices.Equal(answer.Items, want) {
		t.Fatalf("the look answered %d %+v, want 200 with instance test-instance and %+v", status, answer, want)
	}
	if got := builds.Load(); got != 1 {
		t.Errorf("the two looks built %d snapshots, want only the first, when none had been built", got)
	}
}
