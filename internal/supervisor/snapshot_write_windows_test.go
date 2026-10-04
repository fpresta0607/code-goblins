package supervisor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAnEventStreamOpensWhileStoreWritesWaitForAFileReader(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	if _, err := store.claimAnnounced([]string{"first"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(store.path())
	if err != nil {
		t.Fatal(err)
	}
	var writers sync.WaitGroup
	release := sync.OnceFunc(func() { reader.Close() })
	t.Cleanup(func() {
		release()
		writers.Wait()
	})
	for i := range 16 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			if _, err := store.claimAnnounced([]string{fmt.Sprintf("new-%d", i)}, nil, time.Now()); err != nil {
				t.Errorf("queued store write: %v", err)
			}
		}()
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		pending, err := filepath.Glob(filepath.Join(h.State, ".cfo-tmp-*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the writer never reached its file replacement")
		}
		time.Sleep(10 * time.Millisecond)
	}
	s := &Service{Store: store, Instance: "test-instance", subscribers: map[chan struct{}]struct{}{}, done: make(chan struct{})}
	handler := NewHTTP(s, "", nil)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	handler.Host = strings.TrimPrefix(server.URL, "http://")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	response, err := http.DefaultClient.Do(request)

	// Assert
	if err != nil {
		t.Fatalf("the stream returned no headers while store writes waited for a file reader: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream = %d, %s, want an event stream", response.StatusCode, response.Header.Get("Content-Type"))
	}
	if got := len(store.Snapshot().Announced); got != 1 {
		t.Fatalf("snapshot exposed %d announcements before they were saved, want one", got)
	}
	release()
	writers.Wait()
	if got := len(store.Snapshot().Announced); got != 17 {
		t.Fatalf("snapshot shows %d announcements after the writes finished, want 17", got)
	}
}
