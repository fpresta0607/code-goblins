package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/cleanup"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
	"github.com/fpresta0607/code-goblins/internal/githubapi"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/tickets"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

const cleanupUsage = `usage: goblins cleanup <id> [--force-archive] [--keep-issue-open <issue>... --reason "<why>"]

Close the task tab and return one clean, proven-inactive task worktree,
removing the worktree and pruning its Git administrative entry.
Refuses dirty worktrees, active agents, ambiguous identity, the primary
checkout, and a task whose gate run, which a pause leaves running, is still
open or cannot be read: goblins kill <id> aborts that run after keeping its
commits and retires the task. While the task's worktree is gone or no longer
a Git worktree, goblins kill cannot find the run (issue #2246), so end it with
no-mistakes axi abort --run <run> and run the cleanup again. It holds the
task against every other command while it runs, so a pause, a resume, a
kill or a switch of the task that starts meanwhile is refused, and it is
refused itself, before it removes anything, while one of those is in flight.

It refuses as well, naming each, while an issue the task names is still
open on GitHub: an issue its brief names in its Task or Acceptance criteria,
one its gate runs were started to close with --closes, and one its pull
requests name, which GitHub links them to close or their body names after
a closing word or "Part of". A task whose issues cannot be read is refused
too. Close each issue once its work is on its project's main, or retire the
task with it open by --keep-issue-open <issue>, written owner/name#N or as
its address, once for each, with --reason "<why>", which the task's status
log records for each kept issue. goblins kill, which stops work unfinished,
does not check issues.

--force-archive retires a task whose worktree can no longer be validated (a
directory pinned by a dead handle, or already gone). It archives the task
record and leaves the directory untouched - nothing on disk is deleted - and
still refuses a pane with a live agent, a gate run that is still open or
cannot be read, and an open issue, as cleanup does. A paused task's run is
the one its pause saved, read whatever state the worktree is in; the
worktree's branch's run is read as well while the folder is still a Git
worktree, so a worktree gone, or a folder no longer a Git worktree, with no
saved run names no run.
`

// cleanupRequest is one retirement of a task: goblins cleanup's, which keeps
// the task while an issue it names is open unless the CEO keeps the issue
// open with a reason, or goblins kill's, which stops work unfinished and so
// checks no issue.
type cleanupRequest struct {
	ID                string
	ForceArchive      bool
	ShouldCheckIssues bool
	// KeepOpen are the open issues, each owner/name#N, the task retires with,
	// and KeepReason is why.
	KeepOpen   []string
	KeepReason string
}

// runCleanup returns one validated task's worktree through the guarded
// cleanup service. It accepts only a local task ID, never a raw path or an
// explicit Herdr target.
func runCleanup(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "goblins cleanup: task ID is required")
		fmt.Fprint(stderr, cleanupUsage)
		return 2
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(stdout, cleanupUsage)
		return 0
	}
	request := cleanupRequest{ShouldCheckIssues: true}
	isReasonGiven := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--force-archive":
			request.ForceArchive = true
		case arg == "--keep-issue-open" || arg == "--reason":
			if index+1 == len(args) {
				fmt.Fprintf(stderr, "goblins cleanup: %s needs a value\n", arg)
				return 2
			}
			index++
			if arg == "--reason" {
				request.KeepReason, isReasonGiven = strings.TrimSpace(args[index]), true
				continue
			}
			issue, isIssue := tickets.ParseIssueRef(args[index])
			if !isIssue {
				fmt.Fprintf(stderr, "goblins cleanup: --keep-issue-open %q is not an issue: write it owner/name#N or as its address\n", args[index])
				return 2
			}
			request.KeepOpen = append(request.KeepOpen, issue.String())
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(stderr, "goblins cleanup: unknown flag %q\n", arg)
			return 2
		case request.ID == "":
			request.ID = arg
		default:
			fmt.Fprintln(stderr, "goblins cleanup: unexpected arguments")
			return 2
		}
	}
	if request.ID == "" {
		fmt.Fprintln(stderr, "goblins cleanup: task ID is required")
		return 2
	}
	if len(request.KeepOpen) > 0 && request.KeepReason == "" {
		fmt.Fprintln(stderr, `goblins cleanup: --keep-issue-open needs --reason "<why>", which the task records`)
		return 2
	}
	if isReasonGiven && len(request.KeepOpen) == 0 {
		fmt.Fprintln(stderr, "goblins cleanup: --reason says why an issue is kept open, so it needs --keep-issue-open <issue>")
		return 2
	}
	if runtime.resolveHome == nil || runtime.cleanup == nil {
		fmt.Fprintln(stderr, "goblins cleanup: command runtime is incomplete")
		return 1
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !home.IsPrimary(h) {
		fmt.Fprintln(stderr, "goblins cleanup: not a primary home")
		return 1
	}
	output, err := runtime.cleanup(context.Background(), h, request)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, output)
	return 0
}

// defaultCleanup builds the production cleanup service for one invocation;
// github reads the issues of a request that checks them.
func defaultCleanup(ctx context.Context, h home.Home, request cleanupRequest, github *githubapi.Client) (string, error) {
	commands := execx.OSRunner{}
	service := cleanup.Service{
		StateDir:     h.State,
		Data:         h.Data,
		Commands:     commands,
		Terminal:     &herdr.Client{Commands: commands, Session: herdrSession()},
		Worktrees:    worktree.Service{Commands: commands},
		ForceArchive: request.ForceArchive,
		KeepOpen:     request.KeepOpen,
		KeepReason:   request.KeepReason,

		EndLeft: func(ctx context.Context, meta state.TaskMeta) ([]string, error) {
			return endLeftProcesses(ctx, h, meta)
		},
		ReleaseServices: defaultProjectServices(h, io.Discard).ReleaseTask,
		HandOver: func(meta state.TaskMeta) (bool, error) {
			return supervisor.HandOverRetiredNote(h.State, meta)
		},
		OpenGateRun: openGateRun(h),
	}
	if request.ShouldCheckIssues {
		gate := pipeline.Reader{Commands: gatedb.Runner{Next: commands}}
		root, err := pipeline.DefaultRoot()
		if err != nil {
			return "", fmt.Errorf("cleanup: find the gate's home, whose runs say which issues the task closes: %w", err)
		}
		gate.Root = root
		service.OpenIssues = openTaskIssues(h, tickets.GitHub{Commands: commands, API: github}, gate)
	}
	result, err := service.Cleanup(ctx, request.ID)
	if err != nil {
		return "", err
	}
	return result.Output, nil
}
