//go:build !windows

package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/localsocket"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// leaveNoSocketFolder removes the socket folder of state once the test ends:
// it sits outside the test's temporary folder.
func leaveNoSocketFolder(t *testing.T, state string) {
	t.Helper()
	folder := localsocket.Folder(state)
	t.Cleanup(func() { _ = os.RemoveAll(folder) })
}

// dialRunSocketForTest connects to the run request socket for state as a
// client that sends nothing of its own.
func dialRunSocketForTest(t *testing.T, state string) *net.UnixConn {
	t.Helper()
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(25 * time.Millisecond) {
		conn, err := dialRunSocket(state, 0)
		if err == nil {
			return conn
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
	}
}

// squatSocket serves the run request socket for state before any supervisor
// does, the way another process of this user could, and returns its listener
// and what its first client sends it, or nil once the listener closes with no
// client.
func squatSocket(t *testing.T, state string) (net.Listener, <-chan []byte) {
	t.Helper()
	leaveNoSocketFolder(t, state)
	path := runSocketPath(state)
	if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	received := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			received <- nil
			return
		}
		defer conn.Close()
		data, _ := io.ReadAll(io.LimitReader(conn, 64<<10))
		received <- data
	}()
	return listener, received
}

// The socket's folder is this user's own and nobody else may enter it, and
// the socket sits in it.
func TestTheRunSocketSitsInAFolderOnlyThisUserMayEnter(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	leaveNoSocketFolder(t, h.State)

	// Act
	runPipe(t, &Service{Store: store})
	conn := dialRunSocketForTest(t, h.State)
	_ = conn.Close()

	// Assert
	info, err := os.Lstat(localsocket.Folder(h.State))
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) || info.Mode().Perm() != 0o700 {
		t.Fatalf("the socket folder is %s, want this user's own folder with mode 0700", info.Mode())
	}
	if info, err := os.Lstat(runSocketPath(h.State)); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("the run request socket = %v, %v, want a socket", info, err)
	}
}

// A folder of the socket's name that others may enter is not used: under
// /tmp anyone may make it first.
func TestTheRunSocketIsNotServedInAFolderOthersMayEnter(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	leaveNoSocketFolder(t, h.State)
	folder := localsocket.Folder(h.State)
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: store, subscribers: map[chan struct{}]struct{}{}}

	// Act
	s.serveRunRequests(context.Background())
	_, err := askPipe(h.State, runPipeRequest{Kind: "reported"})

	// Assert
	if issues := s.Store.Snapshot().Issues; !slices.ContainsFunc(issues, func(issue string) bool { return strings.Contains(issue, "not this user's own with mode 0700") }) {
		t.Fatalf("issues = %q, want the board to say the socket folder is not this user's own", issues)
	}
	if err == nil {
		t.Fatal("askPipe through a folder others may enter = nil, want it refused")
	}
}

// A request from a process outside the registered CEO's tree reaches the
// supervisor, which tells its process from the socket and refuses it before
// anything is written.
func TestARunRequestFromOutsideTheCEOTreeIsRefusedOverTheSocket(t *testing.T) {
	// Arrange: the registered CEO is a process of its own, which this test
	// does not run under.
	store, h := testStore(t)
	leaveNoSocketFolder(t, h.State)
	ceo := exec.Command("sleep", "120")
	if err := ceo.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ceo.Process.Kill()
		_ = ceo.Wait()
	})
	entries, err := proc.Ancestry(ceo.Process.Pid, 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the stand-in CEO's start time: %v %v", entries, err)
	}
	process, err := registeredProcess(entries[0], "")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(primaryRegistration{Host: "ceo", Agent: "codex", Process: process})
	if err := os.WriteFile(filepath.Join(h.State, "primary.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	runPipe(t, &Service{Store: store, Options: Options{CEO: &CEOConnection{State: h.State}}})

	// Act
	err = sendPipeRequest(h.State, runPipeRequest{ID: "install-tool", Title: "Install the tool", Shell: "bash", Command: "echo tool\n"})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "does not run under the registered CEO") {
		t.Fatalf("a run request from outside the CEO's tree = %v, want it refused", err)
	}
	if runs := store.Snapshot().Runs; len(runs) != 0 {
		t.Fatalf("runs = %+v after a refused request, want none", runs)
	}
}

// A goblin's report has the supervisor's loop look at once.
func TestAReportHasTheSupervisorLookAtOnce(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	leaveNoSocketFolder(t, h.State)
	looks := make(chan chan struct{})
	runPipe(t, &Service{Store: store, looks: looks})
	conn := dialRunSocketForTest(t, h.State)
	_ = conn.Close()

	// Act
	go Reported(h.State)

	// Assert
	select {
	case looked := <-looks:
		close(looked)
	case <-time.After(time.Minute):
		t.Fatal("the supervisor's loop was not asked to look after a report")
	}
}

// A client the supervisor does not answer in time gives up once its time for
// the answer has run out.
func TestARunRequestOverTheSocketGivesUpOnAnAnswerThatDoesNotComeInTime(t *testing.T) {
	// Arrange: the supervisor's loop never takes the look the request asks
	// for, so the supervisor answers only once its own bound on the work runs
	// out.
	store, h := testStore(t)
	leaveNoSocketFolder(t, h.State)
	shorten(t, &runReplyTimeout, time.Second)
	runPipe(t, &Service{Store: store, looks: make(chan chan struct{})})

	// Act
	_, err := askPipe(h.State, runPipeRequest{Kind: "reported"})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "did not answer the run request within 1s") {
		t.Fatalf("askPipe = %v, want it to give up on the answer within 1s, before the supervisor's own %s bound on the work answers", err, runRequestTimeout)
	}
}

// A client that connects and sends nothing is disconnected once its time to
// send has passed.
func TestTheRunSocketDropsAClientThatSendsNothing(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	leaveNoSocketFolder(t, h.State)
	shorten(t, &runReadTimeout, time.Second)
	runPipe(t, &Service{Store: store})
	conn := dialRunSocketForTest(t, h.State)
	defer conn.Close()

	// Act
	read := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 1))
		read <- err
	}()

	// Assert
	select {
	case err := <-read:
		if err == nil {
			t.Fatal("a client that sent nothing got a reply, want it disconnected")
		}
	case <-time.After(time.Minute):
		t.Fatal("a client that sent nothing is still connected, want it disconnected")
	}
}

// The listener stops promptly when its context ends right after a client
// connected, and lets its socket go for the next supervisor.
func TestTheRunSocketStopsPromptlyRightAfterAClientConnects(t *testing.T) {
	store, h := testStore(t)
	leaveNoSocketFolder(t, h.State)
	for range 50 {
		// Arrange
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			(&Service{Store: store}).serveRunRequests(ctx)
		}()
		conn := dialRunSocketForTest(t, h.State)

		// Act
		cancel()

		// Assert
		select {
		case <-done:
		case <-time.After(time.Minute):
			t.Fatal("the run request listener did not stop after its context ended")
		}
		_ = conn.Close()
	}
}

// A supervisor that finds its socket served by another process does not
// serve run requests beside it, and says why.
func TestTheRunSocketIsNotServedWhenAnotherProcessServesIt(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	squatSocket(t, h.State)
	s := &Service{Store: store, subscribers: map[chan struct{}]struct{}{}}

	// Act
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		defer close(done)
		s.serveRunRequests(ctx)
	}()

	// Assert
	select {
	case <-done:
	case <-time.After(firstInstanceWait + time.Minute):
		t.Fatal("the supervisor is serving run requests on a socket another process serves")
	}
	if issues := s.Store.Snapshot().Issues; !slices.ContainsFunc(issues, func(issue string) bool { return strings.Contains(issue, "already serves the socket") }) {
		t.Fatalf("issues = %q, want the board to say another process serves the socket", issues)
	}
}

// goblins run-request sends its command only to the process that holds this
// home's watch lock: a socket served by anything else never sees it.
func TestARunRequestIsNotSentToASocketTheSupervisorDoesNotServe(t *testing.T) {
	// Arrange
	_, h := testStore(t)
	_, received := squatSocket(t, h.State)
	if lock.HeldByNamed(h.State, ".watch.lock", os.Getpid()) {
		t.Fatal("this test process holds the watch lock, so it would be this home's supervisor")
	}

	// Act
	err := sendPipeRequest(h.State, runPipeRequest{ID: "install-tool", Title: "Install the tool", Shell: "bash", Command: "echo secret-plan\n"})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "not this home's supervisor") {
		t.Fatalf("a request to a squatted socket = %v, want it refused", err)
	}
	select {
	case data := <-received:
		if len(data) != 0 {
			t.Fatalf("the squatter received %q", data)
		}
	case <-time.After(time.Minute):
		t.Fatal("the client never closed its connection to the squatter")
	}
}

// The asking side refuses a folder others may enter too: a request is never
// sent through it, even to the home's supervisor.
func TestARunRequestIsNotSentThroughAFolderOthersMayEnter(t *testing.T) {
	// Arrange: this test process serves the socket and holds the home's watch
	// lock, so it passes as the supervisor, but others may enter its folder.
	_, h := testStore(t)
	listener, received := squatSocket(t, h.State)
	if err := os.Chmod(localsocket.Folder(h.State), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := lock.AcquireExclusiveNamed(h.State, ".watch.lock"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.ReleaseExclusiveNamed(h.State, ".watch.lock") })

	// Act
	err := sendPipeRequest(h.State, runPipeRequest{ID: "install-tool", Title: "Install the tool", Shell: "bash", Command: "echo secret-plan\n"})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "not this user's own with mode 0700") {
		t.Fatalf("a request through a folder others may enter = %v, want it refused, saying why", err)
	}
	_ = listener.Close()
	select {
	case data := <-received:
		if len(data) != 0 {
			t.Fatalf("the socket in a folder others may enter received %q", data)
		}
	case <-time.After(time.Minute):
		t.Fatal("the client is still connected to the socket in a folder others may enter")
	}
}

// A socket file nothing answers is a stopped supervisor's: the next
// supervisor replaces it and serves run requests.
func TestTheRunSocketReplacesAStoppedSupervisorsSocket(t *testing.T) {
	// Arrange: a supervisor that stopped without letting its socket go left
	// the socket file behind.
	store, h := testStore(t)
	leaveNoSocketFolder(t, h.State)
	path := runSocketPath(h.State)
	if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	stopped, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stopped.SetUnlinkOnClose(false)
	_ = stopped.Close()
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("the stopped supervisor's socket file = %v, %v, want a socket", info, err)
	}
	looks := make(chan chan struct{})
	s := &Service{Store: store, looks: looks, subscribers: map[chan struct{}]struct{}{}}
	runPipe(t, s)
	conn := dialRunSocketForTest(t, h.State)
	_ = conn.Close()

	// Act
	go Reported(h.State)

	// Assert
	select {
	case looked := <-looks:
		close(looked)
	case <-time.After(time.Minute):
		t.Fatal("the supervisor's loop was not asked to look after a report")
	}
	if issues := s.Store.Snapshot().Issues; slices.ContainsFunc(issues, func(issue string) bool { return strings.Contains(issue, "already serves the socket") }) {
		t.Fatalf("issues = %q, want no word of another process serving a stopped supervisor's socket", issues)
	}
}

// A client that asks before the supervisor has made its socket's folder
// waits for it, as a request does when it starts with goblins serve.
func TestARunRequestWaitsForASupervisorThatIsStarting(t *testing.T) {
	// Arrange: nothing has made the socket's folder yet, and the client says
	// when it waits for it.
	store, h := testStore(t)
	leaveNoSocketFolder(t, h.State)
	if _, err := os.Lstat(localsocket.Folder(h.State)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the socket folder = %v, want none yet", err)
	}
	waiting := make(chan struct{}, 1)
	redialSleep = func(wait time.Duration) {
		select {
		case waiting <- struct{}{}:
		default:
		}
		time.Sleep(wait)
	}
	t.Cleanup(func() { redialSleep = time.Sleep })
	answered := make(chan error, 1)
	go func() {
		_, err := askPipe(h.State, runPipeRequest{Kind: "reported"})
		answered <- err
	}()
	select {
	case <-waiting:
	case <-time.After(time.Minute):
		t.Fatal("the client never waited for the supervisor's socket")
	}
	looks := make(chan chan struct{})

	// Act
	runPipe(t, &Service{Store: store, looks: looks})

	// Assert
	select {
	case looked := <-looks:
		close(looked)
	case <-time.After(time.Minute):
		t.Fatal("the request of a client that waited never reached the supervisor")
	}
	select {
	case err := <-answered:
		if err != nil {
			t.Fatalf("askPipe while the supervisor started = %v, want it answered", err)
		}
	case <-time.After(time.Minute):
		t.Fatal("the client that waited was never answered")
	}
}
