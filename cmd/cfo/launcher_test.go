package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/onboarding"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// fakeBoard answers /api/snapshot the way cfo serve does and returns its
// address.
// fakeBoardPID is the supervisor every fake board answers as, the pid the
// fixture records.
const fakeBoardPID = 4242

func fakeBoard(t *testing.T, snapshot string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/alive" {
			_, _ = fmt.Fprintf(w, `{"pid":%d}`, fakeBoardPID)
			return
		}
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
	// harnesses are the harnesses each CFO start, in Herdr or native, was
	// asked to start.
	harnesses []string
	// cfoTerminalRuns is whether native terminal cfo's host answers.
	cfoTerminalRuns bool
	// setups are the quick start's agent steps as each launch asked for them.
	// They run the real steps over agents whose states are missing, ready
	// unless named, choosing agent wherever they ask, and remember what they
	// end on; setupErr ends them on no agent instead.
	setups   []agentSetup
	agent    string
	missing  []string
	setupErr error
	// screens are the final screens shown, each answered with answer, the
	// CFO's terminal unless a test says otherwise.
	screens []finalScreen
	answer  int
	// settleNotes are what the watch of a new native CFO's startup dialogs
	// says.
	settleNotes []string
	runtime     commandRuntime
}

// agentSetup is one run of the quick start's agent steps: the agent
// --harness named and whether goblins setup asked for the choice again.
type agentSetup struct {
	chosen string
	rerun  bool
}

// finalScreen is the screen a launch ended on: its title, its choices and
// the one Enter accepts.
type finalScreen struct {
	title    string
	choices  []onboarding.Choice
	selected int
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
	f := &launcherFixture{t: t, home: h, cfoLive: true, agent: "claude"}
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
		startCFO: func(_ context.Context, project, harness string) (bool, error) {
			f.cfoStarts = append(f.cfoStarts, project)
			f.harnesses = append(f.harnesses, harness)
			return true, nil
		},
		attachHerdr: func(session string) int {
			f.attached = append(f.attached, session)
			return 0
		},
		startNativeCFO: func(_ home.Home, project, harness string) error {
			f.nativeStarts = append(f.nativeStarts, project)
			f.harnesses = append(f.harnesses, harness)
			return nil
		},
		attachNative: func(_, id string, _, _ io.Writer) int {
			f.nativeAttached = append(f.nativeAttached, id)
			return 0
		},
		nativeTerminalRuns: func(_, id string) bool {
			return f.cfoTerminalRuns && id == supervisor.NativeCFOTerminal
		},
		setupAgent: func(ctx context.Context, stateDir, chosen string, rerun bool, _, _ io.Writer) (string, error) {
			f.setups = append(f.setups, agentSetup{chosen, rerun})
			if f.setupErr != nil {
				return "", f.setupErr
			}
			return rememberAgent(ctx, stateDir, chosen, rerun, onboarding.Flow{
				Detect: func(_ context.Context, id string) onboarding.Agent {
					if slices.Contains(f.missing, id) {
						return onboarding.Agent{ID: id, Name: id, State: onboarding.Missing, Reason: "Not installed"}
					}
					return onboarding.Agent{ID: id, Name: id, State: onboarding.Ready}
				},
				// The choice is answered with agent; a step for an agent that is
				// not ready is cancelled, as Escape at the choice would be.
				Choose: func(title string, _ []string, _ int) (int, error) {
					if strings.HasPrefix(title, "Choose the agent") {
						return slices.Index(onboarding.Agents, f.agent), nil
					}
					return 0, onboarding.ErrCancelled
				},
			})
		},
		settleCFO: func(context.Context, string, string) []string { return f.settleNotes },
		choose: func(_ io.Writer, title string, choices []onboarding.Choice, selected int) (int, error) {
			f.screens = append(f.screens, finalScreen{title, choices, selected})
			return f.answer, nil
		},
	}
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
	if err := writeBoardRecord(f.home.State, boardRecord{PID: fakeBoardPID, URL: board}); err != nil {
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
		if r.URL.Path == "/api/alive" {
			_, _ = fmt.Fprintf(w, `{"pid":%d}`, fakeBoardPID)
			return
		}
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

// With no supervisor, goblins starts one, waits for its board and prints its
// link, and opens no browser: the board opens only when it is chosen. The
// next goblins finds the supervisor and starts no second one.
func TestGoblinsStartsTheSupervisorAndNeverOpensTheBoardUnasked(t *testing.T) {
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
	if len(f.opened) != 0 {
		t.Fatalf("opened %q, want no browser opened unasked", f.opened)
	}

	if exit, _, stderr := f.launch(); exit != 0 || f.starts != 1 || len(f.opened) != 0 {
		t.Fatalf("second launch: exit=%d starts=%d opened=%q stderr=%q, want no second start and no tab", exit, f.starts, f.opened, stderr)
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

// goblins --board starts the supervisor when none runs and opens the board,
// and starts, shows and attaches no CFO, which the board leaves to its own
// first-run screen; run again, it finds the supervisor and opens the board
// again, since opening it is what it is run for.
func TestGoblinsBoardOpensTheBoardAndLeavesTheCFOToIt(t *testing.T) {
	board := fakeBoard(t, `{"registration":"no CFO has registered yet"}`)
	f := newLauncherFixture(t, func(h home.Home) (<-chan struct{}, error) {
		if err := writeBoardRecord(h.State, boardRecord{PID: 4242, URL: board}); err != nil {
			t.Fatal(err)
		}
		return make(chan struct{}), nil
	})
	f.cfoLive = false

	exit, stdout, stderr := f.launch("--board")

	if exit != 0 || f.starts != 1 {
		t.Fatalf("exit=%d starts=%d stderr=%q, want one start", exit, f.starts, stderr)
	}
	if !strings.Contains(stdout, "  board   "+board+"\n") {
		t.Fatalf("stdout = %q, want the board line", stdout)
	}
	if !slices.Equal(f.opened, []string{board}) {
		t.Fatalf("opened %q, want the board once", f.opened)
	}
	if len(f.cfoStarts)+len(f.nativeStarts)+len(f.focused)+len(f.attached)+len(f.nativeAttached) != 0 {
		t.Fatalf("CFO starts=%q native=%q focused=%v attached=%q native attached=%q, want none", f.cfoStarts, f.nativeStarts, f.focused, f.attached, f.nativeAttached)
	}

	if exit, _, stderr := f.launch("--board"); exit != 0 || f.starts != 1 || !slices.Equal(f.opened, []string{board, board}) {
		t.Fatalf("second launch: exit=%d starts=%d opened=%q stderr=%q, want the running board opened again", exit, f.starts, f.opened, stderr)
	}
}

// goblins --board is run to open the board, so a board it cannot open is a
// failure that names the link to open by hand.
func TestGoblinsBoardFailsWhenTheBoardCannotBeOpened(t *testing.T) {
	f := newLauncherFixture(t, func(home.Home) (<-chan struct{}, error) {
		t.Fatal("goblins --board started a second supervisor")
		return nil, nil
	})
	board := fakeBoard(t, busySnapshot)
	f.record(board)
	f.runtime.openURL = func(string) error { return errors.New("no browser is registered") }

	exit, _, stderr := f.launch("--board")

	if want := "goblins: open the board at " + board + " yourself (no browser is registered)\n"; exit != 1 || stderr != want {
		t.Fatalf("exit=%d stderr=%q, want 1 and %q", exit, stderr, want)
	}
}

// The board's address is the same every time: the usual one, or the one the
// person chose, and never another because that one is in use.
func TestTheBoardAddressIsTheUsualOneUnlessAnotherIsChosen(t *testing.T) {
	tests := []struct {
		name   string
		chosen string
		want   string
	}{
		{"none chosen", "", "127.0.0.1:4310"},
		{"blank", "  ", "127.0.0.1:4310"},
		{"one chosen", "127.0.0.1:4311", "127.0.0.1:4311"},
		{"any free port, as a test asks", " 127.0.0.1:0 ", "127.0.0.1:0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			t.Setenv(boardAddressVariable, test.chosen)

			// Act
			got := boardAddress()

			// Assert
			if got != test.want {
				t.Errorf("boardAddress() = %q, want %q", got, test.want)
			}
		})
	}
}

// An address in use is told with who holds it: the Code Goblins fleet whose
// supervisor answers there, by its home and pid, or another program.
func TestABoardAddressInUseIsToldWithWhoHoldsIt(t *testing.T) {
	supervisorAnswering := func(alive string) func(t *testing.T) string {
		return func(t *testing.T) string {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/alive" {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(alive))
			}))
			t.Cleanup(server.Close)
			return strings.TrimPrefix(server.URL, "http://")
		}
	}
	tests := []struct {
		name   string
		holder func(t *testing.T) string
		want   []string
	}{
		{"a fleet that names its home", supervisorAnswering(`{"pid":4242,"home":"C:\\Fleet"}`), []string{`is in use by the Code Goblins fleet in C:\Fleet (supervisor pid 4242), so no second one was started`, "set CFO_HOME to its folder", "setting CFO_BOARD_ADDRESS"}},
		{"an older supervisor that names only its pid", supervisorAnswering(`{"pid":4242}`), []string{"is in use by another Code Goblins supervisor (pid 4242), so no second one was started", "goblins stop", "setting CFO_BOARD_ADDRESS"}},
		{"a web server that is no supervisor", supervisorAnswering(`<html>`), []string{"is in use by another program, so no supervisor was started", "setting CFO_BOARD_ADDRESS"}},
		{"a program that speaks no HTTP", func(t *testing.T) string {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			return listener.Addr().String()
		}, []string{"is in use by another program, so no supervisor was started", "setting CFO_BOARD_ADDRESS"}},
	}
	previous := aliveTimeout
	aliveTimeout = 500 * time.Millisecond
	t.Cleanup(func() { aliveTimeout = previous })
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			address := test.holder(t)

			// Act
			err := boardAddressFree(context.Background(), address)

			// Assert
			var taken boardAddressTaken
			if !errors.As(err, &taken) {
				t.Fatalf("boardAddressFree(%s) = %v, want the address reported in use", address, err)
			}
			for _, want := range append(test.want, "the board's address "+address+" ") {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to say %q", err, want)
				}
			}
		})
	}
}

// An address nothing listens on is free, and so is the port 0 a test or a
// scratch home asks for.
func TestAFreeBoardAddressIsNotReportedInUse(t *testing.T) {
	// Arrange
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := free.Addr().String()
	if err := free.Close(); err != nil {
		t.Fatal(err)
	}

	// Act and assert
	for _, address := range []string{address, "127.0.0.1:0"} {
		if err := boardAddressFree(context.Background(), address); err != nil {
			t.Errorf("boardAddressFree(%s) = %v, want it free", address, err)
		}
	}
}

// Starting the supervisor on an address something else holds starts nothing:
// no process, and so no serve.log. An address that is not a numeric loopback
// one is refused by its variable's name.
func TestStartingTheSupervisorOnAnAddressItCannotTakeStartsNothing(t *testing.T) {
	// Arrange
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	previous := aliveTimeout
	aliveTimeout = 500 * time.Millisecond
	t.Cleanup(func() { aliveTimeout = previous })
	tests := []struct {
		name    string
		address string
		want    string
	}{
		{"an address in use", held.Addr().String(), "the board's address " + held.Addr().String() + " is in use by another program"},
		{"an address that is not loopback", "0.0.0.0:4310", `CFO_BOARD_ADDRESS is "0.0.0.0:4310", not a numeric loopback address such as 127.0.0.1:4310`},
		{"an address with no port", "localhost", `CFO_BOARD_ADDRESS is "localhost", not a numeric loopback address such as 127.0.0.1:4310`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			h := home.Home{Root: dir, State: filepath.Join(dir, "state")}
			if err := os.MkdirAll(h.State, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv(boardAddressVariable, test.address)

			// Act
			exited, err := startDetachedServe(h)

			// Assert
			if err == nil || exited != nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("startDetachedServe = %v, %v; want nothing started and an error saying %q", exited, err, test.want)
			}
			if _, statErr := os.Stat(serveLogPath(h.State)); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("serve.log: %v, want none, since no supervisor was started", statErr)
			}
		})
	}
}

// goblins that finds its board's address held by another fleet says whose,
// and starts no board on another address and opens none.
func TestGoblinsStartsNoSecondBoardWhenItsAddressIsInUse(t *testing.T) {
	// Arrange
	f := newLauncherFixture(t, func(home.Home) (<-chan struct{}, error) {
		return nil, boardAddressTaken{address: "127.0.0.1:4310", cause: errors.New("bind: in use"), pid: 4242, home: `C:\Fleet`}
	})

	// Act
	began := time.Now()
	exit, stdout, stderr := f.launch()

	// Assert
	if exit != 1 || stdout != "" || len(f.opened) != 0 || len(f.cfoStarts)+len(f.nativeStarts) != 0 {
		t.Fatalf("exit=%d stdout=%q opened=%q cfoStarts=%q nativeStarts=%q, want nothing started or shown", exit, stdout, f.opened, f.cfoStarts, f.nativeStarts)
	}
	if want := "goblins: the board's address 127.0.0.1:4310 is in use by the Code Goblins fleet in C:\\Fleet (supervisor pid 4242), so no second one was started."; !strings.HasPrefix(stderr, want) {
		t.Errorf("stderr = %q, want it to start %q", stderr, want)
	}
	if waited := time.Since(began); waited > 20*time.Second {
		t.Errorf("goblins waited %s for a board that another fleet holds", waited)
	}
}

// The address held by this home's own supervisor, started a moment ago by
// another goblins and not yet recorded, is waited for: this goblins shows
// that board rather than refusing its own fleet.
func TestGoblinsWaitsForItsOwnSupervisorHoldingTheAddress(t *testing.T) {
	// Arrange
	board := fakeBoard(t, busySnapshot)
	var f *launcherFixture
	f = newLauncherFixture(t, func(h home.Home) (<-chan struct{}, error) {
		if err := writeBoardRecord(h.State, boardRecord{PID: fakeBoardPID, URL: board}); err != nil {
			t.Fatal(err)
		}
		return nil, boardAddressTaken{address: "127.0.0.1:4310", cause: errors.New("bind: in use"), pid: fakeBoardPID, home: strings.ToUpper(h.Root)}
	})

	// Act
	exit, stdout, stderr := f.launch()

	// Assert
	if exit != 0 || !strings.Contains(stdout, "  board   "+board+"\n") {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want this home's board shown", exit, stdout, stderr)
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
		if err := writeBoardRecord(h.State, boardRecord{PID: fakeBoardPID, URL: board}); err != nil {
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

// The status line counts what the Command Center's badge counts, from the
// board's own snapshot.
func TestStatusLineCountsWhatTheBadgeCounts(t *testing.T) {
	for name, c := range map[string]struct {
		snapshot string
		want     int
	}{
		"a question asked about its goblin's open review page is that page's one card": {`{"questions":[{"status":"pending","page":"waiting-billing-7"}],"reviews":[{"state":"open"}]}`, 1},
		"a question of its own and a page":                                             {`{"questions":[{"status":"pending"}],"reviews":[{"state":"open"}]}`, 2},
	} {
		// Arrange
		var snapshot launcherSnapshot
		if err := json.Unmarshal([]byte(c.snapshot), &snapshot); err != nil {
			t.Fatal(err)
		}

		// Act
		got := statusLine(snapshot)

		// Assert
		if want := fmt.Sprintf("CFO supervising · 0 goblins working · %d waiting on you", c.want); got != want {
			t.Errorf("%s: statusLine = %q, want %q", name, got, want)
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
