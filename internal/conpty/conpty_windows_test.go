package conpty

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// childMode makes the test binary the process inside the console: "echo"
// answers typed lines, "sleep" just waits to be ended.
const childMode = "CONPTY_TEST_CHILD"

func TestMain(m *testing.M) {
	switch os.Getenv(childMode) {
	case "echo":
		echoChild()
	case "sleep":
		time.Sleep(time.Minute)
	default:
		os.Exit(m.Run())
	}
}

// echoChild answers one typed line at a time: its console size, an
// environment value, its directory, a grandchild it starts, an exit code, or
// the line itself.
func echoChild() {
	fmt.Println("ready")
	lines := bufio.NewScanner(os.Stdin)
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		switch {
		case line == "size":
			var info windows.ConsoleScreenBufferInfo
			if err := windows.GetConsoleScreenBufferInfo(windows.Handle(os.Stdout.Fd()), &info); err != nil {
				fmt.Println("size error", err)
				continue
			}
			fmt.Printf("size %dx%d\n", info.Window.Right-info.Window.Left+1, info.Window.Bottom-info.Window.Top+1)
		case strings.HasPrefix(line, "env "):
			fmt.Println("env", os.Getenv(strings.TrimPrefix(line, "env ")))
		case line == "cwd":
			dir, _ := os.Getwd()
			fmt.Println("cwd", dir)
		case line == "spawn":
			grandchild := exec.Command(os.Args[0])
			grandchild.Env = append(os.Environ(), childMode+"=sleep")
			if err := grandchild.Start(); err != nil {
				fmt.Println("spawn error", err)
				continue
			}
			fmt.Printf("grandchild %d\n", grandchild.Process.Pid)
		case strings.HasPrefix(line, "exit "):
			code, _ := strconv.Atoi(strings.TrimPrefix(line, "exit "))
			os.Exit(code)
		default:
			fmt.Println("got", line)
		}
	}
}

// screen collects everything a console writes until it ends.
type screen struct {
	mu    sync.Mutex
	text  bytes.Buffer
	ended chan struct{}
}

func watch(console *Console) *screen {
	s := &screen{ended: make(chan struct{})}
	go func() {
		defer close(s.ended)
		buffer := make([]byte, 4096)
		for {
			n, err := console.Read(buffer)
			s.mu.Lock()
			s.text.Write(buffer[:n])
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return s
}

// waitFor returns the first match of pattern in the screen, failing the test
// if none appears within ten seconds.
func (s *screen) waitFor(t *testing.T, pattern string) []string {
	t.Helper()
	expression := regexp.MustCompile(pattern)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		s.mu.Lock()
		match := expression.FindStringSubmatch(s.text.String())
		s.mu.Unlock()
		if match != nil {
			return match
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t.Fatalf("no %q on the screen within 10s:\n%q", pattern, s.text.String())
	return nil
}

func startChild(t *testing.T, spec Spec) (*Console, *screen) {
	t.Helper()
	if spec.Args == nil {
		spec.Args = []string{os.Args[0]}
	}
	if spec.Env == nil {
		spec.Env = append(os.Environ(), childMode+"=echo")
	}
	console, err := Start(spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	s := watch(console)
	t.Cleanup(func() {
		_ = console.Close()
		<-s.ended
	})
	s.waitFor(t, "ready")
	return console, s
}

func typeLine(t *testing.T, console *Console, line string) {
	t.Helper()
	if _, err := console.Write([]byte(line + "\r")); err != nil {
		t.Fatalf("Write %q: %v", line, err)
	}
}

// The process reads what is typed into the terminal and its output comes
// back out.
func TestConsoleCarriesInputInAndOutputOut(t *testing.T) {
	console, s := startChild(t, Spec{Cols: 80, Rows: 25})

	typeLine(t, console, "hello goblin")

	s.waitFor(t, "got hello goblin")
}

// A resize reaches the process as its terminal's new size.
func TestConsoleResizeReachesTheProcess(t *testing.T) {
	console, s := startChild(t, Spec{Cols: 80, Rows: 25})
	typeLine(t, console, "size")
	s.waitFor(t, `size 80x25`)

	if err := console.Resize(100, 30); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	typeLine(t, console, "size")

	s.waitFor(t, `size 100x30`)
}

// The process starts in the directory and with the environment it was given.
func TestConsoleStartsTheProcessWhereItWasTold(t *testing.T) {
	dir := t.TempDir()
	console, s := startChild(t, Spec{Cols: 80, Rows: 25, Dir: dir, Env: append(os.Environ(), childMode+"=echo", "CONPTY_TEST_VALUE=fleet")})

	typeLine(t, console, "env CONPTY_TEST_VALUE")
	typeLine(t, console, "cwd")

	s.waitFor(t, "env fleet")
	s.waitFor(t, regexp.QuoteMeta("cwd "+dir))
}

// The exit code is kept, and the output ends once the process has.
func TestConsoleReportsTheExitCodeAndEndsItsOutput(t *testing.T) {
	console, s := startChild(t, Spec{Cols: 80, Rows: 25})

	typeLine(t, console, "exit 7")

	select {
	case <-console.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the process did not exit")
	}
	if code := console.ExitCode(); code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
	select {
	case <-s.ended:
	case <-time.After(10 * time.Second):
		t.Fatal("the output did not end after the process exited")
	}
}

// Close ends the process and everything it started, so no goblin's children
// outlive its terminal.
func TestCloseEndsTheWholeProcessTree(t *testing.T) {
	console, s := startChild(t, Spec{Cols: 80, Rows: 25})
	typeLine(t, console, "spawn")
	grandchild, err := strconv.Atoi(s.waitFor(t, `grandchild (\d+)`)[1])
	if err != nil {
		t.Fatal(err)
	}

	if err := console.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for _, pid := range []int{console.pid, grandchild} {
		if !exited(pid) {
			t.Errorf("pid %d is still running after Close", pid)
		}
	}
}

// exited reports whether pid has ended within ten seconds. A pid that can no
// longer be opened has ended.
func exited(pid int) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return true
	}
	defer windows.CloseHandle(handle)
	event, _ := windows.WaitForSingleObject(handle, 10000)
	return event == windows.WAIT_OBJECT_0
}

func TestStartRefusesWhatItCannotRun(t *testing.T) {
	for name, c := range map[string]struct {
		spec Spec
		want string
	}{
		"no command":                     {Spec{Cols: 80, Rows: 25}, "a command is required"},
		"a size too small":               {Spec{Args: []string{os.Args[0]}, Cols: 1, Rows: 25}, "outside 2 to 1000 cells"},
		"a size too large":               {Spec{Args: []string{os.Args[0]}, Cols: 80, Rows: 1001}, "outside 2 to 1000 cells"},
		"an environment entry with no =": {Spec{Args: []string{os.Args[0]}, Cols: 80, Rows: 25, Env: []string{"FLEET"}}, "is not KEY=value"},
	} {
		console, err := Start(c.spec)
		if err == nil {
			_ = console.Close()
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Start error = %v, want %q", name, err, c.want)
		}
	}
}
