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
	"github.com/fpresta0607/code-goblins/internal/treehouse"
)

const cleanupUsage = `usage: cfo cleanup <id>

Close the task tab and return one clean, proven-inactive task worktree
through treehouse.
Refuses dirty worktrees, active agents, ambiguous identity, and the primary
checkout. There is no force override.
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
	if strings.HasPrefix(args[0], "-") {
		fmt.Fprintf(stderr, "cfo cleanup: unknown flag %q\n", args[0])
		return 2
	}
	if len(args) != 1 {
		fmt.Fprintln(stderr, "cfo cleanup: unexpected arguments")
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
	output, err := runtime.cleanup(context.Background(), h, args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, output)
	return 0
}

// defaultCleanup builds the production cleanup service for one invocation.
func defaultCleanup(ctx context.Context, h home.Home, id string) (string, error) {
	commands := execx.OSRunner{}
	service := cleanup.Service{
		StateDir:  h.State,
		Commands:  commands,
		Herdr:     &herdr.Client{Commands: commands, Session: herdrSession()},
		Treehouse: treehouse.Service{Commands: commands},
	}
	result, err := service.Cleanup(ctx, id)
	if err != nil {
		return "", err
	}
	return result.Output, nil
}
