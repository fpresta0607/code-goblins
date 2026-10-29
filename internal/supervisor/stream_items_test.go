package supervisor

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// The Overlord, 2026-10-02: an item the CFO answers "hangs around for a stale
// second or two". Measured that day: the live board took 3 to 13 seconds to
// build one snapshot, which reads the fleet from several hundred files, and
// the Command Center's items reached the board only inside it. They live in
// the store's memory, so the stream sends their change alone the moment one
// changes, and a snapshot built before the change carries them as they are
// when it is sent, never as they were when its build began.
func TestTheStreamSendsTheCommandCentersItemsAheadOfASnapshotStillBeingBuilt(t *testing.T) {
	// Arrange: a board with one question waiting, whose next snapshot takes
	// until the test lets it finish and was begun before the question closed.
	store, _ := testStore(t)
	identity := strings.Repeat("c", 64)
	if err := store.acceptQuestion(Question{ID: "merge-the-train", Identity: identity, Text: "Merge it?", Options: []string{"Yes", "No"}, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: store, Instance: "test-instance", subscribers: map[chan struct{}]struct{}{}, done: make(chan struct{})}
	handler := NewHTTP(s, "", nil)
	begun, finish := make(chan struct{}, 8), make(chan struct{})
	first := true
	s.buildSnapshot = func() (Snapshot, error) {
		snapshot, err := s.Snapshot()
		if first {
			first = false
			return snapshot, err
		}
		begun <- struct{}{}
		<-finish
		return snapshot, err
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	// A build still held when the test ends is let go before the server
	// closes, which waits for its stream.
	release := sync.OnceFunc(func() { close(finish) })
	defer release()
	handler.Host = strings.TrimPrefix(server.URL, "http://")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := openBoard(t, ctx, server.URL)
	if opened := nextEvent(t, events, "its first snapshot"); opened.name != "snapshot" || opened.data.Questions[0].Status != "pending" {
		t.Fatalf("the stream opened with %s and the question %+v, want a snapshot with it pending", opened.name, opened.data.Questions)
	}

	// Act: a snapshot build begins, and only then is the question dismissed.
	s.publish(nil)
	<-begun
	if _, err := store.Queue(Action{ID: "dismiss-1", Kind: "question_clear", QuestionID: "merge-the-train", Generation: identity}); err != nil {
		t.Fatal(err)
	}
	s.publish(nil)

	// Assert: the items arrive while that build is still running, and then
	// their revision.
	items := nextEvent(t, events, "the Command Center's items")
	if items.name != "change" || items.data.Instance != "test-instance" || len(items.data.Questions) != 1 || items.data.Questions[0].Status != "cleared" {
		t.Fatalf("the stream sent %s with the questions %+v before the snapshot was built, want a change of the items with the question cleared", items.name, items.data.Questions)
	}
	if len(items.data.Actions) != 1 || items.data.Actions[0].ID != "dismiss-1" {
		t.Errorf("the items carry the actions %+v, want the dismiss he sent", items.data.Actions)
	}
	revised := nextReading(t, events, "the items' revision")

	// Act: the build that began before the dismiss finishes.
	release()

	// Assert: its snapshot shows the question as it is now.
	built := nextReading(t, events, "the snapshot")
	if built.data.Questions[0].Status != "cleared" {
		t.Fatalf("the board then holds the question %s, want it still cleared", built.data.Questions[0].Status)
	}
	if built.data.Revision < revised.data.Revision {
		t.Errorf("the snapshot's revision %d is older than the items' %d, so the board would go back to an older one", built.data.Revision, revised.data.Revision)
	}
}
