package lock

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// takerDirVariable makes this test binary another process that takes
// takerLock in the state folder it names, and takerHoldVariable says for how
// long each time.
const (
	takerDirVariable  = "CFO_LOCK_TEST_TAKER_DIR"
	takerHoldVariable = "CFO_LOCK_TEST_TAKER_HOLD"
	takerLock         = ".wake-queue.lock"
)

func TestMain(m *testing.M) {
	if dir := os.Getenv(takerDirVariable); dir != "" {
		os.Exit(takeBackToBack(dir))
	}
	os.Exit(m.Run())
}

// takeBackToBack is another process that takes the lock as a fleet command
// does, holds it, lets it go and takes it again at once, until the folder
// holds a file named stop. It leaves a file named taken once it holds the
// lock for the first time.
func takeBackToBack(dir string) int {
	hold, err := time.ParseDuration(os.Getenv(takerHoldVariable))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "stop")); err == nil {
			return 0
		}
		if _, err := AcquireNamedOwnerWithin(dir, takerLock, os.Getpid(), "wake", time.Minute); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := os.WriteFile(filepath.Join(dir, "taken"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		time.Sleep(hold)
		if err := ReleaseNamed(dir, takerLock); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
}

// startTaker starts another process that takes takerLock in dir back to back,
// holding it for hold each time, and returns once it holds the lock.
func startTaker(t *testing.T, dir string, hold time.Duration) {
	t.Helper()
	taker := exec.Command(os.Args[0])
	taker.Env = append(os.Environ(), takerDirVariable+"="+dir, takerHoldVariable+"="+hold.String())
	taker.Stderr = os.Stderr
	if err := taker.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(dir, "stop"), nil, 0o644)
		_ = taker.Process.Kill()
		_ = taker.Wait()
	})
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(dir, "taken")); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the other process did not take the lock within 30 s")
		}
	}
}

// On 2026-10-09 the CFO's acknowledgement gave up after 5.1 s, in 2 runs of
// 2, beside a process that filed one notify after another. The lock was free
// between every two notifies, and the waiter, which looked for it again only
// every so often, never looked at a free moment. A waiter queues: it is next
// after the process that held the lock when it asked, however fast that
// process asks again.
func TestAWaiterGetsTheLockFromAProcessThatTakesItBackToBack(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	startTaker(t, dir, 150*time.Millisecond)

	// Act
	began := time.Now()
	_, err := AcquireNamedOwnerWithin(dir, takerLock, os.Getpid(), "wake", 5*time.Second)
	waited := time.Since(began)

	// Assert
	if err != nil {
		t.Fatalf("the waiter was refused after %s beside a process that takes the lock back to back: %v", waited.Round(time.Millisecond), err)
	}
	if err := ReleaseNamed(dir, takerLock); err != nil {
		t.Errorf("the waiter could not let the lock go: %v", err)
	}
}
