package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// attachNative shows native terminal id in this console until the terminal
// ends, which returns its exit code, or the Overlord presses Ctrl-], which
// leaves it running and returns 0. Keys reach the terminal as this console
// reads them, and the terminal follows this console's size: when another
// viewer, such as the board, gave it another size, the next key typed here
// takes it back first.
func attachNative(stateDir, id string, stdout, stderr io.Writer) int {
	record, ok := nativeRecord(stateDir, id, stderr)
	if !ok {
		return 1
	}
	in, out := windows.Handle(os.Stdin.Fd()), windows.Handle(os.Stdout.Fd())
	var inMode, outMode uint32
	if windows.GetConsoleMode(in, &inMode) != nil || windows.GetConsoleMode(out, &outMode) != nil {
		fmt.Fprintln(stderr, "goblins attach: a native terminal is shown in a console, and this command has none")
		return 1
	}
	client, ok := viewRecord(record, id, stderr)
	if !ok {
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
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	return viewNative(client, id, os.Stdin, stdout, size, tick.C)
}
