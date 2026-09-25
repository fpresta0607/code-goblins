package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/conpty"
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
// "size", and the line itself otherwise.
func attachTestProgram() {
	fmt.Println("ready")
	lines := bufio.NewScanner(os.Stdin)
	for lines.Scan() {
		if lines.Text() != "size" {
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
		resolveHome: func() (home.Home, error) { return home.Home{Root: filepath.Dir(stateDir), State: stateDir}, nil },
		nativeCFO:   supervisor.NativeCFO,
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

// cfo attach shows a native terminal in its own console: keys typed there
// reach the terminal, the terminal's output comes back, the terminal follows
// the console's size, and Ctrl-] leaves the terminal running.
func TestAttachShowsANativeTerminalInThisConsole(t *testing.T) {
	stateDir := t.TempDir()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		_ = host.Run(stateDir, host.Spec{ID: "t1", Args: []string{program, attachTestTerminal}, Cols: 80, Rows: 24})
	}()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := host.ReadRecord(stateDir, "t1"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the host never recorded itself")
		}
	}
	t.Cleanup(func() {
		if record, err := host.ReadRecord(stateDir, "t1"); err == nil {
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
	viewer, err := conpty.Start(conpty.Spec{Args: []string{program, attachTestViewer, stateDir, "t1"}, Cols: 100, Rows: 30})
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

// cfo attach with no terminal named shows the registered CFO's terminal, so
// with no CFO in a native terminal it says so.
func TestAttachWithNoCFOInANativeTerminalSaysSo(t *testing.T) {
	stateDir := t.TempDir()
	var stdout, stderr strings.Builder
	runtime := commandRuntime{
		resolveHome: func() (home.Home, error) { return home.Home{Root: filepath.Dir(stateDir), State: stateDir}, nil },
		nativeCFO:   supervisor.NativeCFO,
	}

	exit := runAttach(nil, &stdout, &stderr, runtime)

	if exit != 1 || !strings.Contains(stderr.String(), "no CFO runs in a native terminal") {
		t.Errorf("exit=%d stderr=%q, want the missing native CFO named", exit, stderr.String())
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
