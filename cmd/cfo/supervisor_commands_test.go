package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// stoppableBoard answers like a running supervisor until a stop request
// naming pid appears in stateDir, then stops answering, the way the
// supervisor honours one.
func stoppableBoard(t *testing.T, stateDir string, pid int) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(busySnapshot))
	}))
	var once sync.Once
	stop := func() { once.Do(server.Close) }
	t.Cleanup(stop)
	go func() {
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			var request struct {
				PID int `json:"pid"`
			}
			data, err := os.ReadFile(filepath.Join(stateDir, "serve.stop"))
			if err == nil && json.Unmarshal(data, &request) == nil && request.PID == pid {
				stop()
				return
			}
		}
	}()
	return server.URL
}

func (f *launcherFixture) command(args ...string) (int, string, string) {
	f.t.Helper()
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime(args, &stdout, &stderr, f.runtime)
	return exit, stdout.String(), stderr.String()
}

func newCommandFixture(t *testing.T) (*launcherFixture, *[]int) {
	t.Helper()
	f := newLauncherFixture(t, func(home.Home) (<-chan struct{}, error) {
		t.Fatal("a status or stop started a supervisor")
		return nil, nil
	})
	var killed []int
	f.runtime.killTree = func(pid int) error {
		killed = append(killed, pid)
		return nil
	}
	return f, &killed
}

// goblins stop asks the supervisor its record names to stop and waits until
// its board no longer answers.
func TestGoblinsStopAsksTheSupervisorToStopAndWaits(t *testing.T) {
	f, killed := newCommandFixture(t)
	f.record(stoppableBoard(t, f.home.State, 4242))

	exit, stdout, stderr := f.command("stop")

	if exit != 0 || !strings.Contains(stdout, "The supervisor (pid 4242) stopped.") {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want the supervisor stopped", exit, stdout, stderr)
	}
	if len(*killed) != 0 {
		t.Fatalf("killed %v, want the supervisor asked, not ended", *killed)
	}
}

// A supervisor that does not stop when asked is left running, and goblins
// stop says how to end it.
func TestGoblinsStopNamesTheForceForASupervisorThatDoesNotStop(t *testing.T) {
	defer func(timeout, poll time.Duration) { stopTimeout, stopPoll = timeout, poll }(stopTimeout, stopPoll)
	stopTimeout, stopPoll = 300*time.Millisecond, 20*time.Millisecond
	f, killed := newCommandFixture(t)
	f.record(fakeBoard(t, busySnapshot))

	exit, _, stderr := f.command("stop")

	if exit != 1 || !strings.Contains(stderr, "goblins stop --force ends it") || len(*killed) != 0 {
		t.Fatalf("exit=%d stderr=%q killed=%v, want the force named and nothing ended", exit, stderr, *killed)
	}
	if _, err := readBoardRecord(f.home.State); err != nil {
		t.Fatalf("the record of a supervisor still running was removed: %v", err)
	}
}

// goblins stop --force ends the supervisor's whole process tree by the pid its
// record names, and asks nothing.
func TestGoblinsStopForceEndsTheSupervisorsTree(t *testing.T) {
	f, killed := newCommandFixture(t)
	f.record(fakeBoard(t, busySnapshot))

	exit, stdout, stderr := f.command("stop", "--force")

	if exit != 0 || len(*killed) != 1 || (*killed)[0] != 4242 || !strings.Contains(stdout, "(pid 4242) was ended") {
		t.Fatalf("exit=%d killed=%v stdout=%q stderr=%q, want pid 4242 ended", exit, *killed, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(f.home.State, "serve.stop")); !os.IsNotExist(err) {
		t.Fatalf("a forced stop also wrote a stop request: %v", err)
	}
	if _, err := os.Stat(boardRecordPath(f.home.State)); !os.IsNotExist(err) {
		t.Fatalf("the ended supervisor's record was kept: %v", err)
	}
}

// With no supervisor there is nothing to stop, and a record whose board does
// not answer is left over from one that ended: it is removed, and nothing is
// ended or asked.
func TestGoblinsStopWithNoSupervisorStopsNothing(t *testing.T) {
	f, killed := newCommandFixture(t)
	if exit, stdout, _ := f.command("stop", "--force"); exit != 0 || !strings.Contains(stdout, "The supervisor is not running.") {
		t.Fatalf("no record: exit=%d stdout=%q, want not running", exit, stdout)
	}
	gone := httptest.NewServer(http.NotFoundHandler())
	f.record(gone.URL)
	gone.Close()

	exit, stdout, _ := f.command("stop", "--force")

	if exit != 0 || !strings.Contains(stdout, "The supervisor is not running.") || len(*killed) != 0 {
		t.Fatalf("stale record: exit=%d stdout=%q killed=%v, want not running and nothing ended", exit, stdout, *killed)
	}
	if _, err := os.Stat(boardRecordPath(f.home.State)); !os.IsNotExist(err) {
		t.Fatalf("the stale record was kept: %v", err)
	}
}

// goblins status shows the running supervisor's board, the fleet's status and
// its pid, and exits 1 when none runs, including when only a record left by
// one that ended remains.
func TestGoblinsStatusShowsTheSupervisorOrItsAbsence(t *testing.T) {
	f, _ := newCommandFixture(t)
	if exit, stdout, _ := f.command("status"); exit != 1 || !strings.Contains(stdout, "The supervisor is not running") {
		t.Fatalf("no supervisor: exit=%d stdout=%q, want 1 and the absence", exit, stdout)
	}
	gone := httptest.NewServer(http.NotFoundHandler())
	f.record(gone.URL)
	gone.Close()
	if exit, stdout, _ := f.command("status"); exit != 1 || !strings.Contains(stdout, "The supervisor is not running") {
		t.Fatalf("stale record: exit=%d stdout=%q, want 1 and the absence", exit, stdout)
	}
	board := fakeBoard(t, busySnapshot)
	f.record(board)

	exit, stdout, stderr := f.command("status")

	want := "  board   " + board + "\n  status  CFO supervising · 2 goblins working · 3 waiting on you\n  pid     4242\n"
	if exit != 0 || stdout != want {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want %q", exit, stdout, stderr, want)
	}
}
