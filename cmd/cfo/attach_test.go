package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/conpty"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// The attach test runs this binary as the program in a native terminal, and
// as cfo attach in a console of its own; TestMain sends each to its part.
const (
	attachTestTerminal = "attach-test-terminal"
	attachTestViewer   = "attach-test-viewer"
)

// attachTestProgram answers one typed line at a time: its terminal's size for
// "size", more output than a host keeps for "spill", and the line itself
// otherwise. It ends at the end of its input.
func attachTestProgram() {
	fmt.Println("ready")
	lines := bufio.NewScanner(os.Stdin)
	for lines.Scan() {
		switch lines.Text() {
		case "size":
		case "spill":
			line := strings.Repeat("s", 100)
			for range 90000 {
				fmt.Println(line)
			}
			fmt.Println("spilled")
			continue
		default:
			fmt.Println("got", lines.Text())
			continue
		}
		var info windows.ConsoleScreenBufferInfo
		if err := windows.GetConsoleScreenBufferInfo(windows.Handle(os.Stdout.Fd()), &info); err != nil {
			fmt.Println("size error", err)
			continue
		}
		fmt.Printf("size %dx%d\n", info.Window.Right-info.Window.Left+1, info.Window.Bottom-info.Window.Top+1)
	}
}

// attachTestView is cfo attach for the state directory and arguments it is
// given, with the test's home in place of the fleet's.
func attachTestView(stateDir string, args []string) int {
	runtime := commandRuntime{
		resolveHome:        func() (home.Home, error) { return home.Home{Root: filepath.Dir(stateDir), State: stateDir}, nil },
		nativeCFO:          supervisor.NativeCFO,
		liveCFO:            supervisor.LiveCFO,
		nativeTerminalRuns: nativeTerminalRuns,
		attachNative:       attachNative,
	}
	return runAttach(args, os.Stdout, os.Stderr, runtime)
}

// console is a pseudo console the test drives, with everything it showed.
type console struct {
	*conpty.Console
	mu     sync.Mutex
	screen strings.Builder
}

func (c *console) shown() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.screen.String()
}

func (c *console) waitFor(t *testing.T, text string) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); !strings.Contains(c.shown(), text); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			shown := c.shown()
			t.Fatalf("the console never showed %q; it shows %q", text, shown[max(0, len(shown)-400):])
		}
	}
}

// hostAttachTestTerminal hosts native terminal id in this test process,
// running attachTestProgram, and closes it when the test ends.
func hostAttachTestTerminal(t *testing.T, stateDir, id string) {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		_ = host.Run(stateDir, host.Spec{ID: id, Args: []string{program, attachTestTerminal}, Cols: 80, Rows: 24})
	}()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := host.ReadRecord(stateDir, id); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the host never recorded itself")
		}
	}
	t.Cleanup(func() {
		if record, err := host.ReadRecord(stateDir, id); err == nil {
			if client, err := host.Dial(record); err == nil {
				_ = client.CloseTerminal()
				_ = client.Close()
			}
		}
		select {
		case <-ended:
		case <-time.After(15 * time.Second):
			t.Error("the terminal's host did not end")
		}
	})
}

// attachInConsole runs cfo attach id in a 100x30 pseudo console of its own.
func attachInConsole(t *testing.T, stateDir, id string) *console {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := conpty.Start(conpty.Spec{Args: []string{program, attachTestViewer, stateDir, id}, Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	c := &console{Console: viewer}
	t.Cleanup(func() { _ = viewer.Close() })
	go func() {
		chunk := make([]byte, 32<<10)
		for {
			n, err := viewer.Read(chunk)
			c.mu.Lock()
			c.screen.Write(chunk[:n])
			c.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return c
}

// cfo attach shows a native terminal in its own console: keys typed there
// reach the terminal, the terminal's output comes back, the terminal follows
// the console's size, and Ctrl-] leaves the terminal running.
func TestAttachShowsANativeTerminalInThisConsole(t *testing.T) {
	stateDir := t.TempDir()
	hostAttachTestTerminal(t, stateDir, "t1")
	c := attachInConsole(t, stateDir, "t1")
	viewer := c.Console

	c.waitFor(t, "ready")
	if _, err := viewer.Write([]byte("hello\r")); err != nil {
		t.Fatal(err)
	}
	c.waitFor(t, "got hello")
	if _, err := viewer.Write([]byte("size\r")); err != nil {
		t.Fatal(err)
	}
	c.waitFor(t, "size 100x30")
	if err := viewer.Resize(90, 20); err != nil {
		t.Fatal(err)
	}
	// cfo attach notices a new console size on its next look, a quarter
	// second at most.
	for resized := false; !resized; {
		if _, err := viewer.Write([]byte("size\r")); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(time.Second); time.Now().Before(deadline) && !resized; time.Sleep(20 * time.Millisecond) {
			resized = strings.Contains(c.shown(), "size 90x20")
		}
		if !resized && strings.Count(c.shown(), "size 100x30") > 8 {
			t.Fatalf("the terminal never took the console's new size; the console shows %q", c.shown()[max(0, len(c.shown())-400):])
		}
	}
	if _, err := viewer.Write([]byte{detachKey}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-viewer.Done():
	case <-time.After(15 * time.Second):
		t.Fatalf("cfo attach did not leave on Ctrl-]; the console shows %q", c.shown()[max(0, len(c.shown())-600):])
	}
	if code := viewer.ExitCode(); code != 0 {
		t.Errorf("cfo attach exit code = %d, want 0 for leaving the terminal running", code)
	}
	record, err := host.ReadRecord(stateDir, "t1")
	if err != nil {
		t.Fatalf("the terminal's record after leaving it: %v", err)
	}
	client, err := host.Dial(record)
	if err != nil {
		t.Fatalf("the terminal does not answer after leaving it: %v", err)
	}
	_ = client.Close()
}

// cfo attach asks its console for Windows key events itself, so keys reach
// the terminal exactly even once the host's history no longer holds the
// pseudo console's own request for them. Ctrl-Z is one such key: read as a
// plain byte, it would end cfo attach's own input rather than the terminal's.
func TestAttachTakesWindowsKeyEventsAfterTheHistoryIsTrimmed(t *testing.T) {
	stateDir := t.TempDir()
	hostAttachTestTerminal(t, stateDir, "t1")
	record, err := host.ReadRecord(stateDir, "t1")
	if err != nil {
		t.Fatal(err)
	}
	client, err := host.Dial(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Input([]byte("spill\r")); err != nil {
		t.Fatal(err)
	}
	var tail []byte
	for !strings.Contains(string(tail), "spilled") {
		event, err := client.Next()
		if err != nil || event.Exited {
			t.Fatalf("the terminal ended before it spilled: %+v, %v", event, err)
		}
		tail = append(tail[max(0, len(tail)-16):], event.Output...)
	}
	_ = client.Close()
	c := attachInConsole(t, stateDir, "t1")
	c.waitFor(t, "spilled")

	if _, err := c.Write([]byte{0x1a, '\r'}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-c.Done():
	case <-time.After(15 * time.Second):
		t.Fatalf("cfo attach did not end with the terminal; the console shows %q", c.shown()[max(0, len(c.shown())-600):])
	}
	if code := c.ExitCode(); code != 0 {
		t.Errorf("cfo attach exit code = %d, want 0 for the terminal's end at Ctrl-Z", code)
	}
}

// A native terminal whose host answers runs, whether or not a CFO in it has
// registered.
func TestANativeTerminalWhoseHostAnswersRuns(t *testing.T) {
	stateDir := t.TempDir()
	hostAttachTestTerminal(t, stateDir, nativeCFOTerminal)

	runs := nativeTerminalRuns(stateDir, nativeCFOTerminal)

	if !runs {
		t.Errorf("nativeTerminalRuns = false, want true for a host that answers")
	}
}

// A record of a native terminal whose host does not answer names nothing
// running.
func TestANativeTerminalWhoseHostDoesNotAnswerDoesNotRun(t *testing.T) {
	stateDir := t.TempDir()
	record := host.Record{ID: nativeCFOTerminal, Pipe: fmt.Sprintf(`\\.\pipe\cfo-attach-test-%d`, time.Now().UnixNano()), Token: "token", Version: host.Version, HostPID: os.Getpid()}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "hosts", nativeCFOTerminal+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	runs := nativeTerminalRuns(stateDir, nativeCFOTerminal)

	if runs {
		t.Errorf("nativeTerminalRuns = true, want false for a host that does not answer")
	}
}

// cfo attach with no terminal named shows the registered CFO's native
// terminal. With no CFO registered it shows terminal cfo while its host
// answers, and a CFO registered in Herdr is never passed over for it.
func TestAttachWithNoTerminalNamedShowsTheCFOsNativeTerminal(t *testing.T) {
	for name, test := range map[string]struct {
		registeredNative string
		inHerdr          bool
		cfoRuns          bool
		shown            string
		refusal          string
	}{
		"a CFO registered in a native terminal":        {registeredNative: "n1", cfoRuns: true, shown: "n1"},
		"a CFO registered in Herdr, terminal cfo runs": {inHerdr: true, cfoRuns: true, refusal: "the CFO runs in Herdr, not in a native terminal"},
		"nothing registered, terminal cfo runs":        {cfoRuns: true, shown: nativeCFOTerminal},
		"nothing registered, nothing runs":             {refusal: "no CFO runs in a native terminal"},
	} {
		t.Run(name, func(t *testing.T) {
			stateDir := t.TempDir()
			var shown []string
			var stdout, stderr strings.Builder
			runtime := commandRuntime{
				resolveHome: func() (home.Home, error) { return home.Home{Root: filepath.Dir(stateDir), State: stateDir}, nil },
				nativeCFO: func(string) (string, bool) {
					return test.registeredNative, test.registeredNative != ""
				},
				liveCFO: func(string) (herdr.Endpoint, bool) { return herdr.Endpoint{}, test.inHerdr },
				nativeTerminalRuns: func(_, id string) bool {
					return test.cfoRuns && id == nativeCFOTerminal
				},
				attachNative: func(_, id string, _, _ io.Writer) int {
					shown = append(shown, id)
					return 0
				},
			}

			exit := runAttach(nil, &stdout, &stderr, runtime)

			if test.refusal != "" {
				if exit != 1 || !strings.Contains(stderr.String(), test.refusal) || len(shown) != 0 {
					t.Errorf("exit=%d stderr=%q shown=%q, want %q and nothing shown", exit, stderr.String(), shown, test.refusal)
				}
				return
			}
			if exit != 0 || !slices.Equal(shown, []string{test.shown}) {
				t.Errorf("exit=%d stderr=%q shown=%q, want %s shown", exit, stderr.String(), shown, test.shown)
			}
		})
	}
}

// Ctrl-] leaves the terminal whichever way the console sends it: as the byte
// itself, or as a Windows key event once the terminal asked for Windows input
// mode. Its release, and other keys that look alike, stay with the terminal.
func TestCtrlCloseBracketLeavesTheTerminalInEitherEncoding(t *testing.T) {
	for name, keys := range map[string]struct {
		keys string
		at   int
	}{
		"the byte":                         {"ab\x1dcd", 2},
		"a Windows key event, pressed":     {"a\x1b[221;27;29;1;8;1_", 1},
		"a Windows key event, released":    {"\x1b[221;27;29;0;8;1_", -1},
		"another key's Windows key event":  {"\x1b[65;30;97;1;0;1_", -1},
		"an arrow key, then an underscore": {"\x1b[A_", -1},
		"typing":                           {"hello\r", -1},
	} {
		t.Run(name, func(t *testing.T) {
			if at := detachAt([]byte(keys.keys)); at != keys.at {
				t.Errorf("detachAt(%q) = %d, want %d", keys.keys, at, keys.at)
			}
		})
	}
}

// A CFO started from inside Herdr registers its native terminal, not the
// pane goblins ran in: the pane's id is left out of its environment.
func TestANativeCFOIsStartedWithoutTheLaunchersHerdrPane(t *testing.T) {
	env := []string{"PATH=C:\\bin", "HERDR_PANE_ID=w1:p1", "herdr_pane_id=w1:p2", "HERDR_SESSION=fleet", "CFO_HOME=C:\\home"}

	got := nativeCFOEnvironment(env)

	want := []string{"PATH=C:\\bin", "HERDR_SESSION=fleet", "CFO_HOME=C:\\home"}
	if !slices.Equal(got, want) {
		t.Errorf("nativeCFOEnvironment = %q, want %q", got, want)
	}
}

// A native terminal starts its program itself, with no shell to run a script
// shim, so a claude found only as a script is refused before any host starts.
func TestANativeCFOIsNotStartedFromAScriptShim(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude.cmd"), []byte("@echo off\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	stateDir := t.TempDir()

	err := startNativeCFO(stateDir, t.TempDir())

	if err == nil || !strings.Contains(err.Error(), "not a program a native terminal can start") {
		t.Fatalf("startNativeCFO error = %v, want the script shim refused", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "hosts")); !os.IsNotExist(err) {
		t.Errorf("hosts directory stat = %v, want no host started", err)
	}
}
