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
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

const reapUsage = `usage: cfo reap [--dry-run] [--apply] [--force <pid|task-id>]... [--json]

Find the fleet resources nothing else notices and, with --apply, retire them:
an unsupervised harness process whose pane is gone, a dev server left running
in a finished goblin's worktree, an orphaned worktree, task record or status
log.

--dry-run is the default and only reports. --apply acts, behind gates that
never kill a process still burning processor time, never remove a worktree
with uncommitted or unpushed work, and never reap a task that has not
finished.

--force names one pid or one task id the operator takes responsibility for,
and may be repeated. It clears the idleness gate for that pid and the
terminal-status gate for that task. It never clears the uncommitted or
unpushed work gate: that work is the whole product of a goblin's run.
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
		fmt.Fprintln(stdout, "Nothing was changed. Run cfo reap --apply to act on the findings above.")
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
