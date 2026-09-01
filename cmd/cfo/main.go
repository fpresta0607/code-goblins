// Command cfo is the Chief Fuckaround Officer's tool belt: the compiled,
// Windows-native replacement for upstream First Mate's bash script layer.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/digest"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/telemetry"
	"github.com/fpresta0607/code-goblins/internal/watch"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// version is stamped by the release build:
//
//	go build -ldflags "-X main.version=v1.2.3" ./cmd/cfo
var version = "dev"

const usage = `usage: cfo <command> [args]

commands:
  version   print the cfo version
  install   wire this checkout into the machine (CFO_HOME, PATH, and the Claude Code hooks in your user settings) so a session in any repo is supervised; --uninstall reverses it
  doctor    check the tools cfo needs (git, gh, claude, herdr, codex, pi, kimi, tasks-axi, quota-axi, no-mistakes, gh-axi, chrome-devtools-axi)
  drain     print or acknowledge the wake queue and recovery episode
  watch     run one triage cycle by hand (manual diagnostics; the hooks are the production entry)
  session-start  print the full session-start digest by hand (manual diagnostics; the SessionStart hook is the production entry)
  cfo auth <project> [--check|--fix] [--env]   preflight a project's services; --fix repairs what needs no human
  cfo auth store [--project <p>] <NAME> [value]   store one credential in a project's scope, or the shared scope without --project (omit the value to read it from stdin)
  cfo auth list [--project <p>]        list stored credential keys, never values
  cfo auth copy <NAME> --to <project> [--from <project>]   copy a stored value into a project's scope; the source is left in place
  cfo auth refresh <task-id>        regenerate a task's auth.ps1 from its project scope; storing or copying into a project scope does this for every live task of that project automatically
  cfo spawn <id> --project <path> --brief <path> --harness <claude|codex|pi|kimi> [--mode <no-mistakes|direct-PR|local-only>] [--model <model>] [--effort <level>] [--yolo]
  cfo switch <id> [--harness <h>] [--model <m>] [--effort <e>] [--force-dirty]   change a running goblin's harness/model/effort in place
  cfo send <target> [--key <key>] [--no-auto-submit] <text...>
  cfo peek <target> [lines]
  cfo fleet-view [--json]
  cfo brief <id> --project <path> [--kind <ship|scout>] [--mode <no-mistakes|direct-PR|local-only>]
  cfo pr check <id> <url>
  cfo pr merge <url> [--method <merge|squash|rebase>] [--delete-branch]
  cfo merge-local <id>
  cfo cleanup <id>
  cfo notify <id> --done --pr <url> | --blocked "<question>" | --failed "<reason>"   a goblin reports its outcome straight into the wake queue
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
	resolveHome   func() (home.Home, error)
	spawn         func(context.Context, home.Home, spawn.Request) (spawn.Result, error)
	switchTask    func(context.Context, home.Home, spawn.SwitchRequest) (spawn.SwitchResult, error)
	sendText      func(context.Context, home.Home, string, string, bool) error
	sendKey       func(context.Context, home.Home, string, string) error
	authRefresher func(home.Home) spawn.AuthRefresher
	peek          func(context.Context, home.Home, string, int) (string, error)
	snapshot      func(context.Context, home.Home) (fleet.Snapshot, error)
	cleanup       func(context.Context, home.Home, string, bool) (string, error)
	speedHint     func(context.Context, string) string
}

func defaultCommandRuntime() commandRuntime {
	return commandRuntime{
		resolveHome: home.Resolve,
		spawn: func(ctx context.Context, h home.Home, request spawn.Request) (spawn.Result, error) {
			commands := execx.OSRunner{}
			client := &herdr.Client{Commands: commands, Session: request.Session}
			service := spawn.Service{
				Herdr:     client,
				Worktrees: worktree.Service{Commands: commands, DataDir: h.Data},
				Harness:   harness.DefaultRegistry(),
				Auth:      auth.SpawnPreflight{DataDir: h.Data, Home: h.Root, Runner: commands},
				Commands:  commands,
				StateDir:  h.State,
			}
			return service.Spawn(ctx, request)
		},
		switchTask: func(ctx context.Context, h home.Home, request spawn.SwitchRequest) (spawn.SwitchResult, error) {
			commands := execx.OSRunner{}
			client := &herdr.Client{Commands: commands, Session: request.Session}
			service := spawn.Service{
				Herdr:     client,
				Worktrees: worktree.Service{Commands: commands, DataDir: h.Data},
				Harness:   harness.DefaultRegistry(),
				Auth:      auth.SpawnPreflight{DataDir: h.Data, Home: h.Root, Runner: commands},
				Commands:  commands,
				StateDir:  h.State,
			}
			return service.Switch(ctx, request)
		},
		sendText: func(ctx context.Context, h home.Home, target, text string, autoSubmit bool) error {
			client := &herdr.Client{Commands: execx.OSRunner{}}
			return fleet.Sender{Resolve: fleet.Resolver{StateDir: h.State}, Herdr: client, AutoSubmit: autoSubmit}.Text(ctx, target, text)
		},
		sendKey: func(ctx context.Context, h home.Home, target, key string) error {
			client := &herdr.Client{Commands: execx.OSRunner{}}
			return fleet.Sender{Resolve: fleet.Resolver{StateDir: h.State}, Herdr: client}.Key(ctx, target, key)
		},
		authRefresher: func(h home.Home) spawn.AuthRefresher {
			return spawn.AuthRefresher{
				StateDir: h.State,
				DataDir:  h.Data,
				Panes:    spawn.HerdrLiveness{Client: &herdr.Client{Commands: execx.OSRunner{}}},
			}
		},
		peek: func(ctx context.Context, h home.Home, target string, lines int) (string, error) {
			client := &herdr.Client{Commands: execx.OSRunner{}}
			return fleet.Peeker{Resolve: fleet.Resolver{StateDir: h.State}, Herdr: client}.Tail(ctx, target, lines)
		},
		snapshot: func(ctx context.Context, h home.Home) (fleet.Snapshot, error) {
			return fleet.BuildSnapshot(ctx, h, fleet.NewHerdrEndpoint(&herdr.Client{Commands: execx.OSRunner{}}))
		},
		cleanup: defaultCleanup,
		speedHint: func(ctx context.Context, name string) string {
			return telemetry.SpeedHint(ctx, execx.OSRunner{}, name)
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
	case "install":
		return runInstall(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(stdout)
	case "drain":
		h, err := home.Resolve()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return runDrain(h, args[1:], stdout, stderr)
	case "auth":
		return runAuth(args[1:], stdout, stderr, runtime)
	case "spawn":
		return runSpawn(args[1:], stdout, stderr, runtime)
	case "switch":
		return runSwitch(args[1:], stdout, stderr, runtime)
	case "send":
		return runSend(args[1:], stdout, stderr, runtime)
	case "peek":
		return runPeek(args[1:], stdout, stderr, runtime)
	case "fleet-view":
		return runFleet(args[1:], stdout, stderr, runtime)
	case "brief":
		return runBrief(args[1:], stdout, stderr)
	case "pr":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "cfo pr: check or merge subcommand is required")
			return 2
		}
		return runPR(args[1], args[2:], stdout, stderr)
	case "merge-local":
		return runMergeLocal(args[1:], stdout, stderr)
	case "cleanup":
		return runCleanup(args[1:], stdout, stderr, runtime)
	case "notify":
		return runNotify(args[1:], stdout, stderr)
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
