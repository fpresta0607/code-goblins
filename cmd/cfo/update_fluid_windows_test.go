package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// startWith runs program in the home as start does, with more environment.
func (u *updateHome) startWith(environment []string, program string, arguments ...string) *exec.Cmd {
	u.t.Helper()
	cmd := exec.Command(program, arguments...)
	cmd.Dir = u.root
	cmd.Env = append(append(os.Environ(), "CFO_TEST_UPDATE_ROOT="+u.root, "CFO_HOME="+u.root), environment...)
	if err := cmd.Start(); err != nil {
		u.t.Fatal(err)
	}
	start, _ := proc.StartTime(cmd.Process.Pid)
	u.started[cmd] = start
	go func() { _ = cmd.Wait() }()
	return cmd
}

// lateSupervisor is the supervisor the late-serve seam started, by the pid
// it left under the home's state.
func (u *updateHome) lateSupervisor() serveProcess {
	u.t.Helper()
	data, err := os.ReadFile(filepath.Join(u.state, lateServeFile))
	if err != nil {
		u.t.Fatalf("the late supervisor was never started: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		u.t.Fatal(err)
	}
	start, _ := proc.StartTime(pid)
	return serveProcess{pid: pid, start: start}
}

// The update of 2026-10-09, in its order: the supervisor was slow to stop,
// the new build's supervisor did not serve, the rollback began, and while it
// put the previous build's files back a goblins opened beside it started the
// new build's supervisor from the alias still holding it. That supervisor
// took the watcher lock, the previous build was refused it three times, and
// the update ended with the board called down, the previous build on disk and
// the new one serving. The rollback finishes all the same: the previous build
// serves, and nothing started late does.
func TestARollbackFinishesThoughASupervisorOfTheNewBuildStartsLate(t *testing.T) {
	// Arrange
	u := newUpdateHome(t, "previous", "second-start")
	slowToStop := u.startWith([]string{"CFO_TEST_SERVE_DEAF=1"}, filepath.Join(u.bin, "goblins.exe"), "serve", "--listen", "127.0.0.1:0")
	u.awaitBoard()

	// Act
	code, output := u.run([]string{"CFO_TEST_UPDATE_LATE_SERVE=restoring"})

	// Assert
	if code != updateRolledBack {
		t.Fatalf("update exited %d, want %d:\n%s", code, updateRolledBack, output)
	}
	if strings.Contains(output, "BOARD IS DOWN") {
		t.Fatalf("the rollback called the board down:\n%s", output)
	}
	u.previousServes()
	if late := u.lateSupervisor(); processIs(late) {
		t.Errorf("the new build's supervisor started late (pid %d) still runs beside the previous build's", late.pid)
	}
	if u.running(slowToStop) {
		t.Error("the supervisor that was slow to stop still runs")
	}
	noPasteLine(t, output)
}

// A goblins opened while the board is away for an update starts a supervisor
// from the alias, which before the swap is the previous build. It is refused
// the watcher lock, which the update holds from the moment it stopped the
// supervisor, so the new build's supervisor starts as if nothing had: the
// update installs, and the supervisor started late does not serve.
func TestAnUpdateInstallsThoughASupervisorStartsWhileTheBoardIsAway(t *testing.T) {
	// Arrange
	u := newUpdateHome(t, "previous", "candidate")
	u.serving()
	candidate, _ := os.ReadFile(u.candidate)

	// Act
	code, output := u.run([]string{"CFO_TEST_UPDATE_LATE_SERVE=stopped"})

	// Assert
	if code != updateInstalled {
		t.Fatalf("update exited %d, want %d:\n%s", code, updateInstalled, output)
	}
	u.aliasesAre(candidate, "candidate")
	record := u.awaitBoard()
	if err := boardAlive(context.Background(), record); err != nil {
		t.Fatalf("the candidate's supervisor does not answer as itself: %v", err)
	}
	late := u.lateSupervisor()
	if record.PID == late.pid || processIs(late) {
		t.Errorf("the supervisor started while the board was away (pid %d) serves or still runs", late.pid)
	}
	noPasteLine(t, output)
}

// stamped is the time a stand-in left in name under the home's state.
func (u *updateHome) stamped(name string) time.Time {
	u.t.Helper()
	data, err := os.ReadFile(filepath.Join(u.state, name))
	if err != nil {
		u.t.Fatalf("%s was never written: %v", name, err)
	}
	nanoseconds, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		u.t.Fatal(err)
	}
	return time.Unix(0, nanoseconds)
}

// working starts a stand-in for lifecycle work that holds lockName until
// until after an update is prepared, and waits until it holds the lock.
func (u *updateHome) working(lockName, until string) *exec.Cmd {
	u.t.Helper()
	work := u.start(filepath.Join(u.bin, "cfo.exe"), "hold", lockName, until)
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if holder, err := lock.ReadNamed(u.state, lockName); err == nil && holder.PID == work.Process.Pid {
			return work
		}
		if time.Now().After(deadline) {
			u.t.Fatalf("the lifecycle work never held %s", lockName)
		}
	}
}

// noPasteLine fails when what an update printed holds a command to paste.
func noPasteLine(t *testing.T, output string) {
	t.Helper()
	for _, pasted := range []string{"PowerShell", "$env:", "update --recover"} {
		if strings.Contains(output, pasted) {
			t.Errorf("the update printed a command to paste (%q):\n%s", pasted, output)
		}
	}
}

// An update that runs past its bound is stopped where it is and the previous
// build is put back, by the update itself: the board is never left away for
// as long as a step happens to take. On 2026-10-09 one step took seven
// minutes and the card showed the update running for twelve.
func TestAnUpdatePastItsBoundIsStoppedAndThePreviousBuildPutBack(t *testing.T) {
	// Arrange
	u := newUpdateHome(t, "previous", "candidate")
	u.serving()

	// Act: the update waits 20 s once it has swapped, far past its bound.
	code, output := u.run([]string{"CFO_TEST_UPDATE_PAUSE=swapped", "CFO_TEST_UPDATE_BOUND=5s"})

	// Assert
	if code != updateRolledBack {
		t.Fatalf("update exited %d, want %d:\n%s", code, updateRolledBack, output)
	}
	if !strings.Contains(output, "took longer than 5s") {
		t.Errorf("the update does not say it took longer than its bound:\n%s", output)
	}
	u.previousServes()
	noPasteLine(t, output)
}

// An update that stopped part way no longer asks for a line pasted into
// PowerShell before the next one: the next update, which Try again on the
// card runs, puts the previous build back by itself first and then installs.
func TestAnUpdateAfterOneThatStoppedPartWayPutsThePreviousBuildBackFirst(t *testing.T) {
	// Arrange
	u := newUpdateHome(t, "previous", "candidate")
	u.serving()
	if code, output := u.run([]string{"CFO_TEST_UPDATE_INTERRUPT=swapped"}); code != 9 {
		t.Fatalf("the update did not end at swapped (exit %d):\n%s", code, output)
	}
	candidate, _ := os.ReadFile(u.candidate)

	// Act
	code, output := u.run(nil)

	// Assert
	if code != updateInstalled {
		t.Fatalf("the next update exited %d, want %d:\n%s", code, updateInstalled, output)
	}
	if !strings.Contains(output, "the previous build is put back first") {
		t.Errorf("the next update does not say it put the previous build back first:\n%s", output)
	}
	u.aliasesAre(candidate, "candidate")
	if err := boardAlive(context.Background(), u.awaitBoard()); err != nil {
		t.Fatalf("the candidate's supervisor does not answer as itself: %v", err)
	}
	noPasteLine(t, output)
}

// Lifecycle work in flight when an update begins, a clean-up, a pause or a
// resume, is given a moment to finish before the supervisor is stopped: the
// update says what it waits for, and stops the supervisor only once that work
// is done.
func TestAnUpdateWaitsForLifecycleWorkInFlightBeforeItStopsTheSupervisor(t *testing.T) {
	for _, test := range []struct {
		name string
		lock string
		says string
	}{
		{"a clean-up", ".cleanup-fix-thing.lock", "the clean-up of fix-thing"},
		{"a pause", ".lifecycle-fix-thing.lock", "the pause, resume or stop of fix-thing"},
		{"a resume", ".switch-fix-thing.lock", "the start of fix-thing's agent"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			u := newUpdateHome(t, "previous", "candidate")
			u.serving()
			u.working(test.lock, "3s")

			// Act
			code, output := u.run(nil)

			// Assert
			if code != updateInstalled {
				t.Fatalf("update exited %d, want %d:\n%s", code, updateInstalled, output)
			}
			if !strings.Contains(output, "Waiting for "+test.says) {
				t.Errorf("the update does not say it waits for %s:\n%s", test.says, output)
			}
			if stopped, finished := u.stamped(servedUntilFile), u.stamped(heldUntilFile); stopped.Before(finished) {
				t.Errorf("the supervisor was stopped at %s, before the lifecycle work finished at %s", stopped.Format(time.StampMilli), finished.Format(time.StampMilli))
			}
		})
	}
}

// Lifecycle work that does not finish within the update's wait does not hold
// the update: it says so, goes on, and installs, and that work, a process of
// its own, is left running.
func TestAnUpdateGoesOnPastLifecycleWorkThatDoesNotFinish(t *testing.T) {
	// Arrange
	u := newUpdateHome(t, "previous", "candidate")
	u.serving()
	work := u.working(".cleanup-fix-thing.lock", "forever")

	// Act
	code, output := u.run([]string{"CFO_TEST_UPDATE_QUIET_WAIT=3s"})

	// Assert
	if code != updateInstalled {
		t.Fatalf("update exited %d, want %d:\n%s", code, updateInstalled, output)
	}
	if !strings.Contains(output, "still runs after 3s") {
		t.Errorf("the update does not say the work still runs after its wait:\n%s", output)
	}
	if !u.running(work) {
		t.Error("the update ended lifecycle work it only waited for")
	}
	if err := boardAlive(context.Background(), u.awaitBoard()); err != nil {
		t.Fatalf("the candidate's supervisor does not answer as itself: %v", err)
	}
}
