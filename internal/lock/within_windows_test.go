package lock

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
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

	// Act
	began := time.Now()
	_, err = AcquireNamedOwnerWithin(dir, ".wake-queue.lock", os.Getpid(), "wake", wait)
	waited := time.Since(began)

	// Assert
	if err == nil {
		t.Fatal("AcquireNamedOwnerWithin took a lock a live holder holds")
	}
	// stall-accepted: it times the acquire, and the wait it rules out, the 5 s a read of a held record waits by default, is too short for a bound past a stall to tell apart.
	if waited > wait+2*time.Second {
		t.Errorf("the acquire waited %s while the record was held, want it to give up near its wait of %s: %v", waited, wait, err)
	}
}

// A pause between two looks that runs past the wait, as one can on a loaded
// machine, leaves the next look no time, and that look ends with the
// deadline: the acquire still returns the holder's ErrHeld. On a CI runner
// (#1393) TestAcquireNamedOwnerWithinGivesUpOnALiveHolderAfterItsWait got
// "context deadline exceeded".
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
