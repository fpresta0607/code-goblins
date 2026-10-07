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
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/conpty"
)

var (
	kernel32              = windows.NewLazySystemDLL("kernel32.dll")
	setConsoleCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")
	readConsoleOutput     = kernel32.NewProc("ReadConsoleOutputW")
)

// consoleProcessIDs are the processes attached to this process's console.
func consoleProcessIDs() []uint32 {
	pids := make([]uint32, 16)
	count, _, _ := kernel32.NewProc("GetConsoleProcessList").Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return pids[:min(int(count), len(pids))]
}

// ownScreen reads the rows of the window of this process's console the way
// the program in a terminal sees them, through a console call of its own, so
// a host's read is compared with the screen rather than with itself.
func ownScreen() ([]string, error) {
	out := windows.Handle(os.Stdout.Fd())
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(out, &info); err != nil {
		return nil, err
	}
	width, height := int(info.Window.Right-info.Window.Left)+1, int(info.Window.Bottom-info.Window.Top)+1
	// CHAR_INFO: a cell's character, then its attributes.
	cells := make([][2]uint16, width*height)
	region := info.Window
	// The buffer's size and the cell to start at, each a COORD taken by value.
	size := uint32(uint16(width)) | uint32(uint16(height))<<16
	if ok, _, err := readConsoleOutput.Call(uintptr(out), uintptr(unsafe.Pointer(&cells[0])), uintptr(size), 0, uintptr(unsafe.Pointer(&region))); ok == 0 {
		return nil, err
	}
	rows := make([]string, height)
	for y := range rows {
		characters := make([]uint16, width)
		for x := range characters {
			characters[x] = cells[y*width+x][0]
		}
		rows[y] = string(utf16.Decode(characters))
	}
	return rows, nil
}

// obeyCtrlC has this process, and every process it starts from now on, end at
// a Ctrl-C as processes do by default, whatever started the test: a process in
// a new process group ignores Ctrl-C and passes that on to what it starts.
func obeyCtrlC(t *testing.T) {
	if ok, _, err := setConsoleCtrlHandler.Call(0, 0); ok == 0 {
		t.Fatalf("SetConsoleCtrlHandler: %v", err)
	}
}

// holdScreenReads holds every screen read a host in this process serves for
// hold, with the terminal's screens held, before the read asks for the
// screen, until the test ends; the channel it returns says when a hold
// begins. Call it before hosting a terminal in this process, so the host sees
// the hold from its start and has ended before it is lifted.
func holdScreenReads(t *testing.T, hold time.Duration) <-chan struct{} {
	held := make(chan struct{}, 1)
	holdScreenRead = func() {
		select {
		case held <- struct{}{}:
		default:
		}
		time.Sleep(hold)
	}
	t.Cleanup(func() { holdScreenRead = func() {} })
	return held
}

// waitForHold waits for a screen read to be held.
func waitForHold(t *testing.T, held <-chan struct{}) {
	t.Helper()
	select {
	case <-held:
	case <-time.After(15 * time.Second):
		t.Fatal("no screen read was held")
	}
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

// jobAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION.
type jobAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}

// processesStarted is how many processes job has held, ended ones included.
func processesStarted(t *testing.T, job windows.Handle) uint32 {
	t.Helper()
	var accounting jobAccounting
	if err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil); err != nil {
		t.Fatalf("QueryInformationJobObject: %v", err)
	}
	return accounting.TotalProcesses
}

// A screen read is a message to a process already attached to the terminal's
// console, never a process of its own: steer delivery reads a goblin's screen
// every 250 ms, and one read that started a process took 8.6 s on a machine
// slow to start them. The host runs here in a job the test made, which counts
// every process the host starts from then on.
func TestAScreenReadStartsNoProcess(t *testing.T) {
	_, record := launch(t)
	v := connect(t, record)
	v.waitFor(t, "ready")
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(job) })
	hostProcess, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(record.HostPID))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(hostProcess)
	if err := windows.AssignProcessToJobObject(job, hostProcess); err != nil {
		t.Fatalf("AssignProcessToJobObject: %v", err)
	}
	before := processesStarted(t, job)

	for range 20 {
		if _, err := ReadScreen(record); err != nil {
			t.Fatalf("ReadScreen: %v", err)
		}
	}

	if started := processesStarted(t, job) - before; started != 0 {
		t.Errorf("20 screen reads started %d processes, want none", started)
	}
}

// A Ctrl-C typed to the terminal reaches the terminal's program and the
// console's input waker, which reads the screen, never the host: the host
// here is this test process, which a Ctrl-C would end. The waker outlives
// it, so the screen still reads.
func TestACtrlCNeverReachesTheHostAndTheScreenStillReads(t *testing.T) {
	obeyCtrlC(t)
	_, record, _ := hostHere(t)
	v := connect(t, record)
	v.waitFor(t, "ready")
	typeLine(t, v, "wait-ctrl-c")
	v.waitFor(t, "waiting for a ctrl-c")

	if err := v.Input([]byte{0x03}); err != nil {
		t.Fatalf("Input: %v", err)
	}

	v.waitFor(t, "interrupted")
	rows, err := ReadScreen(record)
	if err != nil || !strings.Contains(ScreenTail(rows, 0), "interrupted") {
		t.Errorf("ReadScreen after the Ctrl-C = %q, %v; want the screen, showing the interrupt", ScreenTail(rows, 0), err)
	}
	typeLine(t, v, "still here")
	v.waitFor(t, "got still here")
}

// The terminal's console closing while a screen read is in flight ends only
// the read. The host, this test process, reports the terminal's end and
// removes its record, and the read is an error naming the terminal.
func TestTheConsoleClosingDuringAScreenReadNeverReachesTheHost(t *testing.T) {
	held := holdScreenReads(t, 2*time.Second)
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
	waitForHold(t, held)

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

// A close requested while a screen read is in flight waits for the read, so
// the read is answered before the terminal's console closes.
func TestAScreenReadFinishesBeforeTheTerminalCloses(t *testing.T) {
	held := holdScreenReads(t, 2*time.Second)
	_, record, ended := hostHere(t)
	v := connect(t, record)
	v.waitFor(t, "ready")
	read := make(chan error, 1)
	go func() {
		_, err := ReadScreen(record)
		read <- err
	}()
	waitForHold(t, held)

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

// A terminal whose program has ended is never read, since its console closed
// with it: the answer says the terminal has ended.
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

// A screen read the console's input waker cannot answer, here because it
// was ended, is an error naming the terminal, never an empty screen, and
// comes at once rather than when the read times out.
func TestAScreenReadNothingAnswersIsAnErrorAtOnce(t *testing.T) {
	_, record := launch(t)
	v := connect(t, record)
	v.waitFor(t, "ready")
	typeLine(t, v, "end-waker")
	v.waitFor(t, "ended 1 other process")
	started := time.Now()

	rows, err := ReadScreen(record)

	if err == nil || rows != nil || !strings.Contains(err.Error(), "terminal g1") || !strings.Contains(err.Error(), "input waker") {
		t.Fatalf("ReadScreen = %q, %v; want an error naming terminal g1 and its input waker", rows, err)
	}
	if took := time.Since(started); took >= screenTimeout {
		t.Errorf("the read failed after %s, want before its %s timeout", took, screenTimeout)
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
