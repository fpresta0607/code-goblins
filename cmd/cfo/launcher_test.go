package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// fakeBoard answers /api/snapshot the way cfo serve does and returns its
// address.
func fakeBoard(t *testing.T, snapshot string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/snapshot" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(snapshot))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

type launcherFixture struct {
	t         *testing.T
	home      home.Home
	project   string
	starts    int
	opened    []string
	cfo       herdr.Endpoint
	cfoLive   bool
	nativeCFO string
	focused   []herdr.Endpoint
	cfoStarts []string
	attached  []string
	// nativeStarts are the projects a CFO was started in natively, and
	// nativeAttached the native terminals shown in this terminal.
	nativeStarts   []string
	nativeAttached []string
	runtime        commandRuntime
}

// newLauncherFixture runs goblins against an isolated home whose supervisor
// start is start, recording every start and every page it would open.
func newLauncherFixture(t *testing.T, start func(home.Home) (<-chan struct{}, error)) *launcherFixture {
	t.Helper()
	dir := t.TempDir()
	h := home.Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	// A live CFO by default, so a supervisor test prints only the banner.
	f := &launcherFixture{t: t, home: h, cfoLive: true}
	f.runtime = commandRuntime{
		resolveHome: func() (home.Home, error) { return h, nil },
		goblins:     true,
		startServe: func(h home.Home) (<-chan struct{}, error) {
			f.starts++
			return start(h)
		},
		openURL: func(target string) error {
			f.opened = append(f.opened, target)
			return nil
		},
		nativeCFO: func(string) (string, bool) {
			return f.nativeCFO, f.nativeCFO != ""
		},
		liveCFO: func(stateDir string) (herdr.Endpoint, bool) {
			if stateDir != h.State {
				t.Errorf("liveCFO read %s, want the fixture home's state %s", stateDir, h.State)
			}
			return f.cfo, f.cfoLive
		},
		focusCFO: func(_ context.Context, endpoint herdr.Endpoint) error {
			f.focused = append(f.focused, endpoint)
			return nil
		},
		gitTop: func(context.Context) (string, error) { return f.project, nil },
		stdin:  strings.NewReader(""),
		startCFO: func(_ context.Context, project string) (bool, error) {
			f.cfoStarts = append(f.cfoStarts, project)
			return true, nil
		},
		attachHerdr: func(session string) int {
			f.attached = append(f.attached, session)
			return 0
		},
		startNativeCFO: func(_, project string) error {
			f.nativeStarts = append(f.nativeStarts, project)
			return nil
		},
		attachNative: func(_, id string, _, _ io.Writer) int {
			f.nativeAttached = append(f.nativeAttached, id)
			return 0
		},
	}
	f.project = filepath.Join(dir, "project")
	// A goblin running these tests sits in a Herdr pane itself; each test
	// says where it is and which session is the fleet's instead.
	t.Setenv("HERDR_PANE_ID", "")
	t.Setenv("HERDR_SESSION", "fixture-fleet")
	return f
}

func (f *launcherFixture) launch(args ...string) (int, string, string) {
	f.t.Helper()
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime(args, &stdout, &stderr, f.runtime)
	return exit, stdout.String(), stderr.String()
}

func (f *launcherFixture) record(board string) {
	f.t.Helper()
	if err := writeBoardRecord(f.home.State, boardRecord{PID: 4242, URL: board}); err != nil {
		f.t.Fatal(err)
	}
}

const busySnapshot = `{"registration":"","tasks":[{"phase":"working"},{"phase":"working"},{"phase":"waiting"}],"questions":[{"status":"pending"},{"status":"succeeded"}],"reviews":[{"state":"open"},{"state":"answered"}],"runs":[{"state":"ready"},{"state":"succeeded"}]}`

// With a supervisor already serving, goblins starts nothing and opens
// nothing: it prints the board's link and what the fleet is doing.
func TestGoblinsFindsTheRunningSupervisorAndOnlyPrintsItsLink(t *testing.T) {
	f := newLauncherFixture(t, func(home.Home) (<-chan struct{}, error) {
		t.Fatal("goblins started a second supervisor")
		return nil, nil
	})
	board := fakeBoard(t, busySnapshot)
	f.record(board)

	exit, stdout, stderr := f.launch()

	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	if want := renderBanner(false, board, "CFO supervising · 2 goblins working · 3 waiting on you"); stdout != want {
		t.Fatalf("stdout =\n%s\nwant\n%s", stdout, want)
	}
	if f.starts != 0 || len(f.opened) != 0 {
		t.Fatalf("starts=%d opened=%q, want neither", f.starts, f.opened)
	}
}

// A supervisor whose board answers but cannot read the fleet's state is still
// the one supervisor: goblins prints its link, says so, and starts and opens
// nothing.
func TestGoblinsFindsASupervisorWhoseSnapshotFails(t *testing.T) {
	f := newLauncherFixture(t, func(home.Home) (<-chan struct{}, error) {
		t.Fatal("goblins started a second supervisor")
		return nil, nil
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "wake record is malformed", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	f.record(server.URL)

	exit, stdout, stderr := f.launch()

	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	if want := renderBanner(false, server.URL, "the board is up but could not read the fleet's state (HTTP 503)"); stdout != want {
		t.Fatalf("stdout =\n%s\nwant\n%s", stdout, want)
	}
	if f.starts != 0 || len(f.opened) != 0 {
		t.Fatalf("starts=%d opened=%q, want neither", f.starts, f.opened)
	}
}

// With no supervisor, goblins starts one, waits for its board, and opens the
// board once; the next goblins finds it and only prints the link.
func TestGoblinsStartsTheSupervisorAndOpensTheBoardOnce(t *testing.T) {
	board := fakeBoard(t, `{"registration":"no CFO has registered yet"}`)
	var f *launcherFixture
	f = newLauncherFixture(t, func(h home.Home) (<-chan struct{}, error) {
		if err := writeBoardRecord(h.State, boardRecord{PID: 4242, URL: board}); err != nil {
			t.Fatal(err)
		}
		return make(chan struct{}), nil
	})

	exit, stdout, stderr := f.launch()

	if exit != 0 || f.starts != 1 {
		t.Fatalf("exit=%d starts=%d stderr=%q, want one start", exit, f.starts, stderr)
	}
	if !strings.Contains(stdout, "  board   "+board+"\n") || !strings.Contains(stdout, "  status  CFO not connected · 0 goblins working · 0 waiting on you\n") {
		t.Fatalf("stdout = %q, want the board line and the status line", stdout)
	}
	if len(f.opened) != 1 || f.opened[0] != board {
		t.Fatalf("opened %q, want the board once", f.opened)
	}

	if exit, _, stderr := f.launch(); exit != 0 || f.starts != 1 || len(f.opened) != 1 {
		t.Fatalf("second launch: exit=%d starts=%d opened=%q stderr=%q, want no second start or tab", exit, f.starts, f.opened, stderr)
	}
}

// A supervisor that exits without serving, such as one refused the singleton
// while the legacy watcher holds it, is reported with the end of its log.
func TestGoblinsReportsASupervisorThatExitsWithoutServing(t *testing.T) {
	f := newLauncherFixture(t, func(h home.Home) (<-chan struct{}, error) {
		if err := os.WriteFile(serveLogPath(h.State), []byte("supervisor: session lock held by a live process\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		exited := make(chan struct{})
		close(exited)
		return exited, nil
	})

	began := time.Now()
	exit, stdout, stderr := f.launch()

	if exit != 1 || !strings.Contains(stderr, "the supervisor did not start") || !strings.Contains(stderr, "session lock held by a live process") {
		t.Fatalf("exit=%d stderr=%q, want the failure with the log's end", exit, stderr)
	}
	if waited := time.Since(began); waited > launcherStartTimeout/2 {
		t.Fatalf("goblins waited %s, want it to notice the exit instead of waiting out %s", waited, launcherStartTimeout)
	}
	if stdout != "" || len(f.opened) != 0 {
		t.Fatalf("stdout=%q opened=%q, want nothing printed or opened", stdout, f.opened)
	}
}

// A supervisor that never answers is given up on once the wait runs out.
func TestGoblinsGivesUpOnASupervisorThatNeverAnswers(t *testing.T) {
	defer func(timeout, poll time.Duration) { launcherStartTimeout, launcherPoll = timeout, poll }(launcherStartTimeout, launcherPoll)
	launcherStartTimeout, launcherPoll = 200*time.Millisecond, 20*time.Millisecond
	f := newLauncherFixture(t, func(home.Home) (<-chan struct{}, error) {
		return make(chan struct{}), nil
	})

	if exit, _, stderr := f.launch(); exit != 1 || !strings.Contains(stderr, "the supervisor did not start") {
		t.Fatalf("exit=%d stderr=%q, want a failure once the wait runs out", exit, stderr)
	}
}

// A record whose address no longer answers was left by a supervisor that
// ended without removing it, so goblins starts a new one.
func TestGoblinsReplacesAStaleBoardRecord(t *testing.T) {
	gone := httptest.NewServer(http.NotFoundHandler())
	stale := gone.URL
	gone.Close()
	board := fakeBoard(t, busySnapshot)
	f := newLauncherFixture(t, func(h home.Home) (<-chan struct{}, error) {
		if err := writeBoardRecord(h.State, boardRecord{PID: 5151, URL: board}); err != nil {
			t.Fatal(err)
		}
		return make(chan struct{}), nil
	})
	f.record(stale)

	exit, stdout, stderr := f.launch()

	if exit != 0 || f.starts != 1 || !strings.Contains(stdout, board) {
		t.Fatalf("exit=%d starts=%d stdout=%q stderr=%q, want a new supervisor started", exit, f.starts, stdout, stderr)
	}
}

// The record is read from disk and its URL is fetched and may be opened in a
// browser, so anything but a plain loopback board address is refused.
func TestBoardRecordAcceptsOnlyALoopbackBoardAddress(t *testing.T) {
	stateDir := t.TempDir()
	for address, valid := range map[string]bool{
		"http://127.0.0.1:4310":        true,
		"http://[::1]:4310":            true,
		"http://192.0.2.10:4310":       false,
		"https://127.0.0.1:4310":       false,
		"http://127.0.0.1:4310/phish":  false,
		"http://127.0.0.1:4310?x=1":    false,
		"http://user@127.0.0.1:4310":   false,
		"http://localhost:4310":        false,
		"http://127.0.0.1":             false,
		"javascript:alert(1)":          false,
		"file:///C:/Windows/notepad":   false,
		"http://127.0.0.1:4310#anchor": false,
	} {
		if err := writeBoardRecord(stateDir, boardRecord{PID: 1, URL: address}); err != nil {
			t.Fatal(err)
		}
		if _, err := readBoardRecord(stateDir); (err == nil) != valid {
			t.Errorf("readBoardRecord(%q) error = %v, want valid=%v", address, err, valid)
		}
	}
}

// A supervisor removes the record only while it names its own pid.
func TestBoardRecordIsRemovedOnlyByItsOwnSupervisor(t *testing.T) {
	stateDir := t.TempDir()
	if err := writeBoardRecord(stateDir, boardRecord{PID: 7, URL: "http://127.0.0.1:4310"}); err != nil {
		t.Fatal(err)
	}

	removeBoardRecord(stateDir, 8)
	if _, err := readBoardRecord(stateDir); err != nil {
		t.Fatalf("another supervisor removed the record: %v", err)
	}
	removeBoardRecord(stateDir, 7)
	if _, err := os.Stat(boardRecordPath(stateDir)); !os.IsNotExist(err) {
		t.Fatalf("the record outlived its own supervisor: %v", err)
	}
}

// cfo alone is for scripts and keeps printing its usage.
func TestCfoAloneStillPrintsItsUsage(t *testing.T) {
	f := newLauncherFixture(t, func(home.Home) (<-chan struct{}, error) {
		t.Fatal("cfo alone started a supervisor")
		return nil, nil
	})
	f.runtime.goblins = false

	if exit, _, stderr := f.launch(); exit != 2 || !strings.Contains(stderr, "usage: cfo") {
		t.Fatalf("exit=%d stderr=%q, want the usage", exit, stderr)
	}
}

func TestStatusLineSpeaksTheBoardsWords(t *testing.T) {
	for name, c := range map[string]struct {
		snapshot launcherSnapshot
		want     string
	}{
		"a quiet fleet": {launcherSnapshot{}, "CFO supervising · 0 goblins working · 0 waiting on you"},
		"one goblin": {launcherSnapshot{Tasks: []struct {
			Phase string `json:"phase"`
		}{{Phase: "working"}}}, "CFO supervising · 1 goblin working · 0 waiting on you"},
		"a CFO the board misses": {launcherSnapshot{Registration: "no CFO has registered"}, "CFO not connected · 0 goblins working · 0 waiting on you"},
		"a run already finished": {launcherSnapshot{Runs: []struct {
			State string `json:"state"`
		}{{State: "failed"}}}, "CFO supervising · 0 goblins working · 0 waiting on you"},
		"a run the Overlord owes": {launcherSnapshot{Runs: []struct {
			State string `json:"state"`
		}{{State: "running"}}}, "CFO supervising · 0 goblins working · 1 waiting on you"},
	} {
		if got := statusLine(c.snapshot); got != c.want {
			t.Errorf("%s: statusLine = %q, want %q", name, got, c.want)
		}
	}
}

// consoleProbeVariable turns this test binary into a stand-in for cfo serve
// that reports the console it was given to the file the variable names.
const consoleProbeVariable = "CFO_TEST_CONSOLE_PROBE"

var (
	getConsoleProcessList = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleProcessList")
	getConsoleWindow      = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow")
	isWindowVisible       = syscall.NewLazyDLL("user32.dll").NewProc("IsWindowVisible")
)

// probeConsole writes how many processes share this process's console, none
// when it has no console, and whether that console shows a window.
func probeConsole(report string) int {
	processes := make([]uint32, 16)
	count, _, _ := getConsoleProcessList.Call(uintptr(unsafe.Pointer(&processes[0])), uintptr(len(processes)))
	window, _, _ := getConsoleWindow.Call()
	visible := false
	if window != 0 {
		shown, _, _ := isWindowVisible.Call(window)
		visible = shown != 0
	}
	if err := os.WriteFile(report, []byte(fmt.Sprintf("processes=%d visible=%t", count, visible)), 0o600); err != nil {
		return 1
	}
	return 0
}

// The supervisor goblins starts must have a hidden console of its own: with
// none, every console program it runs would open a window of its own, and
// with the terminal's, closing the terminal would end it.
func TestDetachedStartGivesAHiddenConsoleOfItsOwn(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "console.txt")
	t.Setenv(consoleProbeVariable, report)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	command, err := startDetached(executable, dir, filepath.Join(dir, "probe.log"))
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("the stand-in failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		_ = command.Process.Kill()
		t.Fatalf("the stand-in, pid %d, did not exit", command.Process.Pid)
	}

	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "processes=1 visible=false" {
		t.Fatalf("stand-in console: %s, want a hidden console only it is attached to", got)
	}
}
