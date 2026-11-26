//go:build linux || darwin

package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"

	"golang.org/x/sys/unix"

	"github.com/fpresta0607/code-goblins/internal/pty"
)

// attachNative shows native terminal id in this terminal until the terminal
// ends, which returns its exit code, or the Overlord presses Ctrl-], which
// leaves it running and returns 0. This terminal is in raw mode meanwhile,
// so keys reach the native terminal as typed, Ctrl-C and Ctrl-Z included,
// and its output is drawn as written; the native terminal follows this one's
// size, which SIGWINCH says has changed, and when another viewer, such as the
// board, gave it another size, the next key typed here takes it back first.
func attachNative(stateDir, id string, stdout, stderr io.Writer) int {
	record, ok := nativeRecord(stateDir, id, stderr)
	if !ok {
		return 1
	}
	in, out := int(os.Stdin.Fd()), int(os.Stdout.Fd())
	if _, _, err := pty.Size(out); err != nil {
		fmt.Fprintln(stderr, "goblins attach: a native terminal is shown in a terminal, and this command has none")
		return 1
	}
	client, ok := viewRecord(record, id, stderr)
	if !ok {
		return 1
	}
	defer client.Close()
	resized := make(chan os.Signal, 1)
	signal.Notify(resized, unix.SIGWINCH)
	defer signal.Stop(resized)
	restore, err := pty.Raw(in, true)
	if err != nil {
		fmt.Fprintln(stderr, "goblins attach: a native terminal is shown in a terminal, and this command has none")
		return 1
	}
	defer restore()
	size := func() (int, int) {
		cols, rows, err := pty.Size(out)
		if err != nil {
			return 0, 0
		}
		return cols, rows
	}
	return viewNative(client, id, os.Stdin, stdout, size, resized)
}
