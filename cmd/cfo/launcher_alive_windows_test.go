package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

// stalledBoard answers as the recorded supervisor but never sends the
// fleet's snapshot, as a board whose snapshot is still being built. Each
// snapshot request its client gives up sends how long the board held it.
func stalledBoard(t *testing.T) (string, <-chan time.Duration) {
	t.Helper()
	release := make(chan struct{})
	abandoned := make(chan time.Duration, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/alive" {
			_, _ = fmt.Fprintf(w, `{"pid":%d}`, fakeBoardPID)
			return
		}
		arrived := time.Now()
		select {
		case <-r.Context().Done():
			abandoned <- time.Since(arrived)
		case <-release:
		}
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})
	return server.URL, abandoned
}

// holdFile holds path open without sharing it, as the cmd that started the
// recovered supervisor on 2026-10-01 held serve.log.
func holdFile(t *testing.T, path string) {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.CloseHandle(handle) })
}

// askForTheLockAs writes the handover request a serve, here pid, writes
// while it waits for a watcher to hand it the lock.
func askForTheLockAs(t *testing.T, stateDir string, pid int) {
	t.Helper()
	probe := t.TempDir()
	if _, err := lock.AcquireNamedOwner(probe, ".probe.lock", pid, "probe"); err != nil {
		t.Fatal(err)
	}
	identity, err := lock.ReadNamed(probe, ".probe.lock")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"pid": pid, "start": identity.Start, "hostname": identity.Hostname})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "serve.handover"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// On 2026-10-01 the recovered supervisor's snapshot took eight seconds, the
// launcher's three-second wait read it as dead, and goblins tried to start a
// second supervisor, which could not open the serve.log the live one held.
// A live supervisor whose snapshot never comes is still found at once by its
// pid, and goblins goes on to the CFO without waiting on the snapshot.
func TestGoblinsGoesOnPromptlyWhenTheSnapshotNeverAnswers(t *testing.T) {
	f := newLauncherFixture(t, func(home.Home) (<-chan struct{}, error) {
		t.Fatal("goblins started a second supervisor over a live one")
		return nil, nil
	})
	board, abandoned := stalledBoard(t)
	f.record(board)
	holdFile(t, serveLogPath(f.home.State))

	exit, stdout, stderr := f.launch()

	if exit != 0 || f.starts != 0 {
		t.Fatalf("exit=%d starts=%d stdout=%q stderr=%q, want the live supervisor found", exit, f.starts, stdout, stderr)
	}
	if !strings.Contains(stdout, "the fleet's status is still loading") {
		t.Errorf("stdout = %q, want the banner to say the status is still loading", stdout)
	}
	// Only goblins giving the request up ends it, since the board never
	// answers. The board times the wait itself, so the rest of the launch,
	// which a busy machine slows, does not count against the snapshot's bound.
	select {
	case held := <-abandoned:
		if held > snapshotTimeout+2*time.Second {
			t.Errorf("goblins waited %s on the snapshot, want no longer than its own bound of %s", held, snapshotTimeout)
		}
	case <-time.After(time.Minute):
		t.Error("goblins went on without giving up its request for the snapshot")
	}
}

// Only a successful answer naming the recorded pid is the recorded
// supervisor. Any other listener at the address, a supervisor from before
// the probe, an answer naming no pid or another one, is not this home's live
// supervisor, so goblins status says none runs.
func TestGoblinsStatusTakesOnlyTheRecordedSupervisorAsRunning(t *testing.T) {
	for name, answer := range map[string]http.HandlerFunc{
		"a page not found": http.NotFound,
		"no JSON": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("hello"))
		},
		"pid zero": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"pid":0}`))
		},
		"no pid": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{}`))
		},
		"another pid": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"pid":9}`))
		},
		"the right pid with an error": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprintf(w, `{"pid":%d}`, fakeBoardPID)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f, _ := newCommandFixture(t)
			server := httptest.NewServer(answer)
			t.Cleanup(server.Close)
			f.record(server.URL)

			exit, stdout, _ := f.command("status")

			if exit != 1 || !strings.Contains(stdout, "The supervisor is not running") {
				t.Fatalf("exit=%d stdout=%q, want 1 and no supervisor", exit, stdout)
			}
		})
	}
}

// A record naming no process, pid 0 or below, is never a supervisor, even
// when what answers at its address claims the same pid.
func TestGoblinsStatusRefusesARecordThatNamesNoProcess(t *testing.T) {
	f, _ := newCommandFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"pid":0}`))
	}))
	t.Cleanup(server.Close)
	if err := os.WriteFile(boardRecordPath(f.home.State), []byte(fmt.Sprintf(`{"pid":0,"url":%q}`, server.URL)), 0o600); err != nil {
		t.Fatal(err)
	}

	exit, stdout, _ := f.command("status")

	if exit != 1 || !strings.Contains(stdout, "The supervisor is not running") {
		t.Fatalf("exit=%d stdout=%q, want 1 and no supervisor", exit, stdout)
	}
}

// A cfo serve started while this home's supervisor serves says where that
// board is, rather than failing to take the address or the watcher lock.
func TestServeReportsTheBoardAlreadyServingThisHome(t *testing.T) {
	f, _ := newCommandFixture(t)
	board := fakeBoard(t, busySnapshot)
	f.record(board)
	// An address already taken, so a serve that went on would fail at once
	// instead of running.
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = taken.Close() })

	exit, _, stderr := f.command("serve", "--listen", taken.Addr().String())

	if exit != 1 || !strings.Contains(stderr, "already serves this home's board at "+board) {
		t.Fatalf("exit=%d stderr=%q, want the live board named", exit, stderr)
	}
}

// After the Windows restart of 2026-10-27 a serve held the usual address
// before it held the watcher lock, a second goblins read that silent listener
// as another program and started a supervisor on a free port, and that one won
// the lock, so the board came back on a random port (#1592). A serve that does
// not hold the lock yet leaves the address free.
func TestServeLeavesTheBoardsAddressFreeUntilItHoldsTheWatcherLock(t *testing.T) {
	// Arrange
	defer func(wait, alive time.Duration) { watch.HandoverWait, aliveTimeout = wait, alive }(watch.HandoverWait, aliveTimeout)
	watch.HandoverWait, aliveTimeout = 3*time.Second, 300*time.Millisecond
	f, _ := newCommandFixture(t)
	if _, err := lock.AcquireNamedOwner(f.home.State, ".watch.lock", startLiveForeignProcess(t).Process.Pid, watch.WatcherSession); err != nil {
		t.Fatal(err)
	}
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := free.Addr().String()
	if err := free.Close(); err != nil {
		t.Fatal(err)
	}

	// Act
	type served struct {
		exit   int
		stderr string
	}
	ended := make(chan served, 1)
	go func() {
		exit, _, stderr := f.command("serve", "--listen", address)
		ended <- served{exit, stderr}
	}()
	request := filepath.Join(f.home.State, "serve.handover")
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(request); err == nil {
			break
		}
		select {
		case result := <-ended:
			t.Fatalf("serve ended without asking the watcher for the lock: exit=%d stderr=%q", result.exit, result.stderr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("serve never asked the watcher for the lock")
		}
	}
	held := boardAddressFree(context.Background(), address)
	result := <-ended

	// Assert
	if held != nil {
		t.Errorf("while serve waited for the watcher lock, its address: %v, want it free", held)
	}
	if result.exit != 1 || !strings.Contains(result.stderr, "existing watch owner must finish before serve") {
		t.Errorf("exit=%d stderr=%q, want serve refused by the lock's holder", result.exit, result.stderr)
	}
}

// A serve that takes the watcher lock and then cannot take the board's address
// lets the lock go, so the next start finds it free.
func TestServeLetsTheWatcherLockGoWhenItsAddressIsTaken(t *testing.T) {
	// Arrange
	f, _ := newCommandFixture(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = taken.Close() })

	// Act
	exit, _, stderr := f.command("serve", "--listen", taken.Addr().String())

	// Assert
	if exit != 1 || !strings.Contains(stderr, "bind") {
		t.Fatalf("exit=%d stderr=%q, want serve refused by the address", exit, stderr)
	}
	if _, err := lock.ReadNamed(f.home.State, ".watch.lock"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the watcher lock after the refusal: %v, want it let go", err)
	}
	if err := supervisor.AcquireWatchLock(f.home.State); err != nil {
		t.Fatalf("the next start could not take the watcher lock: %v", err)
	}
	if err := supervisor.ReleaseWatchLock(f.home.State); err != nil {
		t.Fatal(err)
	}
}

// goblins stop never takes a supervisor whose snapshot is slow for one that
// ended: it asks it to stop and keeps its record, rather than removing the
// record of a live supervisor, which would let goblins start a second.
func TestGoblinsStopKeepsTheRecordOfASupervisorWhoseSnapshotNeverAnswers(t *testing.T) {
	previous := stopTimeout
	stopTimeout = time.Second
	t.Cleanup(func() { stopTimeout = previous })
	f, killed := newCommandFixture(t)
	board, _ := stalledBoard(t)
	f.record(board)

	_, stdout, stderr := f.command("stop")

	if strings.Contains(stdout, "not running") {
		t.Fatalf("stdout=%q stderr=%q, want the live supervisor asked to stop, not taken for gone", stdout, stderr)
	}
	if _, err := os.Stat(boardRecordPath(f.home.State)); err != nil {
		t.Fatalf("the live supervisor's record was removed: %v", err)
	}
	if len(*killed) != 0 {
		t.Fatalf("killed %v, want nothing ended", *killed)
	}
}

// Two goblins started at once each start a supervisor. The one that loses
// either finds serve.log held by the winner's start, or exits finding the
// winner holding the watcher lock, before the winner has written its board
// record. Either way that goblins waits for the winner's board.
func TestGoblinsWaitsForTheSupervisorAnotherGoblinsStartedAtTheSameMoment(t *testing.T) {
	for name, start := range map[string]func(t *testing.T, h home.Home) (<-chan struct{}, error){
		"its serve.log is held": func(*testing.T, home.Home) (<-chan struct{}, error) {
			return nil, &os.PathError{Op: "open", Path: "serve.log", Err: syscall.Errno(32)}
		},
		"its serve exits while the other's still takes over from a watcher": func(t *testing.T, h home.Home) (<-chan struct{}, error) {
			watcher := startLiveForeignProcess(t)
			if _, err := lock.AcquireNamedOwner(h.State, ".watch.lock", watcher.Process.Pid, "watch"); err != nil {
				t.Fatal(err)
			}
			askForTheLockAs(t, h.State, startLiveForeignProcess(t).Process.Pid)
			exited := make(chan struct{})
			close(exited)
			return exited, nil
		},
		"its serve exits first": func(t *testing.T, h home.Home) (<-chan struct{}, error) {
			winner := startLiveForeignProcess(t)
			if _, err := lock.AcquireNamedOwner(h.State, ".watch.lock", winner.Process.Pid, "exclusive-spawn"); err != nil {
				t.Fatal(err)
			}
			exited := make(chan struct{})
			close(exited)
			return exited, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			board := fakeBoard(t, busySnapshot)
			var f *launcherFixture
			f = newLauncherFixture(t, func(h home.Home) (<-chan struct{}, error) {
				exited, err := start(t, h)
				go func() {
					time.Sleep(700 * time.Millisecond)
					if err := writeBoardRecord(h.State, boardRecord{PID: fakeBoardPID, URL: board}); err != nil {
						t.Error(err)
					}
				}()
				return exited, err
			})

			exit, stdout, stderr := f.launch()

			if exit != 0 || !strings.Contains(stdout, board) {
				t.Fatalf("exit=%d stdout=%q stderr=%q, want the other goblins's board", exit, stdout, stderr)
			}
		})
	}
}

// When serve.log was held, as by another goblins's serve that then failed,
// and no board comes, goblins says it started none because of that and still
// shows the end of serve.log, where the other serve said why it failed.
func TestGoblinsNamesTheHeldServeLogAndShowsItsEndWhenNoSupervisorStarts(t *testing.T) {
	defer func(timeout, poll time.Duration) { launcherStartTimeout, launcherPoll = timeout, poll }(launcherStartTimeout, launcherPoll)
	launcherStartTimeout, launcherPoll = 200*time.Millisecond, 20*time.Millisecond
	f := newLauncherFixture(t, func(h home.Home) (<-chan struct{}, error) {
		if err := os.WriteFile(serveLogPath(h.State), []byte("supervisor: existing watch owner must finish before serve: held\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return nil, &os.PathError{Op: "open", Path: "serve.log", Err: syscall.Errno(32)}
	})

	exit, _, stderr := f.launch()

	if exit != 1 || !strings.Contains(stderr, "was not started, because another process held") || !strings.Contains(stderr, "existing watch owner must finish before serve: held") {
		t.Fatalf("exit=%d stderr=%q, want the held serve.log named and the other serve's failure shown", exit, stderr)
	}
}
