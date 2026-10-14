package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// windowProofVariable names the default terminal of the desktop a test that
// shows windows may run on, console host or Windows Terminal. Only the
// hosted runner of .github/workflows/windows.yml sets it: such a test never
// runs on a desktop a person uses.
const windowProofVariable = "CFO_WINDOW_PROOF"

// ownConsoleTestProgram runs this binary as a cfo command started alone in a
// new console: it calls leaveOwnConsole with the arguments after its report,
// writes to the report the console window it is left with, and stays a
// while, as a run that waits for its turn does.
const ownConsoleTestProgram = "own-console-test-program"

func runOwnConsoleTestProgram(args []string) int {
	leaveOwnConsole(args[1:], false)
	window, _, _ := consoleWindowProc.Call()
	if err := os.WriteFile(args[0]+".tmp", fmt.Appendf(nil, "window=%d", window), 0o600); err != nil || os.Rename(args[0]+".tmp", args[0]) != nil {
		return 1
	}
	time.Sleep(15 * time.Second)
	return 0
}

// visibleWindows lists the visible top-level windows of the desktop by
// handle, each with its class and the program that owns it.
func visibleWindows(t *testing.T) map[windows.HWND]string {
	t.Helper()
	found := map[windows.HWND]string{}
	callback := syscall.NewCallback(func(window windows.HWND, _ uintptr) uintptr {
		if !windows.IsWindowVisible(window) {
			return 1
		}
		var pid uint32
		_, _ = windows.GetWindowThreadProcessId(window, &pid)
		class := make([]uint16, 256)
		copied, _ := windows.GetClassName(window, &class[0], int32(len(class)))
		found[window] = fmt.Sprintf("%s of pid %d", windows.UTF16ToString(class[:copied]), pid)
		return 1
	})
	if err := windows.EnumWindows(callback, nil); err != nil {
		t.Fatal(err)
	}
	return found
}

// A command a program starts alone in a new console, as a goblin's careless
// background start does, leaves that console as it starts, and the window
// Windows showed for it, the console host's or Windows Terminal's, is gone
// a moment later. A command the Overlord starts to look at, cfo attach,
// keeps its window, which also proves the desktop shows a new console here.
func TestACommandStartedAloneInANewConsoleClosesItsWindow(t *testing.T) {
	terminal := os.Getenv(windowProofVariable)
	if terminal == "" {
		t.Skip("shows a window on the desktop, so it runs only on the hosted runner of .github/workflows/windows.yml")
	}
	for _, c := range []struct {
		args       []string
		isWindowed bool
	}{
		{args: []string{"gate", "turn", "--", "go", "test", "./..."}},
		{args: []string{"verify", "cg-example"}},
		{args: []string{"attach"}, isWindowed: true},
	} {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			// Arrange
			report := filepath.Join(t.TempDir(), "report")
			before := visibleWindows(t)
			command := exec.Command(os.Args[0], append([]string{ownConsoleTestProgram, report}, c.args...)...)
			command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE}

			// Act
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = command.Process.Kill()
				_ = command.Wait()
			})
			eventually(t, "the command reports its console", func() bool {
				_, err := os.Stat(report)
				return err == nil
			})
			time.Sleep(time.Second)

			// Assert
			var shown []string
			for window, what := range visibleWindows(t) {
				if _, isOld := before[window]; !isOld {
					shown = append(shown, what)
				}
			}
			slices.Sort(shown)
			got, err := fsx.ReadFile(report)
			if err != nil {
				t.Fatal(err)
			}
			if c.isWindowed {
				if len(shown) == 0 || string(got) == "window=0" {
					t.Fatalf("with %s as the default terminal, cfo %s reported %s and the desktop shows no new window; want its console's window shown", terminal, strings.Join(c.args, " "), got)
				}
				return
			}
			if string(got) != "window=0" || len(shown) != 0 {
				t.Fatalf("with %s as the default terminal, cfo %s reported %s and a second after it the desktop still shows %q; want no console and no window", terminal, strings.Join(c.args, " "), got, shown)
			}
		})
	}
}

// What the Overlord starts to look at keeps its console; every other
// command started alone in a new console was started by a program.
func TestIsShownOnPurposeKeepsOnlyWhatTheOverlordStartsToLookAt(t *testing.T) {
	for _, c := range []struct {
		args      []string
		isGoblins bool
		want      bool
	}{
		{args: nil, isGoblins: true, want: true},
		{args: nil, want: true},
		{args: []string{"--harness", "codex"}, isGoblins: true, want: true},
		{args: []string{"--board"}, isGoblins: true, want: true},
		{args: []string{"setup"}, isGoblins: true, want: true},
		{args: []string{"resume"}, want: true},
		{args: []string{"attach", "cg-example"}, want: true},
		{args: []string{"--board"}},
		{args: []string{"gate", "turn", "--", "go", "test", "./..."}, isGoblins: true},
		{args: []string{"verify", "cg-example"}},
		{args: []string{"serve"}, isGoblins: true},
		{args: []string{"host", "--id", "cg-example"}},
		{args: []string{"notify", "cg-example", "--working", "x"}, isGoblins: true},
	} {
		// Act
		got := isShownOnPurpose(c.args, c.isGoblins)

		// Assert
		if got != c.want {
			t.Errorf("isShownOnPurpose(%q, goblins %t) = %t; want %t", c.args, c.isGoblins, got, c.want)
		}
	}
}
