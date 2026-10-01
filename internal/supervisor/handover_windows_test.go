package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

// standIn is a stand-in process and what became of it.
type standIn struct {
	pid    int
	exited chan struct{}
	code   int
}

// startStandIn starts this test binary, copied as program, with arguments,
// as a stand-in holding the watcher lock in stateDir the way mode says, and
// waits until it holds it unless mode is idle.
func startStandIn(t *testing.T, stateDir, mode, program string, arguments ...string) *standIn {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), program)
	copyExecutable(t, self, path)
	cmd := exec.Command(path, arguments...)
	cmd.Env = append(os.Environ(), lockHolderVariable+"="+stateDir, lockHolderModeVariable+"="+mode, "CFO_POLL=300")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stand := &standIn{pid: cmd.Process.Pid, exited: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		stand.code = cmd.ProcessState.ExitCode()
		close(stand.exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-stand.exited
	})
	if mode != "idle" {
		for deadline := time.Now().Add(15 * time.Second); !lock.HeldByNamed(stateDir, watchLock, stand.pid); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("the %s stand-in never took the watcher lock", mode)
			}
		}
	}
	return stand
}

// exitsWithin waits for the stand-in to exit and returns its exit code.
func (s *standIn) exitsWithin(t *testing.T, within time.Duration) int {
	t.Helper()
	select {
	case <-s.exited:
		return s.code
	case <-time.After(within):
		t.Fatalf("pid %d still runs after %s", s.pid, within)
		return 0
	}
}

// running reports whether the stand-in is still running.
func (s *standIn) running() bool {
	select {
	case <-s.exited:
		return false
	default:
		return true
	}
}

func setHandoverWait(t *testing.T, wait time.Duration) {
	t.Helper()
	previous := watch.HandoverWait
	watch.HandoverWait = wait
	t.Cleanup(func() { watch.HandoverWait = previous })
}

// On 2026-10-01 an install stopped serve, the CFO's Stop hook took the
// watcher lock in the gap, and every new serve refused with "existing watch
// owner must finish before serve" until the hook's window ended, leaving the
// board down. A serve that starts while the hook's watcher holds the lock
// asks it to hand over, the watcher yields at once and exits cleanly, and the
// serve runs within seconds. An install restarts serve again and again, so
// the second start finds the hook watching again and takes over the same way.
func TestServeTakesTheLockFromAWatcherTheStopHookHosts(t *testing.T) {
	_, h := testStore(t)
	for round := 1; round <= 2; round++ {
		hook := startStandIn(t, h.State, "watcher", "cfo.exe", "hook", "stop-autoarm")
		began := time.Now()

		s, err := Start(context.Background(), h, Options{})

		if err != nil {
			t.Fatalf("round %d: serve refused to start while the hook's watcher held the lock: %v", round, err)
		}
		if took := time.Since(began); took > 10*time.Second {
			t.Errorf("round %d: serve took %s to take over, want seconds", round, took)
		}
		if !lock.HeldByNamed(h.State, watchLock, os.Getpid()) {
			t.Errorf("round %d: serve runs without holding the watcher lock", round)
		}
		if code := hook.exitsWithin(t, 10*time.Second); code != 0 {
			t.Errorf("round %d: the hook's watcher exited %d after handing over, want a clean exit", round, code)
		}
		if _, err := os.Stat(filepath.Join(h.State, watch.HandoverName)); !os.IsNotExist(err) {
			t.Errorf("round %d: the handover request outlived the handover: %v", round, err)
		}
		s.Close()
	}
}

// Two serves started at once each ask for the lock, and one can replace the
// other's request and then exit, leaving a request from a process that has
// ended, which the watcher does not honour. The serve still waiting asks again
// and the watcher hands it the lock, rather than holding on until the wait
// runs out and the watcher is ended.
func TestAServeWhoseRequestWasReplacedByOneThatEndedStillGetsTheLock(t *testing.T) {
	setHandoverWait(t, 20*time.Second)
	_, h := testStore(t)
	hook := startStandIn(t, h.State, "watcher", "cfo.exe", "hook", "stop-autoarm")
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
	if err := os.WriteFile(filepath.Join(h.State, watch.HandoverName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	began := time.Now()

	err = acquireWithin(h.State, watch.HandoverWait, true)

	if err != nil {
		t.Fatalf("acquireWithin = %v, want the lock handed over", err)
	}
	if took := time.Since(began); took > 10*time.Second {
		t.Errorf("the handover took %s, want seconds", took)
	}
	if code := hook.exitsWithin(t, 10*time.Second); code != 0 {
		t.Errorf("the hook's watcher exited %d, want a clean handover, not an end", code)
	}
	_ = lock.ReleaseExclusiveNamed(h.State, watchLock)
}

// The Stop hook that held the lock on 2026-10-01 ran a binary from before
// the handover and never reads the request. Once the wait runs out, serve
// ends that watcher alone, proved by its lock record, its start time and its
// command line, and leaves every other process of the fleet, here a stand-in
// for the CFO's terminal host, running.
func TestServeEndsAWatcherThatNeverAnswersTheHandover(t *testing.T) {
	setHandoverWait(t, 2*time.Second)
	_, h := testStore(t)
	cfoHost := startStandIn(t, h.State, "idle", "cfo.exe", "host", "--id", "cfo")
	for round := 1; round <= 2; round++ {
		hook := startStandIn(t, h.State, "legacy", "goblins.exe", "hook", "stop-autoarm")

		s, err := Start(context.Background(), h, Options{})

		if err != nil {
			t.Fatalf("round %d: serve refused to start over a watcher from before the handover: %v", round, err)
		}
		hook.exitsWithin(t, 10*time.Second)
		if !lock.HeldByNamed(h.State, watchLock, os.Getpid()) {
			t.Errorf("round %d: serve runs without holding the watcher lock", round)
		}
		s.Close()
	}
	if !cfoHost.running() {
		t.Fatal("the CFO's terminal host was ended")
	}
}

// Only a watcher is ever ended: a cfo process holding the lock as anything
// else, a terminal's host, a serve or a gate's test, any other program, and a
// process the lock record does not truly name are left running, whatever the
// record says.
func TestServeLeavesALockHolderThatIsNotProvedAWatcherRunning(t *testing.T) {
	setHandoverWait(t, time.Second)
	for _, test := range []struct {
		name       string
		program    string
		arguments  []string
		mismatched bool
	}{
		{"a terminal's host", "cfo.exe", []string{"host", "--id", "cfo"}, false},
		{"a serve", "cfo.exe", []string{"serve"}, false},
		{"a gate's test", "goblins.exe", []string{"gate", "test"}, false},
		{"another program", "helper.exe", []string{"hook", "stop-autoarm"}, false},
		{"a watcher the record names with another start", "cfo.exe", []string{"hook", "stop-autoarm"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, h := testStore(t)
			holder := startStandIn(t, h.State, "legacy", test.program, test.arguments...)
			if test.mismatched {
				record, err := lock.ReadNamed(h.State, watchLock)
				if err != nil {
					t.Fatal(err)
				}
				record.Start = record.Start.Add(-time.Hour)
				data, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(h.State, watchLock), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			s, err := Start(context.Background(), h, Options{})
			if err == nil {
				s.Close()
			}

			time.Sleep(500 * time.Millisecond)
			if !holder.running() {
				t.Fatalf("serve ended a lock holder that is not a watcher (start err %v)", err)
			}
			if !test.mismatched && err == nil {
				t.Error("serve started while a live holder it may not end held the watcher lock")
			}
		})
	}
}
