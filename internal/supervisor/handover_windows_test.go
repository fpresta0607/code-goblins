package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
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
	// Not t.TempDir: a process just ended can keep its image open for a
	// moment. This cleanup is registered first so it runs last, once the
	// process below has ended; it retries, and reports a removal that still
	// fails rather than hiding it.
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
	path := filepath.Join(dir, program)
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
	if mode != "idle" && mode != "serve" {
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

// A watcher on this binary that is winding a slow cycle down when serve asks
// answers at once but can hold the lock past the handover wait. Serve sees the
// answer and waits for it rather than ending a watcher that is yielding: the
// Stop hook it would end is what rewakes an idle CFO.
func TestServeWaitsForAWatcherThatAnsweredRatherThanEndingIt(t *testing.T) {
	setHandoverWait(t, 2*time.Second)
	_, h := testStore(t)
	hook := startStandIn(t, h.State, "acknowledging", "cfo.exe", "hook", "stop-autoarm")
	began := time.Now()

	s, err := Start(context.Background(), h, Options{})

	if err != nil {
		t.Fatalf("serve refused to start over a watcher that answered: %v", err)
	}
	defer s.Close()
	if took := time.Since(began); took < acknowledgedHold-time.Second {
		t.Errorf("serve had the lock after %s, before the watcher let it go at %s", took, acknowledgedHold)
	}
	if code := hook.exitsWithin(t, 10*time.Second); code != 0 {
		t.Errorf("the watcher exited %d, want it left to let the lock go and exit cleanly, not ended", code)
	}
	if !lock.HeldByNamed(h.State, watchLock, os.Getpid()) {
		t.Error("serve runs without holding the watcher lock")
	}
	for _, name := range []string{watch.HandoverName, watch.HandoverAckName} {
		if _, err := os.Stat(filepath.Join(h.State, name)); !os.IsNotExist(err) {
			t.Errorf("%s outlived the handover: %v", name, err)
		}
	}
}

// Two serves can ask one after the other: an install's restart and a goblins
// launch. The first is answered and gives up; the second asks while the
// watcher, yielding, is still in an inspection it cannot cut short. The
// watcher answers the second too, so it is never ended for being slow, and
// the second serve takes the lock once the watcher lets it go.
func TestAWatcherYieldingToOneServeAnswersTheNextServeToo(t *testing.T) {
	setHandoverWait(t, 2*time.Second)
	_, h := testStore(t)
	hook := startStandIn(t, h.State, "slow-watcher", "cfo.exe", "hook", "stop-autoarm")
	first := exec.Command("cmd", "/c", "ping -n 60 127.0.0.1 >NUL")
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Process.Kill(); _, _ = first.Process.Wait() })
	firstStart, ok := proc.StartTime(first.Process.Pid)
	if !ok {
		t.Fatal("read the first serve's start time")
	}
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(map[string]any{"pid": first.Process.Pid, "start": firstStart, "hostname": hostname})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, watch.HandoverName), request, 0o600); err != nil {
		t.Fatal(err)
	}
	ack := filepath.Join(h.State, watch.HandoverAckName)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if data, err := os.ReadFile(ack); err == nil && strings.Contains(string(data), fmt.Sprintf(`"serve_pid":%d`, first.Process.Pid)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the watcher never answered the first serve")
		}
	}
	_ = first.Process.Kill()
	_, _ = first.Process.Wait()
	for _, name := range []string{watch.HandoverName, watch.HandoverAckName} {
		if err := os.Remove(filepath.Join(h.State, name)); err != nil {
			t.Fatal(err)
		}
	}

	s, err := Start(context.Background(), h, Options{})

	if err != nil {
		t.Fatalf("the second serve refused to start over a watcher yielding to it: %v", err)
	}
	defer s.Close()
	if code := hook.exitsWithin(t, 10*time.Second); code != 0 {
		t.Errorf("the watcher exited %d, want it left to finish its inspection and yield, not ended", code)
	}
	if !lock.HeldByNamed(h.State, watchLock, os.Getpid()) {
		t.Error("the second serve runs without holding the watcher lock")
	}
}

// requestFor writes the request a serve, here pid, writes while it waits.
func requestFor(stateDir string, pid int) error {
	start, ok := proc.StartTime(pid)
	if !ok {
		return fmt.Errorf("read pid %d's start time", pid)
	}
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}
	data, err := json.Marshal(map[string]any{"pid": pid, "start": start, "hostname": hostname})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stateDir, watch.HandoverName), data, 0o600)
}

// Two serves started at once, an install's restart and a goblins launch, both
// ask a watcher that is yielding but cannot let the lock go within the
// handover wait, and its answer switches between them as each asks again.
// Neither ends the watcher: an answer naming the watcher proves it is
// yielding, whichever serve it names. One serve takes the lock once the
// watcher lets it go, and the other refuses, because a supervisor holds it.
func TestTwoServesAskingAtOnceNeverEndAWatcherThatAnswered(t *testing.T) {
	setHandoverWait(t, 2*time.Second)
	_, h := testStore(t)
	hook := startStandIn(t, h.State, "slow-watcher", "cfo.exe", "hook", "stop-autoarm")
	other := startStandIn(t, h.State, "serve", "cfo.exe", "serve")
	// The other serve's request is written again far more often than once a
	// second, so the answer names it, not this serve, when this serve's
	// handover wait runs out. A write that meets the watcher reading the
	// request is made again on the next pass, as a serve asking again does.
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			_ = requestFor(h.State, other.pid)
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}()

	s, err := Start(context.Background(), h, Options{})

	close(stop)
	<-stopped
	if code := hook.exitsWithin(t, 15*time.Second); code != 0 {
		t.Errorf("the watcher exited %d, want it left to finish its inspection and yield, not ended", code)
	}
	if err == nil {
		defer s.Close()
		if code := other.exitsWithin(t, 15*time.Second); code != 1 {
			t.Errorf("the other serve exited %d, want it refused because this serve holds the lock", code)
		}
	} else if !lock.HeldByNamed(h.State, watchLock, other.pid) {
		t.Errorf("neither serve holds the lock (this serve: %v)", err)
	}
}

// heldOpenByReaders keeps opening the watcher lock the way every reader of it
// does, without delete sharing, holding it a while each time, as a Stop hook,
// cfo doctor or the board checking who holds the lock, until the test ends.
func heldOpenByReaders(t *testing.T, stateDir string) {
	t.Helper()
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			if f, err := os.Open(filepath.Join(stateDir, watchLock)); err == nil {
				time.Sleep(100 * time.Millisecond)
				_ = f.Close()
			}
			select {
			case <-stop:
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		<-stopped
	})
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
	heldOpenByReaders(t, h.State)
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

// A watcher yielding to serve lets the lock go between serve's acquire that
// it refused and serve reading who held it (2026-10-01, the gate's baseline:
// "existing watch owner must finish before serve: open .watch.lock: The
// system cannot find the file specified."). The lock is free by then, so
// serve takes it; a holder that keeps the lock and is no watcher is still
// refused at once.
func TestServeTakesALockItsHolderLetGoBeforeServeReadWhoHeldIt(t *testing.T) {
	for _, test := range []struct {
		name      string
		session   string
		isLetGo   bool
		wantTaken bool
	}{
		{"a watcher that let go", watch.WatcherSession, true, true},
		{"a holder that keeps it and is no watcher", "exclusive-spawn", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateDir := t.TempDir()
			holder := exec.Command("cmd", "/c", "ping -n 30 127.0.0.1 >NUL")
			if err := holder.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
			if _, err := lock.AcquireNamedOwner(stateDir, watchLock, holder.Process.Pid, test.session); err != nil {
				t.Fatal(err)
			}
			previous := betweenRefusalAndRead
			t.Cleanup(func() { betweenRefusalAndRead = previous })
			refused := 0
			betweenRefusalAndRead = func() {
				refused++
				if test.isLetGo && refused == 1 {
					if err := os.Remove(filepath.Join(stateDir, watchLock)); err != nil {
						t.Error(err)
					}
				}
			}
			began := time.Now()

			err := AcquireWatchLock(stateDir)

			if refused == 0 {
				t.Fatal("the premise failed: the first acquire was not refused")
			}
			if test.wantTaken {
				if err != nil {
					t.Fatalf("serve refused a lock its holder had let go: %v", err)
				}
				defer lock.ReleaseExclusiveNamed(stateDir, watchLock)
				if current, readErr := lock.ReadNamed(stateDir, watchLock); readErr != nil || current.PID != os.Getpid() {
					t.Fatalf("lock holder = %+v, %v; want this process", current, readErr)
				}
				return
			}
			if !errors.Is(err, lock.ErrHeld) {
				t.Fatalf("AcquireWatchLock = %v, want it refused as held", err)
			}
			if took := time.Since(began); took > 5*time.Second {
				t.Errorf("the refusal took %s, want it at once", took)
			}
		})
	}
}
