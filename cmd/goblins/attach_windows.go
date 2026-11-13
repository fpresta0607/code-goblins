package main

import (
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/host"
)

// attachNative shows native terminal id in this console until the terminal
// ends, which returns its exit code, or the Overlord presses Ctrl-], which
// leaves it running and returns 0. Keys reach the terminal as this console
// reads them, and the terminal follows this console's size: when another
// viewer, such as the board, gave it another size, the next key typed here
// takes it back first.
func attachNative(stateDir, id string, stdout, stderr io.Writer) int {
	record, err := host.ReadRecord(stateDir, id)
	if err != nil {
		fmt.Fprintf(stderr, "goblins attach: no native terminal %s is running: %v\n", id, err)
		return 1
	}
	if record.Stream {
		fmt.Fprintf(stderr, "goblins attach: %s runs on stream-json pipes, with no terminal to show; goblins peek %s shows what it is doing\n", id, id)
		return 1
	}
	in, out := windows.Handle(os.Stdin.Fd()), windows.Handle(os.Stdout.Fd())
	var inMode, outMode uint32
	if windows.GetConsoleMode(in, &inMode) != nil || windows.GetConsoleMode(out, &outMode) != nil {
		fmt.Fprintln(stderr, "goblins attach: a native terminal is shown in a console, and this command has none")
		return 1
	}
	client, err := host.View(record)
	if err != nil {
		fmt.Fprintf(stderr, "goblins attach: native terminal %s does not answer: %v\n", id, err)
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
	// isOwner is whether the terminal has this console's size, as far as the
	// sizes the host told say.
	var isOwner atomic.Bool
	isOwner.Store(true)

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
			case event.Cols > 0:
				c, r := size()
				isOwner.Store(event.Cols == c && event.Rows == r)
				continue
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
			if len(typed) > 0 && !isOwner.Load() {
				if c, r := size(); c > 0 {
					isOwner.Store(true)
					_ = client.Resize(c, r)
				}
			}
			if len(typed) > 0 && client.Input(typed) != nil {
				ended <- ending{"The native terminal's host stopped answering.", 1}
				return
			}
			if left {
				ended <- ending{"Left native terminal " + id + " running; goblins attach brings it back.", 0}
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
