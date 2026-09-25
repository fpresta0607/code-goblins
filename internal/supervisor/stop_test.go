package supervisor

import (
	"context"
	"os"
	"testing"
	"time"
)

// A supervisor goblins started in the background has no terminal for Ctrl-C
// to reach, so goblins stop asks it through a request naming its pid. A
// request naming another pid is left over from a supervisor that already
// ended: it is removed and stops nothing.
func TestAStopRequestStopsOnlyTheSupervisorItNames(t *testing.T) {
	_, h := testStore(t)
	s, err := Start(context.Background(), h, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := RequestStop(h.State, os.Getpid()+1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
		t.Fatal("a request naming another pid stopped the supervisor")
	case <-time.After(5 * time.Second):
	}
	if _, err := os.Stat(stopRequestPath(h.State)); !os.IsNotExist(err) {
		t.Fatalf("the leftover request was kept: %v", err)
	}

	if err := RequestStop(h.State, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the supervisor did not stop for a request naming its pid")
	}
	if _, err := os.Stat(stopRequestPath(h.State)); !os.IsNotExist(err) {
		t.Fatalf("the honoured request was kept for the next supervisor: %v", err)
	}
}

// A request left from an earlier supervisor can name the pid a new one
// reuses. The new supervisor removes it at start, so it keeps running.
func TestARequestLeftBeforeStartDoesNotStopTheSupervisor(t *testing.T) {
	_, h := testStore(t)
	if err := RequestStop(h.State, os.Getpid()); err != nil {
		t.Fatal(err)
	}

	s, err := Start(context.Background(), h, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	select {
	case <-s.Done():
		t.Fatal("a request left before start stopped the supervisor")
	case <-time.After(5 * time.Second):
	}
	if _, err := os.Stat(stopRequestPath(h.State)); !os.IsNotExist(err) {
		t.Fatalf("the leftover request was kept: %v", err)
	}
}
