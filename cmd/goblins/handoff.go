package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
	"github.com/fpresta0607/code-goblins/internal/handoff"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

const handoffUsage = "goblins handoff <task> [--wip] | handoff return <task> [--pr <url>] | handoff cancel <task>"

// runHandoff runs goblins handoff, handoff return and handoff cancel.
func runHandoff(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	action := "handoff"
	if len(args) > 0 && (args[0] == "return" || args[0] == "cancel") {
		action, args = args[0], args[1:]
	}
	if len(args) == 0 || state.ValidTaskID(args[0]) != nil {
		fmt.Fprintf(stderr, "goblins handoff: %s\n", handoffUsage)
		return 2
	}
	id := args[0]
	flags := flag.NewFlagSet("handoff "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	isWIP, pullRequest := false, ""
	switch action {
	case "handoff":
		flags.BoolVar(&isWIP, "wip", false, "commit the worktree's uncommitted changes as wip(handoff): <task> before handing off")
	case "return":
		flags.StringVar(&pullRequest, "pr", "", "the cloud's pull request to bring back, when more than one targets the branch")
	}
	if !parseFlags(flags, args[1:]) {
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !home.IsPrimary(h) {
		fmt.Fprintln(stderr, "goblins handoff requires a primary home")
		return 1
	}
	service, err := handoffService(runtime, h)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx := context.Background()
	var record handoff.Record
	var prompt string
	switch action {
	case "handoff":
		record, prompt, err = service.Handoff(ctx, id, isWIP)
	case "return":
		record, err = service.Return(ctx, id, pullRequest)
	case "cancel":
		record, err = service.Cancel(ctx, id)
	}
	if err != nil {
		fmt.Fprintf(stderr, "goblins handoff %s: %v\n", action, err)
		steps := record.Steps
		if action == "return" {
			steps = record.ReturnSteps
		}
		if len(steps) > 0 {
			fmt.Fprintf(stderr, "steps that ran: %v\n", steps)
		}
		return 1
	}
	if prompt != "" {
		fmt.Fprintf(stderr, "Paste this into a cloud session on the repository:\n\n%s\n\n", prompt)
	}
	if err := json.NewEncoder(stdout).Encode(record); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func handoffService(runtime commandRuntime, h home.Home) (handoff.Service, error) {
	if runtime.taskLifecycle == nil {
		return handoff.Service{}, errors.New("task lifecycle is unavailable")
	}
	commands := runtime.github
	if commands == nil {
		commands = execx.OSRunner{}
	}
	root, err := pipeline.DefaultRoot()
	if err != nil {
		return handoff.Service{}, err
	}
	gate := pipeline.Reader{Root: root, Commands: gatedb.Runner{Next: execx.OSRunner{}}}
	github := tickets.GitHub{Commands: commands, API: runtime.githubAPI}
	return handoff.Service{
		StateDir: h.State,
		DataDir:  h.Data,
		Commands: commands,
		Lifecycle: func(ctx context.Context, request lifecycle.Request) (state.Lifecycle, error) {
			return runtime.taskLifecycle(ctx, h, request, "")
		},
		// A gate whose state cannot be read may hold the branch, so the
		// handoff is refused rather than run beside it.
		GateRun: func(ctx context.Context, meta state.TaskMeta, branch string) (string, error) {
			run, err := gate.Interruption(ctx, meta.Project, branch)
			if errors.Is(err, pipeline.ErrNoProgress) {
				return "", nil
			}
			if err != nil || run.IsTerminal() {
				return "", err
			}
			return run.ID, nil
		},
		// A packet is screened as a public issue on its repository is, on
		// every repository, so the screen is always read as public.
		Screen: func(ctx context.Context, meta state.TaskMeta, repository, text string) (tickets.Screen, error) {
			return issueScreen(ctx, github, runtime, h, meta.Project, repository, true, tickets.Root{Body: text})
		},
	}, nil
}
