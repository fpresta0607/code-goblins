package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/fpresta0607/code-goblins/internal/cirunner"
	"github.com/fpresta0607/code-goblins/internal/execx"
)

const ciRunnerUsage = "usage: cfo ci-runner job | status | parity --repo <owner/name>"

// ciRunnerJobLimit bounds a job from the fleet's side, past the service's own
// 30 minute limit and its cleanup, so a service that never answers cannot
// keep the turn the job runs in.
const ciRunnerJobLimit = 45 * time.Minute

// ciRunnerStatusLimit bounds asking the service what runs.
const ciRunnerStatusLimit = 10 * time.Second

// exitNoCIJob is cfo ci-runner job's exit when no job came for the runner.
const exitNoCIJob = 3

// runCIRunner runs cfo ci-runner: job runs one job in this machine's desktop
// CI runner for as long as this process lives, status says what runs, and
// parity keeps a repository's parity record (ci_runner_parity.go).
func runCIRunner(args []string, stdout, stderr io.Writer, commands execx.Runner, runtime commandRuntime) int {
	if len(args) > 0 && args[0] == "parity" {
		return runCIRunnerParity(args[1:], stdout, stderr, commands, runtime)
	}
	if len(args) != 1 || args[0] != "job" && args[0] != "status" {
		fmt.Fprintln(stderr, ciRunnerUsage)
		return 2
	}
	if args[0] == "status" {
		ctx, cancel := context.WithTimeout(context.Background(), ciRunnerStatusLimit)
		defer cancel()
		err := runtime.askCIRunner(ctx, cirunner.Request{Kind: cirunner.AskStatus}, func(reply cirunner.Reply) {
			fmt.Fprintf(stdout, "cfo ci-runner: %s\n", reply.Line)
		})
		if err != nil {
			fmt.Fprintf(stderr, "cfo ci-runner: %v\n", err)
			return 1
		}
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), ciRunnerJobLimit)
	defer cancel()
	var done cirunner.Reply
	err := runtime.askCIRunner(ctx, cirunner.Request{Kind: cirunner.AskRun}, func(reply cirunner.Reply) {
		switch reply.Kind {
		case cirunner.ReplyProgress:
			fmt.Fprintf(stderr, "cfo ci-runner: %s\n", reply.Line)
		case cirunner.ReplyDone:
			done = reply
		}
	})
	if err != nil {
		fmt.Fprintf(stderr, "cfo ci-runner: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "cfo ci-runner: the job ended: %s\n", done.Outcome)
	if done.Error != "" {
		fmt.Fprintf(stderr, "cfo ci-runner: %s\n", done.Error)
	}
	switch done.Outcome {
	case cirunner.Ran:
		return 0
	case cirunner.NoJob:
		return exitNoCIJob
	default:
		return 1
	}
}
