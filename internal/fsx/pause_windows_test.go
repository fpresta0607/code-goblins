package fsx

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/awake"
	"github.com/fpresta0607/code-goblins/internal/awake/awaketest"
)

// A machine that pauses stops every process at once, the one holding a file
// with the one waiting it out, and the wall clock runs on through the pause.
// A replace that held its wait to the wall clock found its budget gone once
// the machine ran again, though the holder had had no time to let go
// (#1102). The pause spends none of the wait, so the replace waits out a
// holder that still needs its time once the machine runs again.
func TestAReplaceWaitsOutAHolderAcrossAPauseOfTheWholeMachine(t *testing.T) {
	if !awaketest.IsPausedProcess() {
		awaketest.Across(t)
		return
	}
	// Arrange: the pause falls once the replace waits.
	path := filepath.Join(t.TempDir(), ".supervisor.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var letGo sync.Once
	release := func() { letGo.Do(func() { _ = reader.Close() }) }
	t.Cleanup(release)
	held, cancelHold := awake.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelHold()
	context.AfterFunc(held, release)
	budget := transientBudget
	transientBudget = time.Second
	var isReady bool
	sleep = func(wait time.Duration) {
		if !isReady {
			isReady = true
			awaketest.Ready()
		}
		time.Sleep(wait)
	}
	t.Cleanup(func() { transientBudget, sleep = budget, time.Sleep })

	// Act
	err = AtomicWriteFile(path, []byte("new"))

	// Assert
	if err != nil {
		t.Fatalf("the replace gave up across a pause of %s on a holder that let go %s of running time after it began: %v", awaketest.Length, 300*time.Millisecond, err)
	}
	if got, err := ReadFile(path); err != nil || string(got) != "new" {
		t.Errorf("ReadFile after the replace = %q, %v, want %q", got, err, "new")
	}
}
