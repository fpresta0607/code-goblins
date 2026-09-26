package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
)

// peekTerminal is the tail of target's terminal: a native terminal's screen as
// its console holds it, or else the Herdr pane's tail.
func peekTerminal(ctx context.Context, h home.Home, target string, lines int) (string, error) {
	if record, err := host.ReadRecord(h.State, target); err == nil {
		rows, err := host.ReadScreen(record)
		if err != nil {
			return "", err
		}
		return host.ScreenTail(rows, lines), nil
	}
	client := &herdr.Client{Commands: execx.OSRunner{}}
	return fleet.Peeker{Resolve: fleet.Resolver{StateDir: h.State}, Terminal: client}.Tail(ctx, target, lines)
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
	output, err := runtime.peek(context.Background(), h, args[0], lines)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprint(stdout, output)
	return 0
}
