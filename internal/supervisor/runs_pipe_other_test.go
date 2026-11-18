//go:build !windows

package supervisor

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// leaveNoSocketFolder removes the socket folder of state once the test ends:
// it sits outside the test's temporary folder.
func leaveNoSocketFolder(t *testing.T, state string) {
	t.Helper()
	folder := socketFolder(state)
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
// does, the way another process of this user could, and returns what its
// clients send it.
func squatSocket(t *testing.T, state string) <-chan []byte {
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
			return
		}
		defer conn.Close()
		data, _ := io.ReadAll(io.LimitReader(conn, 64<<10))
		received <- data
	}()
	return received
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
	info, err := os.Lstat(socketFolder(h.State))
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || !isOwnedBy(info, os.Getuid()) || info.Mode().Perm() != 0o700 {
		t.Fatalf("the socket folder is %s, want this user's own folder with mode 0700", info.Mode())
	}
	if info, err := os.Lstat(runSocketPath(h.State)); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("the run request socket = %v, %v, want a socket", info, err)
	}
}

// A socket's path must fit sun_path, 104 bytes on macOS, whatever the length
// of the home it serves.
func TestTheRunSocketPathFitsSunPathForALongHome(t *testing.T) {
	// Arrange
	home := filepath.Join(t.TempDir(), strings.Repeat("a-long-folder-name/", 12), "state")

	// Act
	path := runSocketPath(home)

	// Assert
	if len(path) >= 104 {
		t.Fatalf("the socket path %s is %d bytes, want under 104", path, len(path))
	}
}

// A folder of the socket's name that others may enter is not used: under
// /tmp anyone may make it first.
func TestTheRunSocketIsNotServedInAFolderOthersMayEnter(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	leaveNoSocketFolder(t, h.State)
	folder := socketFolder(h.State)
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

// Each end of the socket reads the process at the other: here both are this
// test process.
func TestEachEndOfTheRunSocketReadsTheProcessAtTheOther(t *testing.T) {
	// Arrange: a test's temporary folder can overflow sun_path, so the socket
	// sits in a short folder under /tmp.
	folder, err := os.MkdirTemp("/tmp", "peer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(folder) })
	path := filepath.Join(folder, "peer.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan *net.UnixConn, 1)
	go func() {
		conn, _ := listener.AcceptUnix()
		accepted <- conn
	}()
	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := <-accepted
	if server == nil {
		t.Fatal("the listener accepted no connection")
	}
	defer server.Close()

	// Act
	serverSees, serverErr := peerProcess(server)
	clientSees, clientErr := peerProcess(client)

	// Assert
	if serverErr != nil || serverSees != os.Getpid() {
		t.Fatalf("the server reads its client as %d, %v, want %d", serverSees, serverErr, os.Getpid())
	}
	if clientErr != nil || clientSees != os.Getpid() {
		t.Fatalf("the client reads its server as %d, %v, want %d", clientSees, clientErr, os.Getpid())
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
	received := squatSocket(t, h.State)
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
