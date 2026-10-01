package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/claudehook"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	taskstate "github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

// watchLockStandInVariable names the state directory this test binary,
// started as a stand-in, holds the watcher lock in, and watchLockRoleVariable
// as what: "serve" takes it as cfo serve does, over any watcher holding it,
// and keeps the heartbeat as serve's watcher does; "legacy" holds it as a
// Stop hook from before the handover, never reading a serve's request.
const (
	watchLockStandInVariable = "CFO_TEST_WATCH_LOCK_STATE"
	watchLockRoleVariable    = "CFO_TEST_WATCH_LOCK_ROLE"
)

// holdWatchLockAs is the stand-in's whole run, and its exit code. It says
// holding once it holds the lock.
func holdWatchLockAs(stateDir, role string) int {
	switch role {
	case "serve":
		watch.HandoverWait = 2 * time.Second
		if err := supervisor.AcquireWatchLock(stateDir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := monitor.TouchHeartbeat(stateDir, time.Now()); err != nil {
			return 1
		}
	case "legacy":
		if _, err := lock.AcquireNamedOwner(stateDir, ".watch.lock", os.Getpid(), watch.WatcherSession); err != nil {
			return 1
		}
	default:
		return 1
	}
	fmt.Println("holding")
	time.Sleep(2 * time.Minute)
	return 0
}

// lockStandIn is a stand-in process and whether it has exited.
type lockStandIn struct {
	pid    int
	exited chan struct{}
}

// startWatchLockStandIn starts this test binary as a stand-in holding the
// watcher lock in stateDir as role, copied as program with arguments when
// program is set, and waits until it says it holds the lock. It never reads
// the lock meanwhile: on Windows a reader holding the lock file open stops a
// serve removing a dead holder's lock.
func startWatchLockStandIn(t *testing.T, stateDir, role, program string, arguments ...string) *lockStandIn {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if program != "" {
		// Not t.TempDir: a process just ended can keep its image open for a
		// moment, so the removal retries once the process has ended.
		dir, err := os.MkdirTemp("", "cfo-stand-in-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			err := os.RemoveAll(dir)
			for deadline := time.Now().Add(10 * time.Second); err != nil && time.Now().Before(deadline); err = os.RemoveAll(dir) {
				time.Sleep(100 * time.Millisecond)
			}
			if err != nil {
				t.Errorf("remove the stand-in's copy: %v", err)
			}
		})
		copyFile(t, path, filepath.Join(dir, program))
		path = filepath.Join(dir, program)
	}
	var output bytes.Buffer
	cmd := exec.Command(path, arguments...)
	cmd.Env = append(os.Environ(), watchLockStandInVariable+"="+stateDir, watchLockRoleVariable+"="+role)
	cmd.Stderr = &output
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	holding := make(chan struct{})
	go func() {
		if line, err := bufio.NewReader(stdout).ReadString('\n'); err == nil && strings.TrimSpace(line) == "holding" {
			close(holding)
		}
		_, _ = io.Copy(io.Discard, stdout)
	}()
	stand := &lockStandIn{pid: cmd.Process.Pid, exited: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(stand.exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-stand.exited
	})
	select {
	case <-holding:
	case <-stand.exited:
		t.Fatalf("the %s stand-in exited without taking the watcher lock: %s", role, output.String())
	case <-time.After(20 * time.Second):
		t.Fatalf("the %s stand-in never took the watcher lock", role)
	}
	return stand
}

// slowInspection stands for a slow monitor cycle: each inspection runs until
// its context ends.
type slowInspection struct{ inspecting chan struct{} }

func (p slowInspection) Inspect(ctx context.Context, _ taskstate.TaskMeta) (monitor.EndpointSample, error) {
	select {
	case p.inspecting <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return monitor.EndpointSample{}, ctx.Err()
}

// An install restarts serve while the CFO's Stop hook hosts the watcher in
// the middle of a slow cycle. The hook's watcher yields to serve at once
// rather than being ended, and the hook goes on waiting on serve's queue, so
// a wake queued afterwards still rewakes the idle CFO.
func TestTheStopHookYieldsToServeMidCycleAndStillRewakesTheCFO(t *testing.T) {
	dir := newPrimaryHome(t)
	setAncestorPID(t, os.Getpid())
	setTinyAutoarmIntervals(t)
	t.Setenv("CFO_CLAUDE_AUTOARM_ATTEMPTS", "2")
	t.Setenv("CFO_CLAUDE_AUTOARM_WAIT", "60")
	state := filepath.Join(dir, "state")
	if err := taskstate.WriteTaskMeta(state, taskstate.TaskMeta{ID: "g1", Worktree: `C:\work\g1`, Backend: "herdr", HerdrSession: "fleet", HerdrWorkspaceID: "ws", HerdrTabID: "tab-g1", HerdrPaneID: "pane-g1"}); err != nil {
		t.Fatal(err)
	}
	writeHeartbeatFixture(t, state, time.Now())
	inspecting := make(chan struct{}, 1)
	exited := make(chan int, 1)
	var stderr bytes.Buffer
	go func() {
		exited <- hookStopAutoarmWithConfig(home.Home{Root: dir, State: state}, claudehook.Payload{SessionID: "s1"}, io.Discard, &stderr, func(h home.Home) watch.Config {
			cfg := watch.ConfigFromEnv(h)
			cfg.Reap = nil
			cfg.Monitor = &monitor.Service{
				StateDir:            h.State,
				Probe:               slowInspection{inspecting},
				Now:                 time.Now,
				StaleEscalateAfter:  time.Minute,
				BusyTurnMax:         time.Hour,
				PauseResurfaceAfter: time.Hour,
				Heartbeat:           time.Minute,
				HeartbeatMax:        time.Hour,
			}
			return cfg
		})
	}()
	select {
	case <-inspecting:
	case <-time.After(15 * time.Second):
		t.Fatal("the hook's watcher never started its scan")
	}

	startWatchLockStandIn(t, state, "serve", "")
	if _, err := wake.Append(state, "notify", "g1", "blocked: Should I merge this?"); err != nil {
		t.Fatal(err)
	}

	select {
	case code := <-exited:
		if code != 2 || !strings.Contains(stderr.String(), "cfo watcher wake") || !strings.Contains(stderr.String(), "notify:g1") {
			t.Fatalf("exit=%d stderr=%q, want the rewake banner naming notify:g1", code, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the hook never rewoke the CFO for the wake queued after serve took over")
	}
	if pending, err := wake.Pending(state); err != nil || len(pending) != 1 {
		t.Errorf("pending wakes = %d (%v), want the queued wake kept for cfo drain", len(pending), err)
	}
}

// On 2026-10-01 the Stop hook holding the lock ran a binary from before the
// handover, and serve ends such a watcher. The next Stop's hook finds serve
// holding the lock and rewakes the CFO on a wake serve's queue holds.
func TestAfterServeEndsALegacyWatcherTheStopHookRewakesFromServesQueue(t *testing.T) {
	dir := newPrimaryHome(t)
	setAncestorPID(t, os.Getpid())
	setTinyAutoarmIntervals(t)
	t.Setenv("CFO_CLAUDE_AUTOARM_WAIT", "30")
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")
	legacy := startWatchLockStandIn(t, state, "legacy", "cfo.exe", "hook", "stop-autoarm")

	startWatchLockStandIn(t, state, "serve", "")
	select {
	case <-legacy.exited:
	case <-time.After(15 * time.Second):
		t.Fatal("the legacy watcher still runs after serve took the lock")
	}
	if _, err := wake.Append(state, "notify", "g1", "blocked: Should I merge this?"); err != nil {
		t.Fatal(err)
	}

	exit, stderr, _ := runAutoarm(t)

	if exit != 2 || !strings.Contains(stderr, "cfo watcher wake") || !strings.Contains(stderr, "notify:g1") {
		t.Fatalf("exit=%d stderr=%q, want the rewake banner naming notify:g1", exit, stderr)
	}
	if pending, err := wake.Pending(state); err != nil || len(pending) != 1 {
		t.Errorf("pending wakes = %d (%v), want the queued wake kept for cfo drain", len(pending), err)
	}
}
