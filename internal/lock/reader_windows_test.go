package lock

import (
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
)

// A reader that never lets the dead holder's record go is waited on only a
// bounded while: the acquire then fails with the sharing violation itself.
func TestAcquireGivesUpOnAReaderThatNeverLetsADeadHoldersRecordGo(t *testing.T) {
	dir := t.TempDir()
	holdOpen(t, deadHoldersRecord(t, dir, ".watch.lock"))
	began := time.Now()

	_, err := AcquireNamedOwner(dir, ".watch.lock", os.Getpid(), "watch")

	if !errors.Is(err, syscall.Errno(32)) {
		t.Fatalf("AcquireNamedOwner = %v, want the sharing violation", err)
	}
	if waited := time.Since(began); waited > time.Minute {
		t.Errorf("the acquire waited %s, want it bounded", waited)
	}
}
