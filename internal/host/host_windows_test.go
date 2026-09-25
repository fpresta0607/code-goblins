package host

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The test binary plays every part. As the command in a terminal it answers
// typed lines ("echo-child") or just waits ("sleep-child"); with hostRole set
// it is the host itself, or a launcher that starts a host and exits.
const hostRole = "HOST_TEST_ROLE"

func TestMain(m *testing.M) {
	switch {
	case len(os.Args) > 1 && os.Args[1] == "echo-child":
		echoChild()
	case len(os.Args) > 1 && os.Args[1] == "sleep-child":
		time.Sleep(time.Minute)
	case os.Getenv(hostRole) == "host":
		if err := RunArgs(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case os.Getenv(hostRole) == "launch":
		launcher()
	default:
		os.Exit(m.Run())
	}
}

// echoChild answers one typed line at a time: the terminal its host says it
// runs in, its terminal's size, a grandchild it starts, an exit code, a flood
// of output before an exit code, a spill of output it keeps running after, its
// screen as it reads it itself, a screen read attaching to its console, a
// Ctrl-C, leaving its console, or the line itself.
func echoChild() {
	// A Ctrl-C typed to the terminal is reported, not obeyed.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	fmt.Println("ready")
	lines := bufio.NewScanner(os.Stdin)
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		switch {
		case strings.HasPrefix(line, "screen "):
			// Written to a file, so the screen stays as it was read.
			rows, err := consoleScreen()
			if err != nil {
				fmt.Println("screen error", err)
				continue
			}
			data, _ := json.Marshal(rows)
			path := strings.TrimPrefix(line, "screen ")
			if os.WriteFile(path+".part", data, 0o600) == nil {
				_ = os.Rename(path+".part", path)
			}
		case line == "wait-attach" || line == "hold-ctrl-c":
			// A second process on this console is a screen read attached.
			for deadline := time.Now().Add(15 * time.Second); consoleProcesses() < 2 && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			}
			fmt.Println("attached", consoleProcesses())
			if line == "wait-attach" {
				continue
			}
			// Waiting here rather than on input, which a Ctrl-C would end.
			select {
			case <-interrupts:
				fmt.Println("interrupted")
			case <-time.After(15 * time.Second):
				fmt.Println("no interrupt")
			}
		case strings.HasPrefix(line, "free "):
			// Nothing reaches this console through this program any more.
			_, _, _ = kernel32.NewProc("FreeConsole").Call()
			_ = os.WriteFile(strings.TrimPrefix(line, "free "), nil, 0o600)
			time.Sleep(time.Minute)
		case line == "host-id":
			fmt.Println("host-id", os.Getenv(IDVariable))
		case line == "size":
			var info windows.ConsoleScreenBufferInfo
			if err := windows.GetConsoleScreenBufferInfo(windows.Handle(os.Stdout.Fd()), &info); err != nil {
				fmt.Println("size error", err)
				continue
			}
			fmt.Printf("size %dx%d\n", info.Window.Right-info.Window.Left+1, info.Window.Bottom-info.Window.Top+1)
		case line == "spawn":
			grandchild := exec.Command(os.Args[0], "sleep-child")
			// Detached from the console, like a dev server a harness leaves
			// running: closing the console does not end it, only the job does.
			grandchild.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
			if err := grandchild.Start(); err != nil {
				fmt.Println("spawn error", err)
				continue
			}
			fmt.Printf("grandchild %d\n", grandchild.Process.Pid)
		case strings.HasPrefix(line, "flood "):
			code, _ := strconv.Atoi(strings.TrimPrefix(line, "flood "))
			for i := 0; i < 2000; i++ {
				fmt.Println(strings.Repeat("f", 100))
			}
			fmt.Println("last words")
			os.Exit(code)
		case line == "spill":
			for i := 0; i < 2000; i++ {
				fmt.Println(strings.Repeat("s", 100))
			}
			fmt.Println("spilled")
		case strings.HasPrefix(line, "exit "):
			code, _ := strconv.Atoi(strings.TrimPrefix(line, "exit "))
			os.Exit(code)
		default:
			fmt.Println("got", line)
		}
	}
}

// launcher starts a host for the state directory and terminal id it is given,
// prints the host's pid, and exits.
func launcher() {
	record, err := Launch(os.Args[1], []string{os.Args[0]}, hostEnvironment(), Spec{ID: os.Args[2], Args: []string{os.Args[0], "echo-child"}, Cols: 80, Rows: 25})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(record.HostPID)
}

func hostEnvironment() []string {
	env := []string{hostRole + "=host"}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, hostRole+"=") {
			env = append(env, entry)
		}
	}
	return env
}

// launch starts a host running echo-child for the test and ends it, by pid,
// when the test does.
func launch(t *testing.T) (string, Record) {
	t.Helper()
	stateDir := t.TempDir()
	record, err := Launch(stateDir, []string{os.Args[0]}, hostEnvironment(), Spec{ID: "g1", Args: []string{os.Args[0], "echo-child"}, Cols: 80, Rows: 25})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	t.Cleanup(func() { end(record.HostPID) })
	// Every check of the terminal's process leans on this pid, and a pid
	// that names nothing reads as ended.
	if record.ChildPID == record.HostPID || !running(record.ChildPID) {
		t.Fatalf("record %+v does not name a running terminal process", record)
	}
	return stateDir, record
}

// end stops a process this test started, by its pid.
func end(pid int) {
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(handle)
	_ = windows.TerminateProcess(handle, 1)
	_, _ = windows.WaitForSingleObject(handle, 10000)
}

// running reports whether pid is a process that has not ended.
func running(pid int) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	event, _ := windows.WaitForSingleObject(handle, 0)
	return event == uint32(windows.WAIT_TIMEOUT)
}

// exited reports whether pid has ended within ten seconds.
func exited(pid int) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return true
	}
	defer windows.CloseHandle(handle)
	event, _ := windows.WaitForSingleObject(handle, 10000)
	return event == windows.WAIT_OBJECT_0
}

// viewer is a client and everything its terminal has shown it.
type viewer struct {
	*Client
	screen bytes.Buffer
	exit   *Event
}

func connect(t *testing.T, record Record) *viewer {
	t.Helper()
	client, err := Dial(record)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return &viewer{Client: client}
}

// waitFor reads events until the screen matches pattern, and returns the
// match.
func (v *viewer) waitFor(t *testing.T, pattern string) []string {
	t.Helper()
	expression := regexp.MustCompile(pattern)
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		if match := expression.FindStringSubmatch(v.screen.String()); match != nil {
			return match
		}
		if v.exit != nil {
			break
		}
		event, err := v.Next()
		if err != nil {
			t.Fatalf("Next: %v, while waiting for %q on:\n%q", err, pattern, v.screen.String())
		}
		v.screen.Write(event.Output)
		if event.Exited {
			v.exit = &event
		}
	}
	t.Fatalf("no %q on the screen:\n%q", pattern, v.screen.String())
	return nil
}

// waitForExit reads events until the terminal ends and returns its code.
func (v *viewer) waitForExit(t *testing.T) uint32 {
	t.Helper()
	for v.exit == nil {
		event, err := v.Next()
		if err != nil {
			t.Fatalf("Next: %v, while waiting for the terminal to end", err)
		}
		v.screen.Write(event.Output)
		if event.Exited {
			v.exit = &event
		}
	}
	return v.exit.Code
}

func typeLine(t *testing.T, v *viewer, line string) {
	t.Helper()
	if err := v.Input([]byte(line + "\r")); err != nil {
		t.Fatalf("Input %q: %v", line, err)
	}
}

// The program in a terminal learns from its host which terminal it runs in,
// even when the host was launched from another host's terminal.
func TestTheTerminalKnowsWhichTerminalItIs(t *testing.T) {
	t.Setenv(IDVariable, "outer")
	_, record := launch(t)
	v := connect(t, record)
	v.waitFor(t, "ready")

	typeLine(t, v, "host-id")

	v.waitFor(t, "host-id g1")
}

// A viewer types into the terminal the host runs and sees its output.
func TestAViewerTypesIntoTheTerminalAndSeesItsOutput(t *testing.T) {
	_, record := launch(t)
	v := connect(t, record)
	v.waitFor(t, "ready")

	typeLine(t, v, "hello goblin")

	v.waitFor(t, "got hello goblin")
}

// A viewer's resize reaches the terminal.
func TestAViewerResizesTheTerminal(t *testing.T) {
	_, record := launch(t)
	v := connect(t, record)
	v.waitFor(t, "ready")

	if err := v.Resize(100, 30); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	typeLine(t, v, "size")

	v.waitFor(t, "size 100x30")
}

// A viewer that connects later first sees everything the terminal showed.
func TestALateViewerSeesTheTerminalsHistory(t *testing.T) {
	_, record := launch(t)
	first := connect(t, record)
	first.waitFor(t, "ready")
	typeLine(t, first, "before you came")
	first.waitFor(t, "got before you came")

	late := connect(t, record)
	history, err := late.Next()

	if err != nil || !strings.Contains(string(history.Output), "got before you came") {
		t.Fatalf("the late viewer's first event = %q, %v; want the history", history.Output, err)
	}
}

// A client that types and never reads reaches the terminal, even while the
// host has more history for it than the pipe holds.
func TestAClientThatNeverReadsStillTypesIntoTheTerminal(t *testing.T) {
	_, record := launch(t)
	v := connect(t, record)
	v.waitFor(t, "ready")
	typeLine(t, v, "spill")
	v.waitFor(t, "spilled")

	typist, err := Dial(record)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := typist.Input([]byte("typed without reading\r")); err != nil {
		t.Fatalf("Input: %v", err)
	}
	_ = typist.Close()

	v.waitFor(t, "got typed without reading")
}

// A host outlives the process that launched it.
func TestTheHostOutlivesItsLauncher(t *testing.T) {
	stateDir := t.TempDir()
	launcherProcess := exec.Command(os.Args[0], stateDir, "g1")
	launcherProcess.Env = append(os.Environ(), hostRole+"=launch")

	output, err := launcherProcess.Output()

	if err != nil {
		t.Fatalf("the launcher failed: %v", err)
	}
	hostPID, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		t.Fatalf("the launcher printed %q, want the host's pid", output)
	}
	t.Cleanup(func() { end(hostPID) })
	record, err := ReadRecord(stateDir, "g1")
	if err != nil || record.HostPID != hostPID {
		t.Fatalf("ReadRecord = %+v, %v; want the launched host", record, err)
	}
	v := connect(t, record)
	v.waitFor(t, "ready")
	typeLine(t, v, "still here")
	v.waitFor(t, "got still here")
}

// A host refuses a client with the wrong token or protocol version.
func TestTheHostRefusesAWrongTokenOrVersion(t *testing.T) {
	_, record := launch(t)
	for name, c := range map[string]struct {
		version int
		token   string
		want    string
	}{
		"a wrong token":   {Version, strings.Repeat("0", len(record.Token)), "token does not match"},
		"another version": {Version + 1, record.Token, "protocol"},
	} {
		client, err := dial(record, hello{Version: c.version, Token: c.token})
		if err == nil {
			_ = client.Close()
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: dial error = %v, want %q", name, err, c.want)
		}
	}
}

// Closing the terminal ends it and everything it started, tells the viewer,
// and the host leaves no process and no record behind.
func TestClosingTheTerminalLeavesNoProcess(t *testing.T) {
	stateDir, record := launch(t)
	v := connect(t, record)
	v.waitFor(t, "ready")
	typeLine(t, v, "spawn")
	grandchild, err := strconv.Atoi(v.waitFor(t, `grandchild (\d+)`)[1])
	if err != nil {
		t.Fatal(err)
	}

	if err := v.CloseTerminal(); err != nil {
		t.Fatalf("CloseTerminal: %v", err)
	}

	v.waitForExit(t)
	for _, pid := range []int{record.ChildPID, grandchild, record.HostPID} {
		if !exited(pid) {
			t.Errorf("pid %d is still running after the terminal closed", pid)
		}
	}
	if _, err := ReadRecord(stateDir, "g1"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the record is still there: %v", err)
	}
}

// A host that is killed takes its terminal and everything in it along.
func TestAKilledHostTakesItsTerminalWithIt(t *testing.T) {
	_, record := launch(t)
	v := connect(t, record)
	v.waitFor(t, "ready")
	typeLine(t, v, "spawn")
	grandchild, err := strconv.Atoi(v.waitFor(t, `grandchild (\d+)`)[1])
	if err != nil {
		t.Fatal(err)
	}

	end(record.HostPID)

	for _, pid := range []int{record.ChildPID, grandchild} {
		if !exited(pid) {
			t.Errorf("pid %d is still running after its host was killed", pid)
		}
	}
}

// When the terminal's process exits, the viewer hears its exit code and the
// host ends and removes its record.
func TestTheHostEndsWithItsTerminal(t *testing.T) {
	stateDir, record := launch(t)
	v := connect(t, record)
	v.waitFor(t, "ready")

	typeLine(t, v, "exit 3")

	if code := v.waitForExit(t); code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
	if !exited(record.HostPID) {
		t.Error("the host is still running after its terminal ended")
	}
	if _, err := ReadRecord(stateDir, "g1"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the record is still there: %v", err)
	}
}

// A viewer sees everything the terminal's process printed before it exited,
// the pseudo console's closing sequence included, then its exit code.
func TestTheViewerSeesTheTerminalsLastOutput(t *testing.T) {
	_, record := launch(t)
	v := connect(t, record)
	v.waitFor(t, "ready")

	typeLine(t, v, "flood 4")

	code := v.waitForExit(t)
	screen := v.screen.String()
	if !strings.Contains(screen, "last words") {
		t.Errorf("the screen before the exit ends %q, want the last words", screen[max(0, len(screen)-200):])
	}
	// The pseudo console turns win32 input mode off only as it closes; a
	// viewer that misses it is left with its keyboard garbled.
	if strings.Contains(screen, "\x1b[?9001h") && !strings.Contains(screen, "\x1b[?9001l") {
		t.Errorf("the screen before the exit ends %q, want the console's closing sequence", screen[max(0, len(screen)-200):])
	}
	if code != 4 {
		t.Errorf("exit code = %d, want 4", code)
	}
}

// A client refuses a pipe that is not served by the host its record names,
// so a process that took over the pipe cannot pose as the host.
func TestAClientRefusesAPipeAnotherProcessServes(t *testing.T) {
	_, record := launch(t)
	impostor := record
	impostor.HostPID = record.ChildPID

	client, err := Dial(impostor)

	if err == nil {
		_ = client.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "is not served by host pid") {
		t.Fatalf("Dial error = %v, want the pipe refused", err)
	}
}

// A second host for a terminal that already runs is refused before it starts
// anything, so the first host's record keeps finding the first host.
func TestASecondHostForARunningTerminalIsRefused(t *testing.T) {
	stateDir, record := launch(t)

	err := Run(stateDir, Spec{ID: "g1", Args: []string{os.Args[0], "echo-child"}, Cols: 80, Rows: 25})

	if err == nil || !strings.Contains(err.Error(), "already runs") {
		t.Fatalf("Run error = %v, want the running terminal named", err)
	}
	if again, err := ReadRecord(stateDir, "g1"); err != nil || again.HostPID != record.HostPID {
		t.Errorf("record after the refusal = %+v, %v; want the first host's", again, err)
	}
}

// A pipe name cannot be taken twice, so nothing can create the host's pipe
// before the host does and wait there for its viewers.
func TestAPipeNameCannotBeTakenTwice(t *testing.T) {
	name, err := pipeName()
	if err != nil {
		t.Fatal(err)
	}
	first, err := listen(name)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { windows.CloseHandle(first.waiting) })

	second, err := listen(name)

	if err == nil {
		windows.CloseHandle(second.waiting)
		t.Fatal("a second listener took a pipe name already in use")
	}
}
