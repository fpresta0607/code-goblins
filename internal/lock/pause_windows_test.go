package lock

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/awake"
	"github.com/fpresta0607/code-goblins/internal/awake/awaketest"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// A machine that pauses stops the lock's holder with its waiter, and the
// wall clock runs on through the pause. A waiter that held its wait to the
// wall clock found it gone once the machine ran again and refused the
// notify it waited for, though the holder had had no time to let go
// (#1048, #1102). The pause spends none of the wait, so the waiter gets the
// lock from a holder that still needs its time once the machine runs again.
// The holder is a live process that holds the record alone, as a build
// before the line does, so the waiter looks for the record again and again.
func TestAWaiterGetsTheLockAcrossAPauseOfTheWholeMachine(t *testing.T) {
	if !awaketest.IsPausedProcess() {
		awaketest.Across(t)
		return
	}
	// Arrange: the pause falls once the waiter has found the lock held, and
	// only then does the holder's time start, so a late first look on a
	// loaded machine no longer lets the hold run out before the waiter waits:
	// a hold started before the waiter lined up once did, and the waiter took
	// the lock without ever waiting (#1877). The wait of 2s leaves a slow
	// first look room and still runs out in the pause if held to the wall
	// clock.
	dir := t.TempDir()
	if _, err := AcquireNamedOwner(dir, takerLock, os.Getppid(), "holder"); err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	var isReady bool
	sleep = func(pause time.Duration) {
		if !isReady {
			isReady = true
			held, cancelHold := awake.WithTimeout(context.Background(), 300*time.Millisecond)
			t.Cleanup(cancelHold)
			context.AfterFunc(held, func() { released <- fsx.Remove(filepath.Join(dir, takerLock)) })
			awaketest.Ready()
		}
		time.Sleep(pause)
	}
	t.Cleanup(func() { sleep = time.Sleep })

	// Act
	info, err := AcquireNamedOwnerWithin(dir, takerLock, os.Getpid(), "waiter", 2*time.Second)

	// Assert
	if !isReady {
		t.Fatalf("the waiter got %+v, %v without finding the lock held; want it to wait for the holder", info, err)
	}
	if err != nil || info.PID != os.Getpid() {
		t.Fatalf("the waiter got %+v, %v across a pause of %s from a holder that let go %s of running time after it began; want the lock", info, err, awaketest.Length, 300*time.Millisecond)
	}
	if err := <-released; err != nil {
		t.Errorf("the holder could not let the lock go: %v", err)
	}
	if err := ReleaseNamed(dir, takerLock); err != nil {
		t.Errorf("the waiter could not let the lock go: %v", err)
	}
}
