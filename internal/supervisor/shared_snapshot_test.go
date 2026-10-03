package supervisor

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// openStream opens one board's event stream and reports each snapshot event
// it receives on the channel it returns.
func openStream(t *testing.T, ctx context.Context, url string) <-chan struct{} {
	t.Helper()
	request, _ := http.NewRequestWithContext(ctx, "GET", url+"/api/events", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	snapshots := make(chan struct{}, 64)
	go func() {
		reader := bufio.NewReader(response.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.TrimRight(line, "\n") == "event: snapshot" {
				snapshots <- struct{}{}
			}
		}
	}()
	return snapshots
}

// awaitSnapshot waits for a stream's next snapshot event.
func awaitSnapshot(t *testing.T, snapshots <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-snapshots:
	case <-time.After(10 * time.Second):
		t.Fatalf("no snapshot reached %s", what)
	}
}

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
	boards := []<-chan struct{}{openStream(t, ctx, server.URL), openStream(t, ctx, server.URL), openStream(t, ctx, server.URL)}
	for _, board := range boards {
		awaitSnapshot(t, board, "a board as it opened")
	}
	builds.Store(0)

	// Act: one change, then the desktop app reads the snapshot twice.
	s.publish(nil)
	for _, board := range boards {
		awaitSnapshot(t, board, "a board after the change")
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
