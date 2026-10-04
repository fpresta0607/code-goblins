package main

import (
	"fmt"
	"io"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
)

const backlogUsage = "usage: cfo backlog done <id>\n"

func runBacklog(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(stdout, backlogUsage)
		return 0
	}
	if len(args) != 2 || args[0] != "done" {
		fmt.Fprint(stderr, backlogUsage)
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !home.IsPrimary(h) {
		fmt.Fprintln(stderr, "cfo backlog: not a primary home")
		return 1
	}
	if err := fleet.CompleteQueuedTask(h, args[1]); err != nil {
		fmt.Fprintf(stderr, "cfo backlog: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "closed backlog row %s; delivery outcome and archived source kept\n", args[1])
	return 0
}
