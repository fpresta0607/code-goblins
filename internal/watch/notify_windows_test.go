package watch

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
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

// TestRunUsesNoLegacyBeatMarker proves Task 4 moved watcher liveness to the
// typed monitor heartbeat record instead of rewriting a shell-era marker.
func TestRunUsesNoLegacyBeatMarker(t *testing.T) {
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

	// The watcher writes the typed heartbeat on its first cycle. That write
	// races goroutine scheduling under a loaded test binary, so poll with a
	// deadline instead of a fixed sleep that flakes when the machine is busy.
	heartbeat, err := monitor.ReadHeartbeat(stateDir)
	for deadline := time.Now().Add(5 * time.Second); (err != nil || heartbeat.LastCycle.IsZero()) && time.Now().Before(deadline); {
		select {
		case <-done:
			t.Fatalf("Run exited before its first heartbeat")
		default:
		}
		time.Sleep(20 * time.Millisecond)
		heartbeat, err = monitor.ReadHeartbeat(stateDir)
	}
	if _, statErr := os.Stat(filepath.Join(stateDir, ".last-watcher-beat")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("legacy watcher beat marker exists: %v", statErr)
	}
	if err != nil || heartbeat.LastCycle.IsZero() {
		t.Fatalf("typed heartbeat = %+v, %v; want recent LastCycle", heartbeat, err)
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

}
