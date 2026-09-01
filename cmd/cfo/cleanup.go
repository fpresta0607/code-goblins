package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/cleanup"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

const cleanupUsage = `usage: cfo cleanup <id> [--force-archive]

Close the task tab and return one clean, proven-inactive task worktree,
removing the worktree and pruning its Git administrative entry.
Refuses dirty worktrees, active agents, ambiguous identity, and the primary
checkout.

--force-archive retires a task whose worktree can no longer be validated (a
directory pinned by a dead handle, or already gone). It archives the task
record and leaves the directory untouched - nothing on disk is deleted - and
still refuses a pane with a live agent.
`

// runCleanup returns one validated task's worktree through the guarded
// cleanup service. It accepts only a local task ID, never a raw path or an
// explicit Herdr target.
func runCleanup(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "cfo cleanup: task ID is required")
		fmt.Fprint(stderr, cleanupUsage)
		return 2
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(stdout, cleanupUsage)
		return 0
	}
	var id string
	force := false
	for _, arg := range args {
		switch {
		case arg == "--force-archive":
			force = true
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(stderr, "cfo cleanup: unknown flag %q\n", arg)
			return 2
		case id == "":
			id = arg
		default:
			fmt.Fprintln(stderr, "cfo cleanup: unexpected arguments")
			return 2
		}
	}
	if id == "" {
		fmt.Fprintln(stderr, "cfo cleanup: task ID is required")
		return 2
	}
	if runtime.resolveHome == nil || runtime.cleanup == nil {
		fmt.Fprintln(stderr, "cfo cleanup: command runtime is incomplete")
		return 1
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !home.IsPrimary(h) {
		fmt.Fprintln(stderr, "cfo cleanup: not a primary home")
		return 1
	}
	output, err := runtime.cleanup(context.Background(), h, id, force)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, output)
	return 0
}

// defaultCleanup builds the production cleanup service for one invocation.
func defaultCleanup(ctx context.Context, h home.Home, id string, forceArchive bool) (string, error) {
	commands := execx.OSRunner{}
	service := cleanup.Service{
		StateDir:  h.State,
		Commands:  commands,
		Herdr:     &herdr.Client{Commands: commands, Session: herdrSession()},
		Worktrees: worktree.Service{Commands: commands},
		ForceArchive: forceArchive,
	}
	result, err := service.Cleanup(ctx, id)
	if err != nil {
		return "", err
	}
	return result.Output, nil
}
