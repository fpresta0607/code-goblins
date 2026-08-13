package watch

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestWaitSeesFileWrite(t *testing.T) {
	dir := t.TempDir()
	w, err := NewDirWaiter(dir)
	if err != nil {
		t.Fatalf("NewDirWaiter: %v", err)
	}
	defer w.Close()

	go func() {
		time.Sleep(100 * time.Millisecond)
		os.WriteFile(filepath.Join(dir, "a.status"), []byte("x"), 0o644)
	}()

	start := time.Now()
	got := w.Wait(5 * time.Second)
	elapsed := time.Since(start)

	if !got {
		t.Fatalf("Wait returned false, want true (file write should have signaled)")
	}
	if elapsed >= 3*time.Second {
		t.Fatalf("Wait took %v, want well under 3s", elapsed)
	}
}

func TestWaitTimesOutQuietly(t *testing.T) {
	dir := t.TempDir()
	w, err := NewDirWaiter(dir)
	if err != nil {
		t.Fatalf("NewDirWaiter: %v", err)
	}
	defer w.Close()

	start := time.Now()
	got := w.Wait(200 * time.Millisecond)
	elapsed := time.Since(start)

	if got {
		t.Fatalf("Wait returned true, want false (untouched dir)")
	}
	if elapsed < 150*time.Millisecond {
		t.Fatalf("Wait took %v, want at least 150ms", elapsed)
	}
}

func TestBreakerDegradesAfterThreeFailures(t *testing.T) {
	dir := t.TempDir()
	w, err := NewDirWaiter(dir)
	if err != nil {
		t.Fatalf("NewDirWaiter: %v", err)
	}
	w.Close()

	for i := 0; i < 3; i++ {
		if got := w.Wait(10 * time.Millisecond); got {
			t.Fatalf("call %d: Wait returned true on a closed waiter, want false", i+1)
		}
	}
	if !w.Degraded() {
		t.Fatalf("after three failures, Degraded() = false, want true")
	}

	start := time.Now()
	got := w.Wait(50 * time.Millisecond)
	elapsed := time.Since(start)
	if got {
		t.Fatalf("fourth call returned true, want false")
	}
	if elapsed < 40*time.Millisecond {
		t.Fatalf("degraded call took %v, want it to sleep out the full timeout (~50ms)", elapsed)
	}
}

func TestConfigFromEnvWiresWaiter(t *testing.T) {
	existing := t.TempDir()
	cfg := ConfigFromEnv(home.Home{State: existing})
	if cfg.WaitEvent == nil {
		t.Fatalf("ConfigFromEnv with an existing state dir: WaitEvent = nil, want non-nil")
	}
	// A wired WaitEvent with a nil Cleanup is the exact pairing defect the
	// brief was written to prevent (watch.Run defers Cleanup, so a nil one
	// leaks the waiter's handles). Assert it directly rather than only
	// calling Cleanup when present, which would let that pairing defect
	// pass silently.
	if cfg.Cleanup == nil {
		t.Fatalf("ConfigFromEnv with an existing state dir: Cleanup = nil, want non-nil")
	}
	cfg.Cleanup()

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	cfg2 := ConfigFromEnv(home.Home{State: missing})
	if cfg2.WaitEvent != nil {
		t.Fatalf("ConfigFromEnv with a missing state dir: WaitEvent non-nil, want nil")
	}
	if cfg2.Cleanup != nil {
		t.Fatalf("ConfigFromEnv with a missing state dir: Cleanup non-nil, want nil")
	}
}

// TestWaitTimeoutsLeaveNothingOutstanding asserts every call returns false
// on an untouched dir, which a stray AV or indexer touch on some unrelated
// file could in principle flake: before the content filter, ANY completed
// read returned true regardless of what it named. The filter added for C1
// (Wait now returns true only for a record naming *.status or
// *.turn-ended) closes that gap: a stray touch on an unrelated filename no
// longer satisfies Wait, so this test no longer depends on the temp dir
// staying completely untouched by anything outside the test itself.
func TestWaitTimeoutsLeaveNothingOutstanding(t *testing.T) {
	dir := t.TempDir()
	w, err := NewDirWaiter(dir)
	if err != nil {
		t.Fatalf("NewDirWaiter: %v", err)
	}

	for i := 0; i < 50; i++ {
		if got := w.Wait(10 * time.Millisecond); got {
			t.Fatalf("call %d: Wait returned true on an untouched dir, want false", i+1)
		}
	}
	if w.Degraded() {
		t.Fatalf("after 50 quiet timeouts, Degraded() = true, want false (timeouts must not score strikes)")
	}
	w.Close()
}

// TestRunDoesNotFreeRunOnItsOwnBeatTouch is the integration regression test
// for C1: watch.Run touches state/.last-watcher-beat at the top of every
// iteration, on the same directory a real ConfigFromEnv-wired DirWaiter
// watches under FILE_NOTIFY_CHANGE_LAST_WRITE. Before the content filter,
// the kernel buffered that self-inflicted touch on the handle and handed it
// back on the very next read, so Wait returned true in 0ms starting from the
// second iteration and Run free-ran at full CPU instead of waiting out
// CFO_POLL. This test proves the fix by running Run for real, with a real
// waiter, and counting how many times the beat file actually changes over a
// fixed wall-clock window: at CFO_POLL=2s, a healthy loop touches it about
// twice in four seconds, while a free-running loop touches it thousands of
// times in the same window.
func TestRunDoesNotFreeRunOnItsOwnBeatTouch(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CFO_POLL", "2")
	t.Setenv("CFO_HEARTBEAT", "3600")
	t.Setenv("CFO_HEARTBEAT_MAX", "3600")
	t.Setenv("CFO_SIGNAL_GRACE", "1")

	cfg := ConfigFromEnv(home.Home{State: stateDir})
	if cfg.WaitEvent == nil {
		t.Fatalf("ConfigFromEnv did not wire a real waiter on an existing state dir; this test requires the real notification path to be exercised")
	}

	// A genuinely alive, foreign PID is needed to simulate a successor
	// stealing the singleton later, exactly as
	// TestRunReturnsQuietlyWhenSingletonStolen does: a throwaway child
	// process stands in for a live successor's identity.
	cmd := exec.Command("cmd", "/c", "ping -n 30 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	done := make(chan struct{})
	go func() {
		Run(cfg)
		close(done)
	}()

	beatPath := filepath.Join(stateDir, watcherBeatFile)
	beatDeadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(beatPath); err == nil {
			break
		}
		if time.Now().After(beatDeadline) {
			t.Fatalf("watcher never touched its beat file")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Guard the touchCount upper-bound check below against a vacuous pass: a
	// Run that errored out right after its first beat touch would score a
	// low touchCount and satisfy "at most ~2-3" without ever exercising the
	// steady-state wait this test means to observe. Confirming the loop is
	// still alive going into the observation window, combined with the
	// touchCount >= 2 lower bound below, closes that gap.
	select {
	case <-done:
		t.Fatalf("Run exited before the observation window began; the touchCount checks below would be vacuous")
	default:
	}

	var lastMod time.Time
	touchCount := 0
	observeUntil := time.Now().Add(4 * time.Second)
	for time.Now().Before(observeUntil) {
		if fi, err := os.Stat(beatPath); err == nil && !fi.ModTime().Equal(lastMod) {
			touchCount++
			lastMod = fi.ModTime()
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Steal the singleton so Run notices on its next ownership check and
	// returns quietly, rather than leaking the goroutine for the rest of
	// this test binary's life.
	foreign := liveForeignInfo(t, cmd.Process.Pid)
	data, err := json.Marshal(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, watchLockName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatalf("Run did not exit after its singleton was stolen")
	}

	if touchCount > 3 {
		t.Fatalf("beat file touched %d times in ~4s at CFO_POLL=2s, want at most ~2-3: the watcher is free-running on its own beat touch (C1 regression)", touchCount)
	}
	if touchCount < 2 {
		t.Fatalf("beat file touched %d times in ~4s at CFO_POLL=2s, want at least 2: a Run that errored out early (or otherwise never reached steady-state polling) would vacuously pass the upper-bound check above", touchCount)
	}
}
