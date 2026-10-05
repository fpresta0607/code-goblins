package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
)

// peekTerminal is the tail of target's native terminal: its screen as its
// console holds it. target names a task, by its id or as gb-<id>, or any
// other native terminal of this home, such as the CFO's.
func peekTerminal(h home.Home, target string, lines int) (string, error) {
	id := target
	if meta, native := fleet.NativeTask(h.State, target); native {
		id = meta.ID
	}
	record, err := host.ReadRecord(h.State, id)
	if err != nil {
		return "", fmt.Errorf("cfo peek: %s has no native terminal running: %w", target, err)
	}
	rows, err := host.ReadScreen(record)
	if err != nil {
		return "", err
	}
	return host.ScreenTail(rows, lines), nil
}

func runPeek(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "cfo peek: target is required")
		return 2
	}
	if strings.HasPrefix(args[0], "-") {
		fmt.Fprintf(stderr, "cfo peek: unknown flag %q\n", args[0])
		return 2
	}
	if len(args) > 2 {
		fmt.Fprintln(stderr, "cfo peek: expected at most one line count")
		return 2
	}
	lines := 0
	if len(args) == 2 {
		parsed, err := strconv.Atoi(args[1])
		if err != nil {
			fmt.Fprintf(stderr, "cfo peek: invalid line count %q\n", args[1])
			return 2
		}
		lines = parsed
	}
	if runtime.resolveHome == nil || runtime.peek == nil {
		fmt.Fprintln(stderr, "cfo peek: command runtime is incomplete")
		return 1
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	output, err := runtime.peek(h, args[0], lines)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprint(stdout, output)
	return 0
}
