//go:build linux || darwin

package host

import (
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/localsocket"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// hostID is the terminal every host in these tests runs.
const hostID = "cg-host"

// hostRun is a host running in this process, and what Run returned once it
// has.
type hostRun struct {
	stateDir string
	record   Record
	ended    chan struct{}
	err      error
}

// runHost runs script under sh in a host under a fresh home, returns once
// the host has recorded itself, and closes the terminal when the test ends
// if it still runs.
func runHost(t *testing.T, script string) *hostRun {
	t.Helper()
	run := &hostRun{stateDir: t.TempDir(), ended: make(chan struct{})}
	t.Cleanup(func() { _ = os.Remove(localsocket.Folder(run.stateDir)) })
	go func() {
		run.err = Run(run.stateDir, Spec{ID: hostID, Args: []string{"sh", "-c", script}, Cols: 80, Rows: 24})
		close(run.ended)
	}()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if record, err := ReadRecord(run.stateDir, hostID); err == nil {
			run.record = record
			break
		}
		select {
		case <-run.ended:
			t.Fatalf("the host ended before it recorded itself: %v", run.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the host did not record itself within 30 seconds")
		}
	}
	t.Cleanup(func() {
		select {
		case <-run.ended:
			return
		default:
		}
		if client, err := Dial(run.record); err == nil {
			_ = client.CloseTerminal()
			_ = client.Close()
		}
		<-run.ended
	})
	return run
}

// wait waits for Run to return, and returns its error.
func (h *hostRun) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-h.ended:
		return h.err
	case <-time.After(30 * time.Second):
		t.Fatal("the host did not end within 30 seconds")
		return nil
	}
}

// watcher is a viewer of a host, reading its events as they come.
type watcher struct {
	client *Client
	mu     sync.Mutex
	output strings.Builder
	code   uint32
	exited chan struct{}
}

func watch(t *testing.T, record Record) *watcher {
	t.Helper()
	client, err := View(record)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	viewer := &watcher{client: client, exited: make(chan struct{})}
	go func() {
		for {
			event, err := client.Next()
			if err != nil {
				return
			}
			viewer.mu.Lock()
			viewer.output.Write(event.Output)
			if event.Exited {
				viewer.code = event.Code
				viewer.mu.Unlock()
				close(viewer.exited)
				return
			}
			viewer.mu.Unlock()
		}
	}()
	return viewer
}

// await waits until the viewer has seen pattern, and returns its first match.
func (w *watcher) await(t *testing.T, pattern string) []string {
	t.Helper()
	expression := regexp.MustCompile(pattern)
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		w.mu.Lock()
		match := expression.FindStringSubmatch(w.output.String())
		w.mu.Unlock()
		if match != nil {
			return match
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	t.Fatalf("the viewer did not see %q within 30 seconds; it saw %q", pattern, w.output.String())
	return nil
}

// exitCode waits until the viewer is told the terminal ended, and returns
// its program's exit code.
func (w *watcher) exitCode(t *testing.T) uint32 {
	t.Helper()
	select {
	case <-w.exited:
	case <-time.After(30 * time.Second):
		t.Fatal("the viewer was not told the terminal ended within 30 seconds")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.code
}

func TestAUnixHostTypesAViewersInputAndSendsItsOutputBack(t *testing.T) {
	// Arrange
	run := runHost(t, `read line; echo "got $line"`)
	viewer := watch(t, run.record)

	// Act
	if err := viewer.client.Input([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}

	// Assert
	viewer.await(t, `got hello`)
	if code := viewer.exitCode(t); code != 0 {
		t.Fatalf("the viewer was told exit code %d, not 0", code)
	}
	if err := run.wait(t); err != nil {
		t.Fatal(err)
	}
	end, err := ReadEnd(run.stateDir, hostID)
	if err != nil {
		t.Fatal(err)
	}
	if end.Code != 0 || end.Closed {
		t.Fatalf("the host recorded its end as %+v, not exit code 0 by itself", end)
	}
	if _, err := ReadRecord(run.stateDir, hostID); err == nil {
		t.Fatal("the host's record outlived it")
	}
}

func TestAUnixHostReadsItsScreenFromItsEmulator(t *testing.T) {
	// Arrange
	run := runHost(t, `printf 'on the screen\n'; read line`)

	// Act
	var rows []string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		var err error
		if rows, err = ReadScreen(run.record); err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(rows, func(row string) bool { return strings.Contains(row, "on the screen") }) {
			break
		}
	}

	// Assert
	if !slices.ContainsFunc(rows, func(row string) bool { return strings.Contains(row, "on the screen") }) {
		t.Fatalf("the screen reads %q, without what the program wrote", rows)
	}
	if len(rows) != 24 {
		t.Fatalf("the screen has %d rows, not the terminal's 24", len(rows))
	}
}

func TestAViewerResizesAUnixHostsTerminal(t *testing.T) {
	// Arrange
	run := runHost(t, `read line; stty size; read line`)
	viewer := watch(t, run.record)

	// Act
	if err := viewer.client.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	if err := viewer.client.Input([]byte("\n")); err != nil {
		t.Fatal(err)
	}

	// Assert
	viewer.await(t, `30 100`)
	rows, err := ReadScreen(run.record)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 30 {
		t.Fatalf("the screen has %d rows after the resize, not 30", len(rows))
	}
}

func TestAUnixHostsTerminalKnowsItsIDAndCarriesAProofItsRecordProves(t *testing.T) {
	// Arrange
	run := runHost(t, `echo "id=$GOBLINS_HOST_ID proof=$GOBLINS_HOST_PROOF"; read line`)
	viewer := watch(t, run.record)

	// Act
	match := viewer.await(t, `id=(\S+) proof=(\S+)`)

	// Assert
	if match[1] != hostID {
		t.Fatalf("the terminal names itself %q, not %q", match[1], hostID)
	}
	if !run.record.Proves(match[2]) {
		t.Fatal("the host's record does not prove the terminal's proof")
	}
	sums, err := Proofs(run.stateDir, hostID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(sums, run.record.ProofSum) {
		t.Fatalf("the terminal's kept proofs %v lack its own %s", sums, run.record.ProofSum)
	}
	if started, isRead := proc.StartTime(run.record.ChildPID); !isRead || !started.Equal(run.record.ChildStart) {
		t.Fatalf("the record names the program's start %v, not %v", run.record.ChildStart, started)
	}
}

func TestAUnixHostRecordsTheEndOfAProgramThatExitsAtOnce(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	t.Cleanup(func() { _ = os.Remove(localsocket.Folder(stateDir)) })

	// Act
	err := Run(stateDir, Spec{ID: hostID, Args: []string{"sh", "-c", "exit 7"}, Cols: 80, Rows: 24})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	end, err := ReadEnd(stateDir, hostID)
	if err != nil {
		t.Fatal(err)
	}
	if end.Code != 7 || end.ChildStart.IsZero() {
		t.Fatalf("the host recorded its end as %+v, not exit code 7 with the program's start", end)
	}
}

func TestASecondUnixHostOfARunningTerminalIsRefused(t *testing.T) {
	// Arrange
	run := runHost(t, `read line`)

	// Act
	err := Run(run.stateDir, Spec{ID: hostID, Args: []string{"sh", "-c", "exit 0"}, Cols: 80, Rows: 24})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "already runs") {
		t.Fatalf("a second host gave %v, not a refusal", err)
	}
}

func TestAViewerClosesAUnixHostsTerminal(t *testing.T) {
	// Arrange
	run := runHost(t, `sleep 600`)
	client, err := Dial(run.record)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Act
	if err := client.CloseTerminal(); err != nil {
		t.Fatal(err)
	}

	// Assert
	if err := run.wait(t); err != nil {
		t.Fatal(err)
	}
	end, err := ReadEnd(run.stateDir, hostID)
	if err != nil {
		t.Fatal(err)
	}
	if !end.Closed {
		t.Fatalf("the host recorded its end as %+v, not closed by a viewer", end)
	}
	if _, err := ReadScreen(run.record); err == nil || !strings.HasPrefix(err.Error(), "host: read the screen of terminal "+hostID+": ") {
		t.Fatalf("a screen read of the ended host gave %v", err)
	}
}

func TestAClientRefusesASocketItsRecordsHostDoesNotServe(t *testing.T) {
	// Arrange
	run := runHost(t, `read line`)
	other := run.record
	other.HostPID = os.Getppid()

	// Act
	_, err := Dial(other)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "is not served by host pid") {
		t.Fatalf("a dial of a socket another process serves gave %v", err)
	}
}

func TestAUnixHostRefusesAStreamSession(t *testing.T) {
	// Act
	err := Run(t.TempDir(), Spec{ID: hostID, Args: []string{"sh", "-c", "exit 0"}, IsStream: true})

	// Assert
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("a stream session gave %v, not unsupported", err)
	}
}
