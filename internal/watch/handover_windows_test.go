package watch

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
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

// slowProbe stands for a slow cycle: each inspection runs until its context
// ends, as a Herdr read on a starved machine does.
type slowProbe struct{ inspecting chan struct{} }

func (p slowProbe) Inspect(ctx context.Context, _ state.TaskMeta) (monitor.EndpointSample, error) {
	select {
	case p.inspecting <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return monitor.EndpointSample{}, ctx.Err()
}

// A watcher in the middle of a slow cycle, a monitor scan that would run to
// its three-minute budget, still yields as soon as a serve asks: it answers
// the serve, naming itself and the serve, so the serve knows it is yielding
// and does not end it, and stops the scan.
func TestAWatcherInASlowCycleYieldsAtOnceAndAnswersTheServe(t *testing.T) {
	dir := t.TempDir()
	cfg := baseConfig(dir)
	cfg.Monitor = monitoringService(t, dir, "g1")
	probe := slowProbe{inspecting: make(chan struct{}, 1)}
	cfg.Monitor.Probe = probe
	type result struct {
		reason string
		err    error
	}
	returned := make(chan result, 1)
	go func() {
		reason, err := Run(cfg)
		returned <- result{reason, err}
	}()
	select {
	case <-probe.inspecting:
	case <-time.After(10 * time.Second):
		t.Fatal("the watcher never started its scan")
	}

	serve := startServeStandIn(t)
	asked := time.Now()
	askForTheLock(t, dir, serve)

	select {
	case got := <-returned:
		if got.err != nil || got.reason != "" {
			t.Fatalf("Run = %q, %v; want it to yield quietly", got.reason, got.err)
		}
		if waited := time.Since(asked); waited > 3*time.Second {
			t.Errorf("the watcher yielded %s after the request, want at once, not when its scan ended", waited)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the watcher kept the lock a serve asked for until its scan ended")
	}
	if lock.HeldByNamed(dir, ".watch.lock", os.Getpid()) {
		t.Error("the watcher still holds the lock after yielding it")
	}
	data, err := os.ReadFile(filepath.Join(dir, HandoverAckName))
	if err != nil {
		t.Fatalf("the watcher did not answer the serve: %v", err)
	}
	var ack struct {
		WatcherPID   int       `json:"watcher_pid"`
		WatcherStart    time.Time `json:"watcher_start"`
		WatcherHostname string    `json:"watcher_hostname"`
		ServePID        int       `json:"serve_pid"`
		ServeStart      time.Time `json:"serve_start"`
	}
	if err := json.Unmarshal(data, &ack); err != nil {
		t.Fatal(err)
	}
	watcherStart, _ := proc.StartTime(os.Getpid())
	serveStart, _ := proc.StartTime(serve)
	hostname, _ := os.Hostname()
	if ack.WatcherPID != os.Getpid() || !ack.WatcherStart.Equal(watcherStart) || ack.WatcherHostname != hostname || ack.ServePID != serve || !ack.ServeStart.Equal(serveStart) {
		t.Errorf("the answer %+v does not name this watcher (pid %d, %s) and the serve (pid %d, %s)", ack, os.Getpid(), watcherStart, serve, serveStart)
	}
}
