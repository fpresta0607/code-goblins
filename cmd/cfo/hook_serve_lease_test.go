package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/supervise"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// leasedServe stands in for cfo serve as the live fleet ran it on
// 2026-09-30: a live process holds .watch.lock with the exclusive-spawn lease
// supervisor.Start takes for its whole life, and its heartbeat's last cycle
// is lastCycleAgo old.
func leasedServe(t *testing.T, state string, lastCycleAgo time.Duration) int {
	t.Helper()
	serve := startLiveForeignProcess(t)
	if _, err := lock.AcquireNamedOwner(state, ".watch.lock", serve.Process.Pid, "exclusive-spawn"); err != nil {
		t.Fatal(err)
	}
	writeHeartbeatFixture(t, state, time.Now().Add(-lastCycleAgo))
	return serve.Process.Pid
}

// At 19:49:52Z on 2026-09-30 the CFO's Stop hook re-armed while cfo serve
// held the watcher and its heartbeat was more than five minutes old from a
// slow reconcile cycle. The hook cannot host a watcher serve holds, and it
// reported "auto-arm FAILED ... run cfo install", which fixes nothing. While a
// live process holds the watcher the hook delivers from its queue, however old
// the heartbeat.
func TestAutoarmDeliversFromServesQueueWhileItsHeartbeatIsStale(t *testing.T) {
	dir := newPrimaryHome(t)
	setAncestorPID(t, os.Getpid())
	setTinyAutoarmIntervals(t)
	t.Setenv("CFO_CLAUDE_AUTOARM_WAIT", "30")
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")
	leasedServe(t, state, 10*time.Minute)

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(1500 * time.Millisecond)
		if _, err := wake.Append(state, "notify", "g1", "blocked: Should I merge this?"); err != nil {
			t.Error(err)
		}
	}()
	defer func() { <-done }()

	exit, stderr, _ := runAutoarm(t)
	if exit != 2 || !strings.Contains(stderr, "cfo watcher wake") || !strings.Contains(stderr, "notify:g1") || strings.Contains(stderr, "FAILED") {
		t.Fatalf("exit=%d stderr=%q, want the rewake for notify:g1 and no failure banner", exit, stderr)
	}
	if supervise.NotifiedOnce(state) {
		t.Error("a watcher-down episode was marked for a watcher serve holds")
	}
	assertEpochOutcome(t, state, "rewake")
}

// A serve that holds the watcher and has not finished a cycle for the stall
// window has stopped raising monitor wakes, so supervision is down; the banner
// names the holder, how long ago it last finished a cycle and the fix, and
// never sends the CFO to cfo install, which cannot free a lock a live process
// holds.
func TestAutoarmNamesAStalledServeAndItsFix(t *testing.T) {
	dir := newPrimaryHome(t)
	setAncestorPID(t, os.Getpid())
	setTinyAutoarmIntervals(t)
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")
	pid := leasedServe(t, state, 20*time.Minute)

	exit, stderr, _ := runAutoarm(t)

	if exit != 2 {
		t.Fatalf("exit=%d stderr=%q, want the supervision-down rewake", exit, stderr)
	}
	for _, want := range []string{"pid " + strconv.Itoa(pid) + " holds the watcher", "has not finished a cycle for 20m", "restart cfo serve"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr %q lacks %q", stderr, want)
		}
	}
	if strings.Contains(stderr, "cfo install") {
		t.Errorf("stderr %q sends the CFO to cfo install, which cannot free a lock a live process holds", stderr)
	}
}

// A failure beside a healthy serve is not a stall: when recording the rewake
// fails while serve holds the watcher with a fresh heartbeat, the banner names
// that error and does not send the CFO to restart a serve that is working.
func TestAutoarmNamesTheRealErrorBesideAHealthyServe(t *testing.T) {
	dir := newPrimaryHome(t)
	setAncestorPID(t, os.Getpid())
	setTinyAutoarmIntervals(t)
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")
	servingWatcher(t, state)
	if _, err := wake.Append(state, "notify", "g1", "blocked: Should I merge this?"); err != nil {
		t.Fatal(err)
	}
	rewoken := filepath.Join(state, rewokenFile)
	if err := os.WriteFile(rewoken, []byte("0\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(rewoken, 0o644) })

	exit, stderr, _ := runAutoarm(t)

	if exit != 2 || !strings.Contains(stderr, "record the rewake") {
		t.Fatalf("exit=%d stderr=%q, want the failure banner naming the rewake error", exit, stderr)
	}
	if strings.Contains(stderr, "restart cfo serve") {
		t.Errorf("stderr %q tells the CFO to restart a serve whose heartbeat is fresh", stderr)
	}
}

// A hook that watched serve's queue for its whole window with nothing arriving
// ends by rewaking the CFO to re-arm it: exiting silently would leave an idle
// CFO with no hook watching, deaf to every wake after the window.
func TestAutoarmRewakesToReArmAtTheEndOfItsWindow(t *testing.T) {
	dir := newPrimaryHome(t)
	setAncestorPID(t, os.Getpid())
	setTinyAutoarmIntervals(t)
	t.Setenv("CFO_CLAUDE_AUTOARM_WAIT", "2")
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")
	servingWatcher(t, state)

	exit, stderr, elapsed := runAutoarm(t)
	if exit != 2 || !strings.Contains(stderr, "cfo watch window ended") || !strings.Contains(stderr, "re-arm") {
		t.Fatalf("exit=%d stderr=%q, want a rewake that re-arms the hook", exit, stderr)
	}
	if elapsed < 2*time.Second {
		t.Errorf("elapsed = %v, want the hook to have watched its whole 2s window", elapsed)
	}
	assertEpochOutcome(t, state, "rewake")
}
