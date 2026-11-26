//go:build linux || darwin

package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/pty"
)

// init runs this test binary as the program in a native terminal or as
// goblins attach in a terminal of its own, which the attach tests start it
// as, before any test or TestMain's stand-ins. Once goblins attach returns,
// it says so and reads one line from its terminal, which only a terminal
// reading lines again ends.
func init() {
	switch {
	case len(os.Args) > 1 && os.Args[1] == attachTestTerminal:
		attachTestProgram()
		os.Exit(0)
	case len(os.Args) > 2 && os.Args[1] == attachTestViewer:
		code := attachTestView(os.Args[2], os.Args[3:])
		fmt.Printf("attach returned %d\n", code)
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		fmt.Printf("read line %q, %v\n", strings.TrimSpace(line), err)
		os.Exit(code)
	}
}

// attachTestProgram answers one typed line at a time: its terminal's size for
// "size", and the line itself otherwise. It ends at the end of its input.
func attachTestProgram() {
	fmt.Println("ready")
	lines := bufio.NewScanner(os.Stdin)
	for lines.Scan() {
		if lines.Text() != "size" {
			fmt.Println("got", lines.Text())
			continue
		}
		cols, rows, err := pty.Size(int(os.Stdout.Fd()))
		if err != nil {
			fmt.Println("size error", err)
			continue
		}
		fmt.Printf("size %dx%d\n", cols, rows)
	}
}

// console is a terminal the test drives goblins attach in, with everything it
// showed.
type console struct {
	*pty.Terminal
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
	for deadline := time.Now().Add(time.Minute); !strings.Contains(c.shown(), text); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			shown := c.shown()
			t.Fatalf("the terminal never showed %q; it shows %q", text, shown[max(0, len(shown)-400):])
		}
	}
}

// typeLine types text and Enter into the console.
func (c *console) typeLine(t *testing.T, text string) {
	t.Helper()
	if _, err := c.Write([]byte(text + "\r")); err != nil {
		t.Fatal(err)
	}
}

// readsLinesAgain checks that goblins attach, once it returned, left its
// terminal reading echoed lines again. It types the line twice, since a key
// reader left waiting when the native terminal ended may take the first.
func (c *console) readsLinesAgain(t *testing.T) {
	t.Helper()
	c.waitFor(t, "attach returned")
	if _, err := c.Write([]byte("hello\rhello\r")); err != nil {
		t.Fatal(err)
	}
	c.waitFor(t, `read line "hello", <nil>`)
}

// ended waits for goblins attach to end, and returns its exit code.
func (c *console) ended(t *testing.T) uint32 {
	t.Helper()
	select {
	case <-c.Done():
		return c.ExitCode()
	case <-time.After(time.Minute):
		t.Fatalf("goblins attach did not end; the terminal shows %q", c.shown()[max(0, len(c.shown())-600):])
		return 0
	}
}

// attachInConsole runs goblins attach id in a 100x30 terminal of its own.
func attachInConsole(t *testing.T, stateDir, id string) *console {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return startConsole(t, program, attachTestViewer, stateDir, id)
}

// startConsole runs args in a 100x30 terminal of its own.
func startConsole(t *testing.T, args ...string) *console {
	t.Helper()
	viewer, err := pty.Start(pty.Spec{Args: args, Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	c := &console{Terminal: viewer}
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

// goblins attach shows a native terminal in its own terminal: keys typed there
// reach the native terminal, its output comes back, it follows the terminal's
// size as SIGWINCH reports it, and Ctrl-] leaves it running.
func TestAttachShowsANativeTerminalInThisUnixTerminal(t *testing.T) {
	stateDir := t.TempDir()
	hostAttachTestTerminal(t, stateDir, "t1")
	c := attachInConsole(t, stateDir, "t1")

	c.waitFor(t, "ready")
	c.typeLine(t, "hello")
	c.waitFor(t, "got hello")
	c.typeLine(t, "size")
	c.waitFor(t, "size 100x30")
	if err := c.Resize(90, 20); err != nil {
		t.Fatal(err)
	}
	for resized := false; !resized; {
		c.typeLine(t, "size")
		// stall-accepted: running out only types size again, and the test fails on how many old sizes the terminal answers, never on the clock.
		for deadline := time.Now().Add(time.Second); time.Now().Before(deadline) && !resized; time.Sleep(20 * time.Millisecond) {
			resized = strings.Contains(c.shown(), "size 90x20")
		}
		if !resized && strings.Count(c.shown(), "size 100x30") > 8 {
			t.Fatalf("the native terminal never took the terminal's new size; the terminal shows %q", c.shown()[max(0, len(c.shown())-400):])
		}
	}
	if _, err := c.Write([]byte{detachKey}); err != nil {
		t.Fatal(err)
	}

	c.readsLinesAgain(t)
	if code := c.ended(t); code != 0 {
		t.Errorf("goblins attach exit code = %d, want 0 for leaving the terminal running", code)
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

// goblins attach holds its terminal in raw mode, so a key its terminal would
// act on itself reaches the native terminal instead: Ctrl-C ends the native
// terminal's program rather than goblins attach, and Ctrl-D ends that
// program's input rather than goblins attach's own.
func TestAttachSendsTheKeysItsTerminalWouldActOnToTheNativeTerminal(t *testing.T) {
	for _, test := range []struct {
		name  string
		key   byte
		ended string
		code  uint32
	}{
		{"Ctrl-C", 0x03, "ended with exit code 130", 130},
		{"Ctrl-D", 0x04, "ended with exit code 0", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			hostAttachTestTerminal(t, stateDir, "t1")
			c := attachInConsole(t, stateDir, "t1")
			c.waitFor(t, "ready")

			// Act
			if _, err := c.Write([]byte{test.key}); err != nil {
				t.Fatal(err)
			}

			// Assert
			c.readsLinesAgain(t)
			if code := c.ended(t); code != test.code {
				t.Errorf("goblins attach exit code = %d, want %d, the native terminal's", code, test.code)
			}
			if !strings.Contains(c.shown(), "The native terminal "+test.ended) {
				t.Errorf("the terminal shows %q, want the native terminal %s", c.shown()[max(0, len(c.shown())-400):], test.ended)
			}
		})
	}
}

// goblins attach where its output is no terminal refuses before it changes its
// terminal's mode, so the terminal still reads echoed lines.
func TestAttachRefusesWhereItsOutputIsNoTerminal(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	hostAttachTestTerminal(t, stateDir, "t1")
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "output")

	// Act
	c := startConsole(t, "sh", "-c", `exec "$0" "$1" "$2" "$3" >"$4"`, program, attachTestViewer, stateDir, "t1", output)

	// Assert
	c.waitFor(t, "this command has none")
	if _, err := c.Write([]byte("hello\r")); err != nil {
		t.Fatal(err)
	}
	if code := c.ended(t); code != 1 {
		t.Errorf("goblins attach exit code = %d, want 1 for refusing", code)
	}
	written, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if want := `read line "hello", <nil>`; !strings.Contains(string(written), want) {
		t.Errorf("goblins attach wrote %q, want %q after it refused", written, want)
	}
}

// A board view that sizes the native terminal while goblins attach shows it
// leaves the attached terminal drawing output made for another grid; the
// next key typed in goblins attach gives it the terminal's size back before
// it arrives.
func TestAttachTakesTheTerminalBackWhenTypedIntoAfterAnotherViewerResizedItOnUnix(t *testing.T) {
	stateDir := t.TempDir()
	hostAttachTestTerminal(t, stateDir, "t1")
	c := attachInConsole(t, stateDir, "t1")
	c.waitFor(t, "ready")
	c.typeLine(t, "size")
	c.waitFor(t, "size 100x30")
	record, err := host.ReadRecord(stateDir, "t1")
	if err != nil {
		t.Fatal(err)
	}
	board, err := host.Dial(record)
	if err != nil {
		t.Fatal(err)
	}
	defer board.Close()
	if err := board.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	if err := board.Input([]byte("size\r")); err != nil {
		t.Fatal(err)
	}
	c.waitFor(t, "size 120x40")

	c.typeLine(t, "size")

	shown := c.shown()
	for deadline := time.Now().Add(time.Minute); !strings.Contains(shown[strings.LastIndex(shown, "size 120x40"):], "size 100x30"); shown = c.shown() {
		if time.Now().After(deadline) {
			t.Fatalf("the native terminal never took the attached terminal's size back; the terminal shows %q", shown[max(0, len(shown)-400):])
		}
		time.Sleep(20 * time.Millisecond)
	}
}
