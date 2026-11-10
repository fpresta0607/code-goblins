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
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

const cleanupUsage = `usage: ceo cleanup <id> [--force-archive]

Close the task tab and return one clean, proven-inactive task worktree,
removing the worktree and pruning its Git administrative entry.
Refuses dirty worktrees, active agents, ambiguous identity, the primary
checkout, and a task whose gate run, which a pause leaves running, is still
open or cannot be read: ceo kill <id> aborts that run after keeping its
commits and retires the task. While the task's worktree is gone or no longer
a Git worktree, ceo kill cannot find the run (issue #2246), so end it with
no-mistakes axi abort --run <run> and run the cleanup again. It holds the
task against every other command while it runs, so a pause, a resume, a
kill or a switch of the task that starts meanwhile is refused, and it is
refused itself, before it removes anything, while one of those is in flight.

--force-archive retires a task whose worktree can no longer be validated (a
directory pinned by a dead handle, or already gone). It archives the task
record and leaves the directory untouched - nothing on disk is deleted - and
still refuses a pane with a live agent and a gate run that is still open or
cannot be read, as cleanup does. A paused task's run is the one its pause
saved, read whatever state the worktree is in; the worktree's branch's run
is read as well while the folder is still a Git worktree, so a worktree gone,
or a folder no longer a Git worktree, with no saved run names no run.
`

// runCleanup returns one validated task's worktree through the guarded
// cleanup service. It accepts only a local task ID, never a raw path or an
// explicit Herdr target.
func runCleanup(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ceo cleanup: task ID is required")
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
			fmt.Fprintf(stderr, "ceo cleanup: unknown flag %q\n", arg)
			return 2
		case id == "":
			id = arg
		default:
			fmt.Fprintln(stderr, "ceo cleanup: unexpected arguments")
			return 2
		}
	}
	if id == "" {
		fmt.Fprintln(stderr, "ceo cleanup: task ID is required")
		return 2
	}
	if runtime.resolveHome == nil || runtime.cleanup == nil {
		fmt.Fprintln(stderr, "ceo cleanup: command runtime is incomplete")
		return 1
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !home.IsPrimary(h) {
		fmt.Fprintln(stderr, "ceo cleanup: not a primary home")
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
		StateDir:     h.State,
		Data:         h.Data,
		Commands:     commands,
		Terminal:     &herdr.Client{Commands: commands, Session: herdrSession()},
		Worktrees:    worktree.Service{Commands: commands},
		ForceArchive: forceArchive,

		EndLeft: func(ctx context.Context, meta state.TaskMeta) ([]string, error) {
			return endLeftProcesses(ctx, h, meta)
		},
		ReleaseServices: defaultProjectServices(h, io.Discard).ReleaseTask,
		HandOver: func(meta state.TaskMeta) (bool, error) {
			return supervisor.HandOverRetiredNote(h.State, meta)
		},
		OpenGateRun: openGateRun(h),
	}
	result, err := service.Cleanup(ctx, id)
	if err != nil {
		return "", err
	}
	return result.Output, nil
}
