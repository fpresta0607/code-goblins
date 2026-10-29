package supervisor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// On 2026-10-02 the live board took 3 to 13 seconds to build one snapshot,
// which reads the fleet from several hundred files, and every open board
// built its own for every change: three boards, three builds of the same
// thing. One change costs one build, whoever asks: every board's stream and
// the desktop app's reads of /api/snapshot share it.
func TestOneChangeCostsOneSnapshotBuildHoweverManyBoardsAreOpen(t *testing.T) {
	// Arrange: three boards open, each with its first snapshot.
	store, _ := testStore(t)
	s := &Service{Store: store, Instance: "test-instance", subscribers: map[chan struct{}]struct{}{}, done: make(chan struct{})}
	handler := NewHTTP(s, "", nil)
	var builds atomic.Int64
	s.buildSnapshot = func() (Snapshot, error) {
		builds.Add(1)
		return s.Snapshot()
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	handler.Host = strings.TrimPrefix(server.URL, "http://")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	boards := []<-chan streamEvent{openBoard(t, ctx, server.URL), openBoard(t, ctx, server.URL), openBoard(t, ctx, server.URL)}
	for _, board := range boards {
		nextEvent(t, board, "a board's first snapshot")
	}
	builds.Store(0)

	// Act: one change, then the desktop app reads the snapshot twice.
	s.publish(nil)
	for _, board := range boards {
		nextEvent(t, board, "a board's change")
	}
	for range 2 {
		response, err := http.Get(server.URL + "/api/snapshot")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/snapshot = %d", response.StatusCode)
		}
	}

	// Assert
	if got := builds.Load(); got != 1 {
		t.Fatalf("one change with three boards open and two reads of /api/snapshot built the snapshot %d times, want once", got)
	}
}
