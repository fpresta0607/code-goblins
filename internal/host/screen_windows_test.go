package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/fpresta0607/code-goblins/internal/conpty"
)

// consoleProcesses is how many processes are attached to this process's
// console.
func consoleProcesses() int {
	pids := make([]uint32, 16)
	count, _, _ := kernel32.NewProc("GetConsoleProcessList").Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return int(count)
}

// obeyCtrlC has this process, and every process it starts from now on, end at
// a Ctrl-C as processes do by default, whatever started the test: a process in
// a new process group ignores Ctrl-C and passes that on to what it starts.
func obeyCtrlC(t *testing.T) {
	if ok, _, err := setConsoleCtrlHandler.Call(0, 0); ok == 0 {
		t.Fatalf("SetConsoleCtrlHandler: %v", err)
	}
}

// holdScreenReads keeps every screen read attached for hold before it reads,
// until the test ends. Call it before hosting a terminal in this process, so
// the host sees the hold from its start and has ended before it is lifted.
func holdScreenReads(t *testing.T, hold time.Duration) {
	screenHold = hold
	t.Cleanup(func() { screenHold = 0 })
}

// hostHere hosts terminal g1 in this test process, running echo-child, so a
// control event that reached the host would reach the test. It returns the
// record and a channel that closes once the host has ended.
func hostHere(t *testing.T) (string, Record, <-chan struct{}) {
	t.Helper()
	stateDir := t.TempDir()
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		_ = Run(stateDir, Spec{ID: "g1", Args: []string{os.Args[0], "echo-child"}, Cols: 80, Rows: 25})
	}()
	var record Record
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var err error
		if record, err = ReadRecord(stateDir, "g1"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the host never recorded itself")
		}
	}
	t.Cleanup(func() {
		if client, err := Dial(record); err == nil {
			_ = client.CloseTerminal()
			_ = client.Close()
		}
		select {
		case <-ended:
		case <-time.After(15 * time.Second):
			t.Error("the host did not end")
		}
	})
	return stateDir, record, ended
}

// waitForFile waits for the terminal's program to write path.
func waitForFile(t *testing.T, path string) []byte {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if data, err := os.ReadFile(path); err == nil {
			return data
		}
		if time.Now().After(deadline) {
			t.Fatalf("the terminal's program never wrote %s", path)
		}
	}
}

// The host reads its terminal's screen exactly as the terminal's program reads
// it from inside its own console, row for row.
func TestTheHostReadsTheScreenItsTerminalsProgramSees(t *testing.T) {
	_, record := launch(t)
	v := connect(t, record)
	v.waitFor(t, "ready")
	typeLine(t, v, "hello goblin")
	v.waitFor(t, "got hello goblin")
	inside := filepath.Join(t.TempDir(), "screen.json")
	typeLine(t, v, "screen "+inside)
	var want []string
	if err := json.Unmarshal(waitForFile(t, inside), &want); err != nil {
		t.Fatal(err)
	}

	rows, err := ReadScreen(record)

	if err != nil {
		t.Fatalf("ReadScreen: %v", err)
	}
	if !slices.Equal(rows, want) {
		t.Errorf("the host read\n%q\nthe program read\n%q", rows, want)
	}
	if len(rows) != 25 || utf8.RuneCountInString(rows[0]) != 80 {
		t.Fatalf("the host read %d rows, the first %d wide; want the 80x25 window", len(rows), utf8.RuneCountInString(rows[0]))
	}
	for i, text := range []string{"ready", "hello goblin", "got hello goblin"} {
		if row := strings.TrimRight(rows[i], " "); row != text {
			t.Errorf("row %d = %q, want %q", i, row, text)
		}
	}
}

// A Ctrl-C typed to the terminal while a screen read is attached to its
// console reaches the terminal's program and the read, never the host. The
// host here is this test process, which a Ctrl-C would end. The read finishes.
func TestACtrlCDuringAScreenReadNeverReachesTheHost(t *testing.T) {
	obeyCtrlC(t)
	holdScreenReads(t, 3*time.Second)
	_, record, _ := hostHere(t)
	v := connect(t, record)
	v.waitFor(t, "ready")
	read := make(chan error, 1)
	go func() {
		_, err := ReadScreen(record)
		read <- err
	}()
	typeLine(t, v, "hold-ctrl-c")
	v.waitFor(t, "attached 2")

	if err := v.Input([]byte{0x03}); err != nil {
		t.Fatalf("Input: %v", err)
	}

	v.waitFor(t, "interrupted")
	if err := <-read; err != nil {
		t.Errorf("the read the Ctrl-C reached failed: %v", err)
	}
	typeLine(t, v, "still here")
	v.waitFor(t, "got still here")
}

// The terminal's console closing while a screen read is attached to it ends
// only the read. The host, this test process, reports the terminal's end and
// removes its record, and the read is an error naming the terminal.
func TestTheConsoleClosingDuringAScreenReadNeverReachesTheHost(t *testing.T) {
	holdScreenReads(t, 5*time.Second)
	stateDir, record, ended := hostHere(t)
	v := connect(t, record)
	v.waitFor(t, "ready")
	read := make(chan error, 1)
	go func() {
		rows, err := ReadScreen(record)
		if err == nil {
			err = fmt.Errorf("read %d rows from a console that closed", len(rows))
		}
		read <- err
	}()
	typeLine(t, v, "wait-attach")
	v.waitFor(t, "attached 2")

	typeLine(t, v, "exit 3")

	if code := v.waitForExit(t); code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
	select {
	case <-ended:
	case <-time.After(15 * time.Second):
		t.Fatal("the host did not end with its terminal")
	}
	if _, err := ReadRecord(stateDir, "g1"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the record is still there: %v", err)
	}
	if err := <-read; !strings.Contains(err.Error(), "terminal g1") {
		t.Errorf("the read = %v, want an error naming terminal g1", err)
	}
}

// A close requested while a screen read is attached waits for the read, so
// the terminal's program, and with it the pid the read attached to, outlives
// the read.
func TestAScreenReadFinishesBeforeTheTerminalCloses(t *testing.T) {
	holdScreenReads(t, 2*time.Second)
	_, record, ended := hostHere(t)
	v := connect(t, record)
	v.waitFor(t, "ready")
	read := make(chan error, 1)
	go func() {
		_, err := ReadScreen(record)
		read <- err
	}()
	typeLine(t, v, "wait-attach")
	v.waitFor(t, "attached 2")

	if err := v.CloseTerminal(); err != nil {
		t.Fatalf("CloseTerminal: %v", err)
	}

	if err := <-read; err != nil {
		t.Errorf("the read = %v, want the screen, read before the terminal closed", err)
	}
	v.waitForExit(t)
	select {
	case <-ended:
	case <-time.After(15 * time.Second):
		t.Fatal("the host did not end after the read")
	}
}

// A terminal whose program has ended is never read, since the program's pid
// may by then name another process: the answer says the terminal has ended.
func TestAScreenOfAnEndedTerminalIsNotRead(t *testing.T) {
	console, err := conpty.Start(conpty.Spec{Args: []string{os.Args[0], "echo-child"}, Cols: 80, Rows: 25})
	if err != nil {
		t.Fatal(err)
	}
	if err := console.Close(); err != nil {
		t.Fatal(err)
	}
	reading, answering, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reading.Close()

	go func() {
		defer answering.Close()
		serveScreen(answering, console, &sync.RWMutex{})
	}()

	if _, err := readHello(reading); err != nil {
		t.Fatalf("readHello: %v", err)
	}
	kind, payload, err := readFrame(reading)
	if err != nil || kind != frameScreen {
		t.Fatalf("readFrame = %q, %q, %v; want a screen frame", kind, payload, err)
	}
	var answer screen
	if err := json.Unmarshal(payload, &answer); err != nil || answer.Error != "the terminal has ended" || answer.Rows != nil {
		t.Errorf("the answer = %+v, %v; want only that the terminal has ended", answer, err)
	}
}

// A screen read that cannot attach to the terminal's console, here because
// the terminal's program left it, is an error naming the terminal, never an
// empty screen.
func TestAScreenReadThatCannotAttachIsAnError(t *testing.T) {
	_, record := launch(t)
	v := connect(t, record)
	v.waitFor(t, "ready")
	left := filepath.Join(t.TempDir(), "left")
	typeLine(t, v, "free "+left)
	waitForFile(t, left)

	rows, err := ReadScreen(record)

	if err == nil || rows != nil || !strings.Contains(err.Error(), "terminal g1") || !strings.Contains(err.Error(), "attach to the console") {
		t.Fatalf("ReadScreen = %q, %v; want an error naming terminal g1 and the attach that failed", rows, err)
	}
}

// A screen read of a terminal whose host does not answer is an error naming
// the terminal.
func TestAScreenReadOfAHostThatDoesNotAnswerIsAnError(t *testing.T) {
	record := Record{ID: "g9", Pipe: fmt.Sprintf(`\\.\pipe\cfo-host-screen-test-%d`, time.Now().UnixNano()), Token: "token", Version: Version, HostPID: os.Getpid()}

	rows, err := ReadScreen(record)

	if err == nil || rows != nil || !strings.Contains(err.Error(), "terminal g9") {
		t.Fatalf("ReadScreen = %q, %v; want an error naming terminal g9", rows, err)
	}
}
