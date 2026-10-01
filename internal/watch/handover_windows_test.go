package watch

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// startServeStandIn starts a live process to stand for a serve asking for
// the lock.
func startServeStandIn(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("cmd", "/c", "ping -n 60 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	return cmd.Process.Pid
}

// askForTheLock writes the request a starting serve, here pid, writes.
func askForTheLock(t *testing.T, dir string, pid int) {
	t.Helper()
	start, ok := proc.StartTime(pid)
	if !ok {
		t.Fatalf("read pid %d's start time", pid)
	}
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"pid": pid, "start": start.UTC(), "hostname": hostname})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "serve.handover"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, within time.Duration, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(within); !done(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within %s", what, within)
		}
	}
}

// On 2026-10-01 an install stopped serve, the CFO's Stop hook took the
// watcher lock in the gap, and the new serve refused to start until the
// hook's window ended. A watcher yields the lock as soon as a serve asks for
// it: the request wakes its wait, so it does not sit out its poll.
func TestAWatcherYieldsTheLockAtOnceToAServeThatAsksForIt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CFO_POLL", "300")
	cfg := ConfigFromEnv(home.Home{Root: filepath.Dir(dir), State: dir})
	cfg.Monitor, cfg.Reap, cfg.FileEvery = nil, nil, 0
	type result struct {
		reason string
		err    error
	}
	returned := make(chan result, 1)
	go func() {
		reason, err := Run(cfg)
		returned <- result{reason, err}
	}()
	eventually(t, 10*time.Second, "the watcher taking the lock", func() bool { return lock.HeldByNamed(dir, ".watch.lock", os.Getpid()) })

	asked := time.Now()
	askForTheLock(t, dir, startServeStandIn(t))

	select {
	case got := <-returned:
		if got.err != nil || got.reason != "" {
			t.Fatalf("Run = %q, %v; want it to yield quietly", got.reason, got.err)
		}
		if waited := time.Since(asked); waited > 5*time.Second {
			t.Errorf("the watcher yielded %s after the request, want at once, not after its five-minute poll", waited)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the watcher kept the lock a serve asked for")
	}
	if lock.HeldByNamed(dir, ".watch.lock", os.Getpid()) {
		t.Error("the watcher still holds the lock after yielding it")
	}
}

// While a serve waits for the lock, a watcher starting up, the Stop hook
// re-arming in the gap of an install, does not take it from under it; once
// the request is gone it takes the lock as before.
func TestAWatcherTakesNoLockWhileAServeWaitsForIt(t *testing.T) {
	dir := t.TempDir()
	serve := startServeStandIn(t)
	askForTheLock(t, dir, serve)
	returned := make(chan error, 1)
	go func() {
		_, err := Run(baseConfig(dir))
		returned <- err
	}()

	time.Sleep(time.Second)
	if lock.HeldByNamed(dir, ".watch.lock", os.Getpid()) {
		t.Fatal("a watcher took the lock a serve was waiting for")
	}

	if err := os.Remove(filepath.Join(dir, "serve.handover")); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "the watcher taking the free lock", func() bool { return lock.HeldByNamed(dir, ".watch.lock", os.Getpid()) })
	askForTheLock(t, dir, serve)
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the watcher kept the lock a serve asked for")
	}
}

// A request from a serve that has ended, or from a pid now reused by another
// program, is not honoured: the watcher takes the lock.
func TestAWatcherIgnoresARequestFromAServeThatIsGone(t *testing.T) {
	dir := t.TempDir()
	exited := exec.Command("cmd", "/c", "exit 0")
	if err := exited.Run(); err != nil {
		t.Fatal(err)
	}
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"pid": exited.ProcessState.Pid(), "start": time.Now().Add(-time.Minute).UTC(), "hostname": hostname})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "serve.handover"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = Run(baseConfig(dir)) }()

	eventually(t, 3*time.Second, "the watcher taking the lock", func() bool { return lock.HeldByNamed(dir, ".watch.lock", os.Getpid()) })
	askForTheLock(t, dir, startServeStandIn(t))
	eventually(t, 5*time.Second, "the watcher yielding", func() bool { return !lock.HeldByNamed(dir, ".watch.lock", os.Getpid()) })
}
