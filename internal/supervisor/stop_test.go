package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
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

// A stop request ends the supervisor within a moment whatever its cycle is
// doing: the request cancels the cycle in progress, which the once-a-minute
// reconcile can hold for minutes, rather than waiting for it to finish and for
// the loop to come round. An update that asked a busy supervisor to stop used
// to wait its whole stop wait and then end it mid-cycle.
func TestAStopRequestEndsTheSupervisorWhileItsCycleIsBusy(t *testing.T) {
	// Arrange: a reconcile that runs until the supervisor's work is cancelled.
	_, h := testStore(t)
	busy := make(chan struct{})
	var once sync.Once
	s, err := Start(context.Background(), h, Options{Reconcile: func(ctx context.Context) error {
		once.Do(func() { close(busy) })
		<-ctx.Done()
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	select {
	case <-busy:
	case <-time.After(10 * time.Second):
		t.Fatal("the supervisor never reached its reconcile")
	}

	// Act
	if err := RequestStop(h.State, os.Getpid()); err != nil {
		t.Fatal(err)
	}

	// Assert
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the supervisor still runs 5 s after a stop request, with its cycle busy")
	}
}

// A stop request that names a successor, as an update's does, gets the
// watcher lock handed to it as the supervisor ends: the lock is never free
// between the two, so nothing else, a supervisor a goblins starts or the
// CFO's Stop hook, can take it in the moment the supervisor used to let go.
func TestAStopRequestNamingASuccessorHandsItTheWatcherLock(t *testing.T) {
	// Arrange
	_, h := testStore(t)
	s, err := Start(context.Background(), h, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	successor := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := successor.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = successor.Process.Kill(); _ = successor.Wait() })
	request, err := json.Marshal(map[string]int{"pid": os.Getpid(), "successor": successor.Process.Pid})
	if err != nil {
		t.Fatal(err)
	}

	// Act
	if err := os.WriteFile(stopRequestPath(h.State), request, 0o600); err != nil {
		t.Fatal(err)
	}

	// Assert
	select {
	case <-s.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the supervisor did not stop for a request naming its pid")
	}
	holder, err := lock.ReadNamed(h.State, watchLock)
	if err != nil || holder.PID != successor.Process.Pid || !holder.Alive() {
		t.Fatalf("the watcher lock names %+v (%v), want it handed to the successor pid %d", holder, err, successor.Process.Pid)
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
