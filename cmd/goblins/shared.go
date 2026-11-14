package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/services"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// sharedStaleAfter is how old the supervisor's record may be before goblins
// shared says it may be out of date: three of its one-minute checks.
const sharedStaleAfter = 3 * time.Minute

// sharedReport is goblins shared --json: the supervisor's record, how old it is,
// and whether that is older than its checks should leave it.
type sharedReport struct {
	supervisor.SharedServices
	AgeSeconds int  `json:"age_seconds"`
	IsStale    bool `json:"is_stale"`
}

// runShared prints the state of every service the fleet shares as the
// supervisor last checked it. It reads one file and asks no service
// anything, so a goblin's read adds no load to a busy one, and it is the same
// under every harness.
func runShared(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	flags := flag.NewFlagSet("shared", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	isJSON := flags.Bool("json", false, "print the record as JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: goblins shared [--json]")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	record, err := supervisor.ReadSharedServices(h.State)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(stderr, "goblins shared: the supervisor has not checked the shared services yet; ask the CEO whether it runs")
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, "goblins shared:", err)
		return 1
	}
	now := time.Now
	if runtime.now != nil {
		now = runtime.now
	}
	age := now().Sub(record.CheckedAt)
	report := sharedReport{SharedServices: record, AgeSeconds: int(age.Seconds()), IsStale: age > sharedStaleAfter}
	if *isJSON {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	fmt.Fprintf(stdout, "goblins shared: checked by the supervisor %s ago, at %s\n", age.Round(time.Second), record.CheckedAt.Format("15:04:05Z"))
	if report.IsStale {
		fmt.Fprintln(stdout, "That is older than its one-minute checks leave it, so this may be out of date: ask the CEO whether the supervisor runs.")
	}
	for _, service := range record.Services {
		fmt.Fprintf(stdout, "\n%s: %s\n  %s\n  next: %s - %s\n", service.Name, service.State, service.Detail, service.Next, service.NextDetail)
		if !service.RetryAt.IsZero() {
			fmt.Fprintf(stdout, "  checked again at %s\n", service.RetryAt.Format("15:04:05Z"))
		}
		if len(service.Holders) > 0 {
			fmt.Fprintf(stdout, "  held by: %s\n", strings.Join(service.Holders, ", "))
		}
		if len(service.Queued) > 0 {
			fmt.Fprintf(stdout, "  in its line: %s\n", strings.Join(service.Queued, ", "))
		}
		if len(service.Waiting) > 0 {
			fmt.Fprintf(stdout, "  goblins waiting on it: %s\n", strings.Join(service.Waiting, ", "))
		}
	}
	return 0
}

// sharedProbes are the machine's reads of the shared services for the
// supervisor's check: the gate daemon by its pid file, process and one health
// question, the heavy line and memory, whether the Docker engine answers,
// each through commands, and the Scrawl server's process. An example home shows no machine's shared services,
// so it has none.
func sharedProbes(isExample bool, commands execx.Runner) *supervisor.SharedProbes {
	if isExample {
		return nil
	}
	return &supervisor.SharedProbes{
		Daemon: func(ctx context.Context) (pipeline.Daemon, error) {
			root, err := pipeline.DefaultRoot()
			if err != nil {
				return pipeline.Daemon{State: pipeline.DaemonUnknown}, err
			}
			return pipeline.Reader{Root: root, Commands: gatedb.Runner{Next: commands}}.Daemon(ctx)
		},
		DaemonAnswers: func(ctx context.Context) bool {
			root, err := pipeline.DefaultRoot()
			if err != nil {
				return false
			}
			// daemon status only reads, and says "daemon not running" with a
			// clean exit when the daemon gave no answer, so its words count.
			result, err := commands.Run(ctx, execx.Request{Env: pipeline.NativeEnv(root), Name: "no-mistakes", Args: []string{"daemon", "status"}})
			return err == nil && result.ExitCode == 0 && strings.Contains(string(result.Stdout), "daemon running")
		},
		Line:   supervisor.MachineSharedLine,
		Docker: services.CLI{Commands: commands}.EngineRunning,
		Scrawl: supervisor.MachineScrawl,
	}
}
