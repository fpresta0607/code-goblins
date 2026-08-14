// Command cfo is the Chief Fuckaround Officer's tool belt: the compiled,
// Windows-native replacement for upstream First Mate's bash script layer.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/fpresta0607/code-goblins/internal/digest"
	"github.com/fpresta0607/code-goblins/internal/doctor"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/treehouse"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

// version is stamped by the release build:
//
//	go build -ldflags "-X main.version=v1.2.3" ./cmd/cfo
var version = "dev"

const usage = `usage: cfo <command> [args]

commands:
  version   print the cfo version
  doctor    check the tools cfo needs (git, gh, claude, herdr, treehouse, codex, pi)
  drain     print or acknowledge the wake queue and recovery episode
  watch     run one triage cycle by hand (manual diagnostics; the hooks are the production entry)
  session-start  print the full session-start digest by hand (manual diagnostics; the SessionStart hook is the production entry)
  cfo spawn <id> --project <path> --brief <path> --harness <claude|codex|pi> [--mode <no-mistakes|direct-PR|local-only>] [--model <model>] [--effort <level>] [--yolo]
  cfo send <target> [--key <key>] <text...>
  cfo peek <target> [lines]
  cfo fleet-view [--json]
  hook <name>  claude code hook entry points (session-start, pretool-arm, pretool-cd, pretool-subagent, turnend-guard, stop-autoarm)
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWithRuntime(args, stdout, stderr, defaultCommandRuntime())
}

// commandRuntime gives command tests the narrow seams they need without
// keeping process-wide mutable service state. Production constructs a fresh
// value for each invocation and resolves its home from the environment.
type commandRuntime struct {
	resolveHome func() (home.Home, error)
	spawn       func(context.Context, home.Home, spawn.Request) (spawn.Result, error)
	sendText    func(context.Context, home.Home, string, string) error
	sendKey     func(context.Context, home.Home, string, string) error
	peek        func(context.Context, home.Home, string, int) (string, error)
	snapshot    func(context.Context, home.Home) (fleet.Snapshot, error)
}

func defaultCommandRuntime() commandRuntime {
	return commandRuntime{
		resolveHome: home.Resolve,
		spawn: func(ctx context.Context, h home.Home, request spawn.Request) (spawn.Result, error) {
			commands := execx.OSRunner{}
			client := &herdr.Client{Commands: commands, Session: request.Session}
			service := spawn.Service{
				Herdr:     client,
				Treehouse: treehouse.Service{Commands: commands},
				Harness:   harness.DefaultRegistry(),
				StateDir:  h.State,
			}
			return service.Spawn(ctx, request)
		},
		sendText: func(ctx context.Context, h home.Home, target, text string) error {
			client := &herdr.Client{Commands: execx.OSRunner{}}
			return fleet.Sender{Resolve: fleet.Resolver{StateDir: h.State}, Herdr: client}.Text(ctx, target, text)
		},
		sendKey: func(ctx context.Context, h home.Home, target, key string) error {
			client := &herdr.Client{Commands: execx.OSRunner{}}
			return fleet.Sender{Resolve: fleet.Resolver{StateDir: h.State}, Herdr: client}.Key(ctx, target, key)
		},
		peek: func(ctx context.Context, h home.Home, target string, lines int) (string, error) {
			client := &herdr.Client{Commands: execx.OSRunner{}}
			return fleet.Peeker{Resolve: fleet.Resolver{StateDir: h.State}, Herdr: client}.Tail(ctx, target, lines)
		},
		snapshot: func(ctx context.Context, h home.Home) (fleet.Snapshot, error) {
			return fleet.BuildSnapshot(ctx, h, fleet.NewHerdrEndpoint(&herdr.Client{Commands: execx.OSRunner{}}))
		},
	}
}

func runWithRuntime(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "cfo %s\n", version)
		return 0
	case "doctor":
		checks := doctor.Run()
		for _, c := range checks {
			if c.Err != "" {
				fmt.Fprintf(stdout, "MISSING  %-10s %s (install: %s)\n", c.Name, c.Err, c.Hint)
			} else {
				fmt.Fprintf(stdout, "ok       %-10s %s\n", c.Name, c.Version)
			}
		}
		if !doctor.Healthy(checks) {
			return 1
		}
		return 0
	case "drain":
		h, err := home.Resolve()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return runDrain(h, args[1:], stdout, stderr)
	case "spawn":
		return runSpawn(args[1:], stdout, stderr, runtime)
	case "send":
		return runSend(args[1:], stdout, stderr, runtime)
	case "peek":
		return runPeek(args[1:], stdout, stderr, runtime)
	case "fleet-view":
		return runFleet(args[1:], stdout, stderr, runtime)
	case "session-start":
		// Deliberate deviation, recorded for the ledger: a home that cannot
		// be resolved errors out here (stderr plus exit 1), matching this
		// file's other manual commands (drain, watch) rather than exiting 0
		// with "SESSION START DEGRADED" digest text. This entry point is a
		// manual diagnostic, not the hook Claude Code drives, so a nonzero
		// exit here carries no session-blocking risk.
		h, err := home.Resolve()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err := digest.Compose(h, resolveSessionOwnerPID(), "", stdout); err != nil {
			fmt.Fprintf(stdout, "SESSION START DEGRADED: %s\n", err)
		}
		return 0
	case "watch":
		h, err := home.Resolve()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if !home.IsPrimary(h) {
			fmt.Fprintln(stderr, "cfo watch: not a primary home")
			return 1
		}
		reason, err := watch.Run(watch.ConfigFromEnv(h))
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if reason != "" {
			fmt.Fprintln(stdout, reason)
		}
		return 0
	case "hook":
		if len(args) < 2 {
			fmt.Fprint(stderr, usage)
			return 2
		}
		return runHook(args[1], os.Stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "cfo: unknown command %q\n%s", args[0], usage)
		return 2
	}
}
