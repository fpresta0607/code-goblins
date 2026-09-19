package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/cleanup"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

const reapUsage = `usage: cfo reap [--dry-run] [--apply] [--force <pid|task-id>]... [--json]

Find the fleet resources nothing else notices and retire them:
an unsupervised harness process whose pane is gone, a dev server left running
in a worktree no pane's agent is working in, whatever its status log says, an
orphaned worktree, task record or status log, and the empty directory a dead
task leaves under .worktrees/.

A harness process is placed by its ancestry and its command line, never by its
image name: the desktop application and the agents of a no-mistakes review
round are not fleet processes and are not reported.

--dry-run is the default and only reports.

--apply acts on every finding it is not holding, and it never ends a process.
That asymmetry is deliberate: everything else this sweep does is recoverable,
an archived log is moved rather than deleted, a removed directory was proven
empty, a returned worktree was proven to hold no unpushed work. Ending a
process is none of those and costs a goblin the round it is in, so a kill is
authorised only by naming that pid with --force. Tidying a status log can
therefore never take a dev server with it.

--apply is otherwise gated: it never removes a worktree with uncommitted or
unpushed work, and never reaps a task that has not finished.

--force names one pid or one task id the operator takes responsibility for,
and may be repeated. Every refusal answers only to its own key: naming a pid
speaks for that process, naming a task id says that task is over, and neither
speaks for the other. A finding held for two reasons therefore needs both
answered, and its HELD line says which keys to name; where a refusal answers
to a key that line does not ask you to name, it also says that the rest is
evidence to resolve rather than override. Some refusals answer to no --force
at all, including the uncommitted or unpushed work gate, because that work is
the whole product of a goblin's run.
`

// runReap sweeps the fleet for leaked resources. Reporting is the default
// because every action this command can take is irreversible.
func runReap(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	options := reap.Options{Force: map[string]bool{}, ProbeIdle: true}
	asJSON := false
	for index := 0; index < len(args); index++ {
		switch arg := args[index]; arg {
		case "--help", "-h":
			fmt.Fprint(stdout, reapUsage)
			return 0
		case "--dry-run":
			options.Apply = false
		case "--apply":
			options.Apply = true
		case "--json":
			asJSON = true
		case "--force":
			index++
			if index >= len(args) {
				fmt.Fprintln(stderr, "cfo reap: --force needs a pid or a task id")
				return 2
			}
			options.Force[args[index]] = true
		default:
			fmt.Fprintf(stderr, "cfo reap: unknown argument %q\n%s", arg, reapUsage)
			return 2
		}
	}
	if runtime.resolveHome == nil || runtime.reap == nil {
		fmt.Fprintln(stderr, "cfo reap: command runtime is incomplete")
		return 1
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !home.IsPrimary(h) {
		fmt.Fprintln(stderr, "cfo reap: not a primary home")
		return 1
	}

	result, err := runtime.reap(context.Background(), h, options)
	// A failed sweep is still worth recording: the next session-start digest
	// must say the fleet is unreadable rather than say it is clean.
	record := reap.Record{Time: time.Now().UTC(), Findings: result.Findings, Notes: result.Notes}
	if err != nil {
		record.Error = err.Error()
	}
	if writeErr := reap.WriteRecord(h.State, record); writeErr != nil {
		fmt.Fprintf(stderr, "cfo reap: record audit: %v\n", writeErr)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(result); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if err := reap.Render(stdout, result); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !options.Apply && len(result.Findings) > 0 {
		fmt.Fprintln(stdout, "Nothing was changed. Run cfo reap --apply to act on the findings above, except to end a process, which needs its pid named with --force.")
	}
	return 0
}

// defaultReap builds the production sweep for one invocation, composing the
// services that already own each primitive: herdr for panes, cleanup for
// worktree return, proc for processor time.
func defaultReap(ctx context.Context, h home.Home, options reap.Options) (reap.Result, error) {
	commands := execx.OSRunner{}
	session := herdrSession()
	client := &herdr.Client{Commands: commands, Session: session}
	service := reap.Service{
		Home: h,
		Inventory: reap.Collector{
			Home:      h,
			Session:   session,
			Panes:     client,
			Processes: reap.CIMProcesses{Commands: commands},
			Commands:  commands,

			ProjectsRoot: install.MachineProjectsRoot,
		},
		Commands: commands,
		CPU:      proc.CPUTime,
		Kill:     func(ctx context.Context, pid int) error { return killTree(ctx, commands, pid) },
		Clean: func(ctx context.Context, id string, forceArchive bool) error {
			_, err := cleanup.Service{
				StateDir:     h.State,
				Commands:     commands,
				Herdr:        client,
				Worktrees:    worktree.Service{Commands: commands},
				ForceArchive: forceArchive,
			}.Cleanup(ctx, id)
			return err
		},
		Return: func(ctx context.Context, project, path string) error {
			return worktree.Service{Commands: commands}.Return(ctx, project, path)
		},
	}
	if options.Apply {
		return service.Apply(ctx, options)
	}
	return service.Audit(ctx, options)
}

// killTree ends a process and everything under it. taskkill /T is what reaches
// the children: a dev server is a shell that spawned the actual server, and
// killing only the parent leaves the port held.
func killTree(ctx context.Context, commands execx.Runner, pid int) error {
	result, err := commands.Run(ctx, execx.Request{
		Name: "taskkill",
		Args: []string{"/PID", strconv.Itoa(pid), "/T", "/F"},
	})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("taskkill for pid %d exited with code %d: %s", pid, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return nil
}
