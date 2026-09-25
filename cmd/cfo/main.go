// Command cfo is the Chief Fuckaround Officer's tool belt: the compiled,
// Windows-native replacement for upstream First Mate's bash script layer.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/digest"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
	projectcfg "github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/runtime"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
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
  serve     run the persistent native supervisor and embedded browser board on loopback
  hooks     check|install <claude|codex|pi> native lifecycle hooks
  native-hook <harness>  bounded hook entry point (JSON on stdin)
  register  make this session the primary CFO the board delivers to; the SessionStart hooks do it, run it by hand when the board says the registration is stale
  install   wire a CFO home into the machine (CFO_HOME, PATH, and the Claude Code hooks in your user settings) so a session in any repo is supervised: run from a checkout it wires that checkout, run anywhere else it sets up %LOCALAPPDATA%\CodeGoblins from this binary (the CFO's contract and skills, the default policy, and the binary as cfo.exe and goblins.exe); --projects-root <dir> records the folder that holds your checkouts so --project can take a bare name; --uninstall reverses the wiring, the board's native hooks included, and keeps the home's files
  uninstall the same as install --uninstall
  doctor    check the tools cfo needs (git, gh, claude, herdr, codex, pi, kimi, tasks-axi, quota-axi, no-mistakes, gh-axi, chrome-devtools-axi)
  pipeline  config-drift | config-apply | migrate <id> | run <id> --intent <text> | respond <id> --action <fix|approve> [--findings <ids>] [--instructions <text>] | recover <id>
  drain     print or acknowledge the wake queue and recovery episode
  watch     run one triage cycle by hand (manual diagnostics; the hooks are the production entry)
  session-start  print the full session-start digest by hand (manual diagnostics; the SessionStart hook is the production entry)
  cfo auth <project> [--check|--fix] [--env]   preflight a project's services; --fix repairs what needs no human
  cfo auth store [--project <p>] <NAME> [value]   store one credential in a project's scope, or the shared scope without --project (omit the value to read it from stdin)
  cfo auth list [--project <p>]        list stored credential keys, never values
  cfo auth copy <NAME> --to <project> [--from <project>]   copy a stored value into a project's scope; the source is left in place
  cfo auth refresh <task-id>        regenerate a task's auth.ps1 from its project scope; storing or copying into a project scope does this for every live task of that project automatically
  cfo project show|check|init <project>
  cfo route [--project <project>] <brief>
  cfo verify <task-id> [--tier fast|full|deep]
  cfo security <task-id> [--deep]
  cfo hygiene <task-id>
  cfo deploy <task-id> [--target <name>]
  cfo evidence <task-id>
  cfo supersede <task-id> --reason <text>
  cfo spawn <id> --project <name|path> --brief <path> [--harness <claude|codex|pi|kimi>] [--mode <no-mistakes|direct-PR|local-only>] [--model <model>] [--effort <level>] [--class <ordinary|high-risk|mechanical>] [--yolo]   without --harness the lane table in data/routing.json picks harness, model and effort from the brief and the quota headroom
  cfo switch <id> [--harness <h>] [--model <m>] [--effort <e>] [--force-dirty]   change a running goblin's harness/model/effort in place
  cfo send <target> [--key <key>] <text...>
  cfo peek <target> [lines]
  cfo fleet-view [--json]
  cfo runtime [--json]   what is running on this machine and who owns it: containers by owner, listening dev servers and whether each is safe to stop, machine headroom, each project's deploy target, and how to run each project locally
  cfo brief <id> --project <name|path> [--kind <ship|scout>] [--mode <no-mistakes|direct-PR|local-only>]
  cfo pr check <id> <url>
  cfo pr merge <url> [--method <merge|squash|rebase>] [--delete-branch]
  cfo merge-local <id>
  cfo cleanup <id>
  cfo reap [--dry-run] [--apply] [--force <pid|task-id>]... [--json]   find orphaned harness processes, stale dev servers, worktrees, task records and status logs; --apply retires the worktrees, records and logs, and ending a process needs its pid named with --force
  cfo notify <id> --done --pr <url> | --blocked "<question>" | --failed "<reason>" | --working "<what>" | --waiting-on <task-id|overlord|ci|deploy> "<why>"   a goblin reports its outcome straight into the wake queue, or what it is working on or waiting on
  cfo question --id <stable-id> --text "<user question>" [--option "<choice>"]... [--recommend "<exact-choice>"]   registered CFO opens a user decision modal with Other; the answer returns as one normal native message, not a native prompt-tool response
  cfo answer <question-id|wake-seq> --option <choice> [--note "<text>"]   registered CFO answers a goblin's blocked question: delivered like cfo send, the notify retired, and the choice, who and when recorded for the board
  cfo review --id <stable-id> --title "<what to look at>" [--task <id>] [--image <path>]... [--lavish <url|html-file>] | --id <stable-id> --withdraw "<reason>" [--task <id>]   report an item that stays in the Command Center until the Overlord answers or clears it, or withdraw your own; a Lavish page named by its HTML file is polled by the supervisor, so the Overlord's feedback on it reaches the CFO as a review wake
  cfo run-request --id <stable-id> --title "<why>" --shell powershell|pwsh|bash [--admin] [--cwd <dir>] --command-file <path>   registered CFO asks the Overlord to run a command with one click in the Command Center; the file is read once and runs as a script file, and the output and exit code come back as his answer
  cfo present --id <stable-id> --kind browser|review --url <safe-url> [--task <id> [--generation <spawn-gen>]] [--state active|ended] [--ttl 5m]   report a successful presentation without opening a browser or waiting; omit task only from verified primary CFO context
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
	sendText      func(context.Context, home.Home, string, string) error
	sendKey       func(context.Context, home.Home, string, string) error
	authRefresher func(home.Home) spawn.AuthRefresher
	peek          func(context.Context, home.Home, string, int) (string, error)
	snapshot      func(context.Context, home.Home) (fleet.Snapshot, error)
	localRuntime  func(context.Context, home.Home) (runtime.Inventory, error)
	cleanup       func(context.Context, home.Home, string, bool) (string, error)
	reap          func(context.Context, home.Home, reap.Options) (reap.Result, error)
	speedHint     func(context.Context, string) string
	quota         func(context.Context) (quota.Report, string)
	// projectsRoot reads the machine's projects root. A runtime without one
	// (a test's) has no root, so a bare project name stays what it was before
	// names resolved: refused where a checkout is needed, a literal scope
	// where a credential scope is.
	projectsRoot func() (string, error)
}

// resolveProject turns a --project argument into a checkout directory: a path
// as written, a bare name looked up under the projects root.
func (r commandRuntime) resolveProject(arg string) (string, error) {
	return projectcfg.Resolve(arg, r.projectsRoot)
}

func defaultCommandRuntime() commandRuntime {
	return commandRuntime{
		resolveHome: home.Resolve,
		spawn: func(ctx context.Context, h home.Home, request spawn.Request) (spawn.Result, error) {
			commands := execx.OSRunner{}
			client := &herdr.Client{Commands: commands, Session: request.Session}
			service := spawn.Service{
				Herdr:      client,
				Worktrees:  worktree.Service{Commands: commands, DataDir: h.Data},
				Harness:    harness.DefaultRegistry(),
				Auth:       auth.SpawnPreflight{DataDir: h.Data, Home: h.Root, Runner: commands},
				Commands:   commands,
				StateDir:   h.State,
				PolicyPath: filepath.Join(h.Root, "config", "pipeline.json"),
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
		sendText: func(ctx context.Context, h home.Home, target, text string) error {
			client := &herdr.Client{Commands: execx.OSRunner{}}
			receipt := supervisor.PrepareSendActivity(ctx, h, client, target)
			if err := (fleet.Sender{Resolve: fleet.Resolver{StateDir: h.State}, Herdr: client}).Text(ctx, target, text); err != nil {
				return err
			}
			if err := receipt(); err != nil {
				fmt.Fprintln(os.Stderr, "Message accepted; board activity receipt unavailable. Do not resend for this notice.")
			}
			return nil
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
		localRuntime: func(ctx context.Context, h home.Home) (runtime.Inventory, error) {
			commands := execx.OSRunner{}
			return runtime.Collector{
				Home:   h,
				Docker: runtime.Docker{Commands: commands},
				System: runtime.System{Commands: commands},
			}.Collect(ctx)
		},
		cleanup: defaultCleanup,
		reap:    defaultReap,
		speedHint: func(ctx context.Context, name string) string {
			return telemetry.SpeedHint(ctx, execx.OSRunner{}, name)
		},
		quota:        quota.Reader{Commands: execx.OSRunner{}}.Read,
		projectsRoot: install.MachineProjectsRoot,
	}
}

func runWithRuntime(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:], stdout, stderr, runtime)
	case "native-hook":
		return runNativeHook(args[1:], os.Stdin, stdout, stderr, runtime)
	case "hooks":
		return runNativeSetup(args[1:], stdout, stderr, runtime)
	case "version":
		fmt.Fprintf(stdout, "cfo %s\n", version)
		return 0
	case "install":
		return runInstall(args[1:], stdout, stderr)
	case "uninstall":
		return runInstall(append([]string{"--uninstall"}, args[1:]...), stdout, stderr)
	case "doctor":
		return runDoctor(stdout, runtime)
	case "pipeline":
		return runPipeline(args[1:], stdout, stderr, runtime)
	case "drain":
		h, err := home.Resolve()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return runDrain(h, args[1:], stdout, stderr)
	case "auth":
		return runAuth(args[1:], stdout, stderr, runtime)
	case "project":
		return runProject(args[1:], stdout, stderr, runtime)
	case "route":
		return runRoute(args[1:], stdout, stderr, runtime)
	case "verify":
		return runVerify(args[1:], stdout, stderr, runtime)
	case "security":
		return runSecurity(args[1:], stdout, stderr, runtime)
	case "hygiene":
		return runHygiene(args[1:], stdout, stderr, runtime)
	case "deploy":
		return runDeploy(args[1:], stdout, stderr, runtime)
	case "evidence":
		return runEvidence(args[1:], stdout, stderr, runtime)
	case "supersede":
		return runSupersede(args[1:], stdout, stderr, runtime)
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
	case "runtime":
		return runRuntime(args[1:], stdout, stderr, runtime)
	case "brief":
		return runBrief(args[1:], stdout, stderr, runtime)
	case "pr":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "cfo pr: check or merge subcommand is required")
			return 2
		}
		return runPR(args[1], args[2:], stdout, stderr, execx.OSRunner{})
	case "merge-local":
		return runMergeLocal(args[1:], stdout, stderr)
	case "cleanup":
		return runCleanup(args[1:], stdout, stderr, runtime)
	case "reap":
		return runReap(args[1:], stdout, stderr, runtime)
	case "notify":
		return runNotify(args[1:], stdout, stderr)
	case "question":
		return runQuestion(args[1:], stdout, stderr, runtime)
	case "answer":
		return runAnswer(args[1:], stdout, stderr, runtime)
	case "register":
		return runRegister(args[1:], stdout, stderr, runtime)
	case "present":
		return runPresent(args[1:], stdout, stderr, runtime)
	case "review":
		return runReview(args[1:], stdout, stderr, runtime)
	case "run-request":
		return runRunRequest(args[1:], stdout, stderr, runtime)
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
