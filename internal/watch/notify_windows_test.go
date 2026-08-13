package watch

import (
	"os"
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
	if cfg.Cleanup != nil {
		cfg.Cleanup()
	}

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	cfg2 := ConfigFromEnv(home.Home{State: missing})
	if cfg2.WaitEvent != nil {
		t.Fatalf("ConfigFromEnv with a missing state dir: WaitEvent non-nil, want nil")
	}
	if cfg2.Cleanup != nil {
		t.Fatalf("ConfigFromEnv with a missing state dir: Cleanup non-nil, want nil")
	}
}

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
