package lock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/awake"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// A wait for a live holder ends at its own deadline even while another
// process holds the holder's record open sharing nothing, as a scan of a
// record just written does: the record's reads stop waiting it out at the
// deadline too. On a CI runner one such hold made a 300 ms wait take 6 s.
func TestAcquireNamedOwnerWithinEndsAtItsWaitWhileTheRecordIsHeld(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	holder := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
	if _, err := AcquireNamedOwner(dir, ".wake-queue.lock", holder.Process.Pid, "wake"); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(filepath.Join(dir, ".wake-queue.lock"))
	if err != nil {
		t.Fatal(err)
	}
	held, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(held) })
	const wait = 300 * time.Millisecond
	stagedBefore := fsx.Stages()

	// Act
	began := time.Now()
	_, err = AcquireNamedOwnerWithin(dir, ".wake-queue.lock", os.Getpid(), "wake", wait)
	waited := time.Since(began)

	// Assert: the record went unread, but it is there, so the lock is held.
	if !errors.Is(err, ErrHeld) {
		t.Fatalf("AcquireNamedOwnerWithin over a held record nobody could read = %v, want ErrHeld", err)
	}
	if staged := fsx.Stages() - stagedBefore; staged != 0 {
		t.Errorf("the waiter staged %d record(s) beside a record it could not open, want it to find the record there without one", staged)
	}
	// stall-accepted: it times the acquire, and the wait it rules out, the 5 s a read of a held record waits by default, is too short for a bound past a stall to tell apart.
	if waited > wait+2*time.Second {
		t.Errorf("the acquire waited %s while the record was held, want it to give up near its wait of %s: %v", waited, wait, err)
	}
}

// A look at a held lock has no use for a record of its own, and staging one is
// the slow part of a write: it makes a new file, which a virus scanner reads.
// A loaded machine spent the whole wait on that file, so the look ended with
// the deadline before it saw the record, and the waiter reported "context
// deadline exceeded" where the holder's ErrHeld belongs (#1878).
func TestALookAtAHeldLockSpendsNoWaitOnAFileOfItsOwn(t *testing.T) {
	// Arrange: a look that writes a record of its own stalls there past the wait.
	dir := t.TempDir()
	holder := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
	if _, err := AcquireNamedOwner(dir, ".wake-queue.lock", holder.Process.Pid, "wake"); err != nil {
		t.Fatal(err)
	}
	const wait = 300 * time.Millisecond
	publishing = func() { time.Sleep(2 * wait) }
	t.Cleanup(func() { publishing = func() {} })
	stagedBefore := fsx.Stages()

	// Act
	_, err := AcquireNamedOwnerWithin(dir, ".wake-queue.lock", os.Getpid(), "wake", wait)

	// Assert
	if !errors.Is(err, ErrHeld) {
		t.Fatalf("AcquireNamedOwnerWithin over a live holder = %v, want ErrHeld", err)
	}
	if staged := fsx.Stages() - stagedBefore; staged != 0 {
		t.Errorf("the waiter staged %d record(s) at a lock it found held, want none", staged)
	}
}

// A read the deadline ended says nothing about the record it never read. A
// look that took that for an unreadable record would call a live holder's
// record a crash orphan once a few such reads had passed, and remove it.
func TestALookPastItsDeadlineLeavesALiveHoldersRecord(t *testing.T) {
	// Arrange
	dir, name := t.TempDir(), ".wake-queue.lock"
	holder := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
	if _, err := AcquireNamedOwner(dir, name, holder.Process.Pid, "wake"); err != nil {
		t.Fatal(err)
	}
	self, status := ownerInfo(os.Getpid(), "wake")
	if status == statusDead {
		t.Fatal("current process unexpectedly dead")
	}
	look, cancel := awake.WithTimeout(context.Background(), 0)
	defer cancel()

	// Act
	_, err := acquire(look, dir, name, self, true, false)

	// Assert
	if !errors.Is(err, ErrHeld) {
		t.Errorf("a look past its deadline at a held lock = %v, want ErrHeld", err)
	}
	if recorded, readErr := ReadNamed(dir, name); readErr != nil || recorded.PID != holder.Process.Pid {
		t.Errorf("the lock records %+v, %v after the look, want the live holder, pid %d", recorded, readErr, holder.Process.Pid)
	}
}

// A look slowed across its deadline ends with the deadline, not with the
// holder, so the holder the last look found is the answer: a caller told the
// lock is held tries again, and this lock was held for nearly the whole wait.
func TestALookThatRunsOutOfTimeAfterFindingAHolderReturnsThatHoldersErrHeld(t *testing.T) {
	// Arrange: the holder lets go at the first pause, and the next look finds
	// the record gone and outlasts its deadline writing its own.
	dir, name := t.TempDir(), ".wake-queue.lock"
	holder := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
	if _, err := AcquireNamedOwner(dir, name, holder.Process.Pid, "wake"); err != nil {
		t.Fatal(err)
	}
	const wait = 300 * time.Millisecond
	sleep = func(time.Duration) {
		if err := fsx.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Error(err)
		}
	}
	t.Cleanup(func() { sleep = time.Sleep })
	isWriting := false
	publishing = func() {
		isWriting = true
		time.Sleep(2 * wait)
	}
	t.Cleanup(func() { publishing = func() {} })

	// Act
	_, err := AcquireNamedOwnerWithin(dir, name, os.Getpid(), "wake", wait)

	// Assert
	if !isWriting {
		t.Fatalf("the premise failed: no look went on to write a record (%v)", err)
	}
	if !errors.Is(err, ErrHeld) {
		t.Fatalf("a look that ran out of time after one found the holder = %v, want ErrHeld", err)
	}
}

// A pause between two looks that runs past the wait, as one can on a loaded
// machine, spends the wait, and the next look still has a deadline of its own
// to read the record in: the acquire returns the ErrHeld that names the holder.
// On a CI runner (#1393)
// TestAcquireNamedOwnerWithinGivesUpOnALiveHolderAfterItsWait got "context
// deadline exceeded".
func TestAPauseThatRunsPastTheWaitStillReturnsTheHoldersErrHeld(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	holder := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
	if _, err := AcquireNamedOwner(dir, ".wake-queue.lock", holder.Process.Pid, "wake"); err != nil {
		t.Fatal(err)
	}
	const wait = 300 * time.Millisecond
	pauses := 0
	sleep = func(pause time.Duration) {
		pauses++
		time.Sleep(pause + wait)
	}
	t.Cleanup(func() { sleep = time.Sleep })

	// Act
	_, err := AcquireNamedOwnerWithin(dir, ".wake-queue.lock", os.Getpid(), "wake", wait)

	// Assert
	if pauses == 0 {
		t.Fatalf("the premise failed: the acquire never paused between two looks (%v)", err)
	}
	if !errors.Is(err, ErrHeld) || !strings.Contains(err.Error(), fmt.Sprintf("pid %d", holder.Process.Pid)) {
		t.Fatalf("AcquireNamedOwnerWithin after a pause past its wait = %v, want the ErrHeld of the holder, pid %d", err, holder.Process.Pid)
	}
}

// At the head of the line the record is the caller's to take, so it takes
// one look at it however late the line let it through. A machine that stalls
// can spend the whole wait in a line with nobody ahead: on a CI runner (train
// PR 1043) the monitor's wake lock was given up as "context deadline
// exceeded" before its free record was ever tried, and the pause test of the
// same run lost the same ten seconds.
func TestAWaiterAtTheHeadOfTheLineLooksAtTheRecordHoweverLate(t *testing.T) {
	// Arrange: the caller stalls once it is at the head of the line, past
	// its whole wait.
	dir := t.TempDir()
	const wait = 100 * time.Millisecond
	stalled := inLine
	inLine = func() { time.Sleep(2 * wait) }
	t.Cleanup(func() { inLine = stalled })

	// Act
	info, err := AcquireNamedOwnerWithin(dir, ".wake-queue.lock", os.Getpid(), "wake", wait)

	// Assert
	if err != nil || info == nil || info.PID != os.Getpid() {
		t.Fatalf("acquire = %+v, %v, want the free lock taken by the caller the line let through late", info, err)
	}
	if err := ReleaseNamed(dir, ".wake-queue.lock"); err != nil {
		t.Fatal(err)
	}
}
