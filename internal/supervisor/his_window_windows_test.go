package supervisor

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/standin"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// asAWindowWhoseOpenerExited makes every request for one of the Overlord's
// switches read as one from his own desktop window after it restarted itself
// onto an update. The supervisor opens that window through the desktop shell,
// and the program that started it exits, so the window's parents stop at the
// window itself. It is the window he pressed the switch in on 2026-10-09:
// goblins-window.exe pid 22504, whose parent pid 21820 was gone. image is the
// program that window runs.
func asAWindowWhoseOpenerExited(s *Service, image string) {
	s.peerOf = func(netip.AddrPort, netip.AddrPort) (int, error) { return 7001, nil }
	s.inspectCaller = func(pid int) ([]proc.Entry, []string, error) {
		started := time.Now().Add(-2 * time.Hour)
		return []proc.Entry{
			{PID: pid, ParentPID: 6200, ExeBase: "msedgewebview2.exe", Start: started.Add(2 * time.Second)},
			{PID: 6200, ParentPID: 22504, ExeBase: "msedgewebview2.exe", Start: started.Add(time.Second)},
			{PID: 22504, ParentPID: 21820, ExeBase: "goblins-window.exe", Start: started},
		}, []string{"USERNAME=overlord"}, nil
	}
	s.windows = (&standInWindows{image: image}).system()
}

// "fix this issue i should alwyas be abel to turn on afk no issues" (the
// Overlord, 2026-10-09): he pressed the switch in the Code Goblins window
// itself and was refused, because that window had restarted itself onto
// v0.5.8 and its opener had exited. His own window turns AFK mode on and off
// whoever opened it.
func TestHisWindowSwitchesAFKModeOnAndOffAfterItRestartedItselfOntoAnUpdate(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := boardService(store)
	asAWindowWhoseOpenerExited(s, filepath.Join(h.Bin(), windowName))

	// Act
	onCode, onBody := askTheBoard(t, s, "POST", "/api/afk", `{"on":true}`, nil)
	on, onErr := afk.Read(h.State)
	offCode, offBody := askTheBoard(t, s, "POST", "/api/afk", `{"on":false}`, nil)
	off, offErr := afk.Read(h.State)

	// Assert
	if onCode != http.StatusOK || strings.TrimSpace(onBody) != `{"state":"on"}` {
		t.Fatalf("POST on from his window = %d %s, want AFK mode turned on", onCode, onBody)
	}
	if onErr != nil || !on.On || on.From != "his own board (goblins-window.exe pid 22504)" {
		t.Errorf("the switch after on = %+v, %v, want on from his own window", on, onErr)
	}
	if offCode != http.StatusOK || strings.TrimSpace(offBody) != `{"state":"off"}` {
		t.Fatalf("POST off from his window = %d %s, want AFK mode turned off", offCode, offBody)
	}
	if offErr != nil || off.On || off.EndedFrom != "his own board (goblins-window.exe pid 22504)" {
		t.Errorf("the switch after off = %+v, %v, want off from his own window", off, offErr)
	}
}

// The Update item is pressed on a board of his own, proven as the switch
// proves one, so it broke for the same window the same way.
func TestHisWindowPressesUpdateAfterItRestartedItselfOntoAnUpdate(t *testing.T) {
	// Arrange
	source := newReleaseSource(t, "v0.5.0")
	s := releaseService(t, source, "v0.4.2")
	s.checkReleases(context.Background())
	item := updateItems(s)[0]
	asAWindowWhoseOpenerExited(s, filepath.Join(s.Store.Home.Bin(), windowName))

	// Act
	status, body := askTheBoard(t, s, "POST", "/api/actions", `{"id":"press-1","kind":"run","run_id":"`+item.ID+`","generation":"`+item.Identity+`"}`, nil)

	// Assert
	if status != http.StatusAccepted {
		t.Fatalf("Update pressed in his window = %d %s, want it accepted", status, body)
	}
}

// A window the supervisor moved once is moved again by the next update: the
// page in it asks, and the supervisor takes it for his own window although
// the shell that opened it has exited.
func TestHisWindowMovesOntoTheNextUpdateAfterItRestartedItselfOntoOne(t *testing.T) {
	// Arrange
	s, machine, program := windowHome(t, func(bin string) string {
		return filepath.Join(bin, "goblins-window.exe.LU4BWTHK5ISXTSKSYN7HMYSJ32.old")
	})
	his := s.windows
	asAWindowWhoseOpenerExited(s, machine.image)
	s.windows = his

	// Act
	code, body := askTheBoard(t, s, "POST", "/api/window/move", `{"hidden":false}`, nil)

	// Assert
	if code != http.StatusOK || strings.TrimSpace(body) != `{"moved":true}` {
		t.Fatalf("POST /api/window/move from his window = %d %s, want the window moved", code, body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, opened := machine.seen(); len(opened) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, opened := machine.seen(); len(opened) != 1 || opened[0] != program {
		t.Errorf("opened = %q, want the home's window %s", opened, program)
	}
}

// boardAnswer is what the board answered one request.
type boardAnswer struct {
	code int
	body string
}

// realBoard serves a supervisor's board on a real connection, as cfo serve
// does. Each request waits for its opener to have exited before the board
// sees it, and what the board answers is handed to the test.
type realBoard struct {
	s        *Service
	address  string
	answered chan boardAnswer
	// exited is closed once the opener of the request under way has exited.
	mu     sync.Mutex
	exited chan struct{}
}

// opener is what a request waits on: closed once its opener has exited.
func (board *realBoard) opener() chan struct{} {
	board.mu.Lock()
	defer board.mu.Unlock()
	return board.exited
}

// open starts the wait of the next request and returns what ends it.
func (board *realBoard) open() chan struct{} {
	board.mu.Lock()
	defer board.mu.Unlock()
	board.exited = make(chan struct{})
	return board.exited
}
func serveRealBoard(t *testing.T, s *Service) *realBoard {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	board := &realBoard{s: s, address: listener.Addr().String(), answered: make(chan boardAnswer, 1)}
	served := NewHTTP(s, board.address, nil)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-board.opener()
		answer := httptest.NewRecorder()
		served.ServeHTTP(answer, r)
		w.WriteHeader(answer.Code)
		_, _ = w.Write(answer.Body.Bytes())
		board.answered <- boardAnswer{answer.Code, answer.Body.String()}
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return board
}

// press has program, a copy of Windows's own curl, ask the board to turn AFK
// mode on, started as the desktop shell starts the window: by a program that
// exits at once, so the program's parents stop at itself, with environment
// and nothing of this test's own, which runs under go test and often under a
// goblin's harness.
func (board *realBoard) press(t *testing.T, program string, environment ...string) boardAnswer {
	t.Helper()
	body := filepath.Join(t.TempDir(), "on.json")
	if err := os.WriteFile(body, []byte(`{"on":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	exited := board.open()
	opener := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/c", "start", "", "/b", program,
		"--silent", "--max-time", "60", "--request", "POST",
		"--header", "Origin: http://"+board.address, "--header", "X-CFO-Token: "+board.s.Instance, "--header", "Content-Type: application/json",
		"--data-binary", "@"+body, "http://"+board.address+"/api/afk")
	opener.Env = append([]string{"SystemRoot=" + os.Getenv("SystemRoot"), "PATH=" + filepath.Join(os.Getenv("SystemRoot"), "System32"), "USERNAME=overlord"}, environment...)
	opener.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	if err := opener.Run(); err != nil {
		t.Fatalf("the opener did not start %s: %v", program, err)
	}
	close(exited)
	select {
	case answer := <-board.answered:
		return answer
	case <-time.After(time.Minute):
		t.Fatalf("%s never asked the board", program)
		return boardAnswer{}
	}
}

// The whole proof over a real connection, with nothing standing in for
// Windows: a real program run from the home's bin under the window's name,
// whose opener has really exited, is read by the supervisor as Windows names
// it, by its image, its user and its session, and turns AFK mode on. The same
// program with a goblin's environment is refused, and so is the same program
// run under the window's name from another folder.
func TestAProgramWindowsNamesAsTheHomesWindowIsHisOverARealConnectionAfterItsOpenerExited(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := boardService(store)
	board := serveRealBoard(t, s)
	curl, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "curl.exe"))
	if err != nil {
		t.Fatalf("Windows's own curl stands in for the window's program here, and it could not be read: %v", err)
	}
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	standin.RemoveAtCleanup(t, elsewhere)
	standin.RemoveAtCleanup(t, h.Bin())
	for _, folder := range []string{h.Bin(), elsewhere} {
		if err := os.MkdirAll(folder, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, windowName), curl, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	window := filepath.Join(h.Bin(), windowName)

	// Act
	goblins := board.press(t, window, "CFO_ROLE=goblin")
	lookalike := board.press(t, filepath.Join(elsewhere, windowName))
	afterRefusals, readErr := afk.Read(h.State)
	his := board.press(t, window)

	// Assert
	if goblins.code != http.StatusForbidden || lookalike.code != http.StatusForbidden {
		t.Errorf("the window's program with a goblin's environment = %d %s, and the same program from another folder = %d %s, want both refused", goblins.code, goblins.body, lookalike.code, lookalike.body)
	}
	if readErr != nil || afterRefusals.On {
		t.Errorf("the switch after the two refusals = %+v, %v, want it still off", afterRefusals, readErr)
	}
	if his.code != http.StatusOK || strings.TrimSpace(his.body) != `{"state":"on"}` {
		t.Fatalf("the home's own window, its opener exited = %d %s, want AFK mode turned on", his.code, his.body)
	}
	if switched, err := afk.Read(h.State); err != nil || !switched.On || !strings.HasPrefix(switched.From, "his own board (goblins-window.exe pid ") {
		t.Errorf("the switch = %+v, %v, want on from his own window", switched, err)
	}
	pending, err := wake.Pending(h.State)
	if err != nil || len(pending) != 3 {
		t.Fatalf("the CFO's queue = %+v, %v, want the two refusals and the switch", pending, err)
	}
	if !strings.Contains(pending[0].Detail, "runs in a goblin's terminal") || !strings.Contains(pending[1].Detail, "nothing proves the program that shows this board is his") || !strings.Contains(pending[2].Detail, "turned AFK mode on from his own board") {
		t.Errorf("the CFO is told %q, then %q, then %q, want the goblin's refused, the lookalike refused and his switch", pending[0].Detail, pending[1].Detail, pending[2].Detail)
	}
}
