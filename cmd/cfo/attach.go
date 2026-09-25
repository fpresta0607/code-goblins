package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/host"
)

// nativeCFOTerminal is the native terminal goblins --native starts the CFO in.
const nativeCFOTerminal = "cfo"

// detachKey is Ctrl-], which leaves an attached terminal running.
const detachKey = 0x1d

// windowsInputMode asks a console for keys as Windows key events.
const windowsInputMode = "\x1b[?9001h"

// terminalReset undoes what a terminal's program commonly sets and would
// leave behind in this console: a hidden cursor, bracketed paste and focus
// reporting. Windows input mode stays, since turning it off could change the
// input mode of whatever hosts this console.
const terminalReset = "\x1b[?25h\x1b[?2004l\x1b[?1004l"

// runAttach shows a native terminal in this console: the one named, or else
// the one the registered CFO runs in. With no CFO registered, that is terminal
// cfo while its host answers, since the CFO started there may not have
// registered yet.
func runAttach(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	f := flag.NewFlagSet("attach", flag.ContinueOnError)
	f.SetOutput(stderr)
	if err := f.Parse(args); err != nil || f.NArg() > 1 {
		fmt.Fprintln(stderr, "usage: cfo attach [terminal]")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	id := f.Arg(0)
	if id == "" {
		native, live := runtime.nativeCFO(h.State)
		_, inHerdr := runtime.liveCFO(h.State)
		switch {
		case live:
			id = native
		case inHerdr:
			fmt.Fprintln(stderr, "cfo attach: the CFO runs in Herdr, not in a native terminal; name the terminal to attach to")
			return 1
		case runtime.nativeTerminalRuns(h.State, nativeCFOTerminal):
			id = nativeCFOTerminal
		default:
			fmt.Fprintln(stderr, "cfo attach: no CFO runs in a native terminal; name the terminal to attach to")
			return 1
		}
	}
	return runtime.attachNative(h.State, id, stdout, stderr)
}

// nativeTerminalRuns reports whether native terminal id's host answers.
func nativeTerminalRuns(stateDir, id string) bool {
	record, err := host.ReadRecord(stateDir, id)
	if err != nil {
		return false
	}
	client, err := host.Dial(record)
	if err != nil {
		return false
	}
	_ = client.Close()
	return true
}

// attachNative shows native terminal id in this console until the terminal
// ends, which returns its exit code, or the Overlord presses Ctrl-], which
// leaves it running and returns 0. Keys reach the terminal as this console
// reads them, and the terminal follows this console's size.
func attachNative(stateDir, id string, stdout, stderr io.Writer) int {
	record, err := host.ReadRecord(stateDir, id)
	if err != nil {
		fmt.Fprintf(stderr, "cfo attach: no native terminal %s is running: %v\n", id, err)
		return 1
	}
	in, out := windows.Handle(os.Stdin.Fd()), windows.Handle(os.Stdout.Fd())
	var inMode, outMode uint32
	if windows.GetConsoleMode(in, &inMode) != nil || windows.GetConsoleMode(out, &outMode) != nil {
		fmt.Fprintln(stderr, "cfo attach: a native terminal is shown in a console, and this command has none")
		return 1
	}
	client, err := host.Dial(record)
	if err != nil {
		fmt.Fprintf(stderr, "cfo attach: native terminal %s does not answer: %v\n", id, err)
		return 1
	}
	defer client.Close()
	// Keys go to the terminal as a terminal sends them, Ctrl-C included, and
	// this console draws the terminal's output as a terminal would. Keys are
	// asked for as Windows key events, which the terminal's pseudo console
	// always takes, so every key such as Shift+Enter arrives exactly however
	// little of the terminal's history is left to replay its own request.
	_ = windows.SetConsoleMode(in, inMode&^(windows.ENABLE_ECHO_INPUT|windows.ENABLE_LINE_INPUT|windows.ENABLE_PROCESSED_INPUT)|windows.ENABLE_VIRTUAL_TERMINAL_INPUT)
	defer windows.SetConsoleMode(in, inMode)
	_ = windows.SetConsoleMode(out, outMode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING|windows.DISABLE_NEWLINE_AUTO_RETURN)
	defer windows.SetConsoleMode(out, outMode)
	_, _ = io.WriteString(stdout, windowsInputMode)

	size := func() (int, int) {
		var info windows.ConsoleScreenBufferInfo
		if windows.GetConsoleScreenBufferInfo(out, &info) != nil {
			return 0, 0
		}
		return int(info.Window.Right-info.Window.Left) + 1, int(info.Window.Bottom-info.Window.Top) + 1
	}
	cols, rows := size()
	_ = client.Resize(cols, rows)

	type ending struct {
		reason string
		code   int
	}
	ended := make(chan ending, 2)
	go func() {
		for {
			event, err := client.Next()
			switch {
			case err != nil:
				ended <- ending{"The native terminal's host stopped answering.", 1}
				return
			case event.Exited:
				ended <- ending{fmt.Sprintf("The native terminal ended with exit code %d.", event.Code), int(event.Code)}
				return
			}
			_, _ = stdout.Write(event.Output)
		}
	}()
	go func() {
		keys := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(keys)
			if err != nil {
				ended <- ending{"This console stopped reading keys; native terminal " + id + " keeps running.", 1}
				return
			}
			typed := keys[:n]
			left := false
			if at := detachAt(typed); at >= 0 {
				typed, left = typed[:at], true
			}
			if len(typed) > 0 && client.Input(typed) != nil {
				ended <- ending{"The native terminal's host stopped answering.", 1}
				return
			}
			if left {
				ended <- ending{"Left native terminal " + id + " running; cfo attach brings it back.", 0}
				return
			}
		}
	}()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case end := <-ended:
			fmt.Fprintf(stdout, "\r\n%s\r\n", end.reason)
			_, _ = io.WriteString(stdout, terminalReset)
			return end.code
		case <-tick.C:
			if c, r := size(); c > 0 && (c != cols || r != rows) {
				cols, rows = c, r
				_ = client.Resize(c, r)
			}
		}
	}
}

// detachAt returns where keys holds Ctrl-] pressed, or -1. A console sends it
// as the byte itself, or, in the Windows input mode cfo attach asks for and
// the terminal's pseudo console asks for too, as a Windows key event:
// ESC [ Vk;Sc;Uc;Kd;Cs;Rc _, here with the character 0x1d and the key down.
func detachAt(keys []byte) int {
	for i := 0; i < len(keys); i++ {
		if keys[i] == detachKey {
			return i
		}
		if keys[i] != 0x1b || i+1 >= len(keys) || keys[i+1] != '[' {
			continue
		}
		end := i + 2
		for end < len(keys) && (keys[end] >= '0' && keys[end] <= '9' || keys[end] == ';') {
			end++
		}
		if end < len(keys) && keys[end] == '_' {
			if fields := strings.Split(string(keys[i+2:end]), ";"); len(fields) == 6 && fields[2] == "29" && fields[3] == "1" {
				return i
			}
		}
	}
	return -1
}

// startNativeCFO starts Claude Code as the CFO in native terminal cfo, in
// project, in a host of its own that outlives this console.
func startNativeCFO(stateDir, project string) error {
	claude, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude is not on PATH: %w", err)
	}
	// A native terminal starts a program itself, with no shell to run a
	// script shim.
	if !strings.EqualFold(filepath.Ext(claude), ".exe") {
		return fmt.Errorf("%s is not a program a native terminal can start; the native build of Claude Code is claude.exe", claude)
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	_, err = host.Launch(stateDir, []string{self, "host"}, nativeCFOEnvironment(os.Environ()), host.Spec{ID: nativeCFOTerminal, Args: []string{claude}, Dir: project, Cols: 120, Rows: 40})
	return err
}

// nativeCFOEnvironment is env without the Herdr pane a launcher run inside
// Herdr has, so the CFO registers its native terminal rather than that pane.
func nativeCFOEnvironment(env []string) []string {
	return slices.DeleteFunc(env, func(entry string) bool {
		return strings.HasPrefix(strings.ToUpper(entry), "HERDR_PANE_ID=")
	})
}
