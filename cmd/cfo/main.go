// Command cfo is the Chief Fuckaround Officer's tool belt: the compiled,
// Windows-native replacement for upstream First Mate's bash script layer.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/digest"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/gatetest"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/onboarding"
	projectcfg "github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/runtime"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/telemetry"
	"github.com/fpresta0607/code-goblins/internal/terminal"
	"github.com/fpresta0607/code-goblins/internal/tickets"
	"github.com/fpresta0607/code-goblins/internal/watch"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// version is stamped by the release build:
//
//	go build -ldflags "-X main.version=v1.2.3" ./cmd/cfo
var version = "dev"

const usage = `usage: cfo <command> [args]

Run as goblins with no command, from any folder, it is the quick start: one Enter at a time it checks which of Claude Code, Codex and pi this machine has and is signed in to, offers to install or sign in to the one you choose, finds the supervisor or starts one in the background, and starts the CFO in the Code Goblins home when none runs: in Herdr, or in a native terminal shown here with goblins --native, and a Codex or pi CFO always in a native terminal, with or without --native, since only there is it woken. It ends on one screen with the board's link: Enter shows the CFO's terminal here, and B or Ctrl+click on the link opens the board. Later runs skip what is already set up, and a running CFO keeps its terminal and its harness. goblins setup shows the choice of agent again. goblins --harness claude|codex|pi names the agent instead of asking, remembered for later starts. goblins --board finds or starts the supervisor the same way and opens the board in the browser every time, without starting or showing a CFO in this terminal. Where the desktop window, goblins-window.exe, sits beside goblins, B opens the board in it instead of the browser; goblins --window shows the window without starting or showing a CFO here, and goblins --window --background, which Windows runs at login, keeps it in the tray.

commands:
  version   print the cfo version
  serve     run the persistent native supervisor and embedded browser board on loopback
  host      run one goblin terminal in a process of its own; cfo starts it, not you
  attach    show a native terminal in this console, the CFO's unless one is named; --state <dir> names the fleet's state folder; Ctrl-] leaves it running
  status    whether the supervisor runs: its board, what the fleet is doing and its pid; exits 1 when none runs
  stop      ask the supervisor to stop and wait until it has; --force ends its process tree instead
  setup     as goblins setup: run the quick start again and choose the agent the CFO starts as
  update    run by a verified candidate build: install it as this home's cfo.exe and goblins.exe, restart only the supervisor on it, and put the previous build back and restart that instead if anything fails; --recover finishes an update that stopped part way by putting the previous build back
  hooks     check|install <claude|codex|pi> native lifecycle hooks
  native-hook <harness>  bounded hook entry point (JSON on stdin)
  register  make this session the primary CFO the board delivers to; the SessionStart hooks do it, run it by hand when the board says the registration is stale
  install   wire a CFO home into the machine (CFO_HOME, PATH, and the Claude Code hooks in your user settings) so a session in any repo is supervised: run from a checkout it wires that checkout, run anywhere else it sets up %LOCALAPPDATA%\CodeGoblins from this binary (the CFO's contract and skills, the default policy, and the binary as cfo.exe and goblins.exe); --projects-root <dir> records the folder that holds your checkouts so --project can take a bare name; --uninstall reverses the wiring, the board's native hooks and the Start-menu shortcut included, and keeps the home's files
  uninstall the same as install --uninstall
  home      migrate [--apply --plan <digest>] [--memory-from <dir>]: lay out a home whose data predates the layout; without --apply a dry run that lists every file it would move, create or change, proves none is dropped and prints the plan digest --apply --plan makes
  doctor    check the tools cfo needs (git, gh, claude, herdr, codex, pi, kimi, tasks-axi, quota-axi, no-mistakes, gh-axi, chrome-devtools-axi)
  pipeline  config-drift | config-apply | migrate <id> | run <id> --intent <text> | respond <id> --action <fix|approve> [--findings <ids>] [--instructions <text>] | recover <id>
  drain     print or acknowledge the wake queue and recovery episode
  watch     run one triage cycle by hand (manual diagnostics; the hooks are the production entry)
  session-start  print the full session-start digest by hand (manual diagnostics; the SessionStart hook is the production entry)
  cfo auth <project> [--check|--fix] [--env]   preflight a project's services; --fix repairs what needs no human
  cfo auth store [--project <p>] <NAME> [value]   store one credential in a project's scope, or the shared scope without --project (omit the value to read it from stdin, hidden when typed at a console)
  cfo auth request --project <p> [--task <id>] --why "<text>" [--link <url>] [--env-file <file>] NAME [NAME...]   ask the Overlord for credential values by name; he pastes them on the board
  cfo auth list [--project <p>]        list stored credential keys, never values
  cfo auth copy <NAME> --to <project> [--from <project>]   copy a stored value into a project's scope; the source is left in place
  cfo auth refresh <task-id>        regenerate a task's auth.ps1 from its project scope; storing or copying into a project scope does this for every live task of that project automatically
  cfo project show|check|init <project>
  cfo route [--project <project>] <brief>
  cfo verify <task-id> [--tier fast|full|deep]
  cfo security <task-id> [--deep]
  cfo hygiene <task-id>
  cfo gate tests-kept   run from a no-mistakes repository gate: exits 1 when the gate's own fix commits deleted or skipped a test, so the run parks for an ask-user decision
  cfo gate test [--level fast|affected|full] [--plan]   this repository's gate test step: go vet and go test on the packages the branch changed and their direct importers, without the fleet's home; CI runs every package; --level fast leaves the slow packages' tests and the importers' to affected, full tests every package, --plan prints the plan and runs nothing; each run leaves a report and prints its path; above fast its tests wait for the run's turn on the machine, one run at a time
  cfo gate turns        show which cfo gate test runs hold the machine's turns, for how long and under what budget, and which wait
  cfo deploy <task-id> [--target <name>]
  cfo evidence <task-id>
  cfo supersede <task-id> --reason <text>
  cfo spawn <id> --project <name|path> --brief <path> [--harness <claude|codex|pi|kimi>] [--mode <no-mistakes|direct-PR|local-only>] [--model <model>] [--effort <level>] [--class <ordinary|high-risk|mechanical>] [--title "<short title>"] [--overlap-ok "<why>"] [--yolo]   starts the goblin in a native terminal of its own; without --harness the lane table in data/routing.json picks harness, model and effort from the brief and the quota headroom; without --title the task takes its backlog row's title, and with neither it is named by its id
  cfo title <id> "<short title>"   give a running task its short title: the board shows it, and the supervisor writes it to the ticket it opened for the task
  cfo switch <id> [--harness <h>] [--model <m>] [--effort <e>] [--force-dirty]   change a running goblin's harness/model/effort in place
  cfo send <target> [--key <key>] <text...>
  cfo peek <target> [lines]
  cfo fleet-view [--json]
  cfo runtime [--json]   what is running on this machine and who owns it: containers by owner, listening dev servers and whether each is safe to stop, machine headroom, each project's deploy target, and how to run each project locally
  cfo tickets <project> [--brief <file>] [--files <paths>] [--json]   read-only report of what others have in flight in the project's GitHub repository: whether it is collaborative, its active contributors, open issues, open and draft PRs with their files, and branches others pushed in the last 14 days; with --brief or --files it names the PRs, branches and issues that overlap that area
  cfo tickets <project> --allow-public-tickets   let the supervisor keep each task's ticket in the project's repository although it is public, where every issue is public; asked once per repository
  cfo brief <id> --project <name|path> [--kind <ship|scout>] [--mode <no-mistakes|direct-PR|local-only>]
  cfo pr check <id> <url>
  cfo pr merge <url> [--method <merge|squash|rebase>] [--delete-branch] [--verified "<what verified it>"]   while AFK mode is on this is the CFO's own merge word: it needs --verified, a goblin's pull request whose head holds its base's tip, and no --delete-branch, and it is logged with its evidence before it merges
  cfo afk on [--asked "<his words>"] | off [--asked "<his words>"] | status | report | log --kind <merge|deploy|migration|install|answer|other> --what "<what>" --evidence "<evidence>" [--link <url>]   AFK mode, the Supreme Overlord's switch for running the fleet while he is away: on and off are his, made from a terminal of his own and refused in a goblin's; the registered CFO makes them only at his ask, with --asked and his words quoted exactly, which the switch and the report keep; off prints the report of the stretch; status says who turned it on, what was decided so far and what is held for him; log is the registered CFO recording a decision it made under the authority, with its evidence
  cfo merge-local <id>
  cfo cleanup <id>
  cfo pause <id> | resume <id> | kill <id>   pause, resume or stop a task while preserving its work
  cfo reap [--dry-run] [--apply] [--force <pid|task-id>]... [--json]   find orphaned harness processes, stale dev servers, worktrees, task records and status logs; --apply retires the worktrees, records and logs, and ending a process needs its pid named with --force
  cfo notify <id> --done --pr <url> | --blocked "<question>" | --failed "<reason>" | --working "<what>" | --waiting-on <task-id|overlord|ci|deploy|memory> "<why>"   a goblin reports its outcome straight into the wake queue, or what it is working on or waiting on
  cfo question --id <stable-id> --text "<user question>" [--option "<choice>"]... [--recommend "<exact-choice>"]   registered CFO opens a user decision modal with Other; the answer returns as one normal native message, not a native prompt-tool response
  cfo answer <question-id|wake-seq> --option <choice> [--note "<text>"]   registered CFO answers a goblin's blocked question: delivered like cfo send (queued behind a working goblin's turn counts as delivered), the notify retired, and the choice, who and when recorded for the board
  cfo answer <question-id> --option <choice> [--note "<text>"] --record-only [--in <where>]   registered CFO records on the board a choice already given another way, for a goblin's notify already acknowledged or answered, and sends nothing; --in names where the Overlord gave it, such as chat, which the CFO's own question needs, and the card reads as his answer there
  cfo review --id <stable-id> --title "<what to look at>" [--task <id>] [--image <path>]... [--lavish <url|html-file>] | --id <stable-id> --withdraw "<reason>" [--task <id>] | --clear <stable-id> --reason "<why>"   report an item that stays in the Command Center until the Overlord answers or clears it, or withdraw your own, or as the registered primary CFO clear any open item, audited; a Scrawl page named by its HTML file is polled by the supervisor, so the Overlord's feedback on it reaches the CFO as a review wake
  cfo deliver --id <stable-id> --title "<what it is>" --file <path> [--url <link>] [--task <id>]   hand the Overlord a document as a Command Center item with Open and Download; the file is copied, a goblin's from its own folders, and the item leaves the queue when he opens or downloads it
  cfo run-request --id <stable-id> --title "<why>" --shell powershell|pwsh|bash [--admin] [--cwd <dir>] --command-file <path>   registered CFO asks the Overlord to run a command with one click in the Command Center; the file is read once and runs as a script file, and the output and exit code come back as his answer
  cfo run-request --withdraw <id> --reason "<why>"   registered CFO takes a run item nobody ran off the Command Center, audited in state/runs.audit; Run on it is refused from then on, and a replacement is a new item under a new ID
  cfo present --id <stable-id> --kind browser|review --url <safe-url> [--task <id> [--generation <spawn-gen>]] [--state active|ended] [--ttl 5m]   report a successful presentation without opening a browser or waiting; omit task only from verified primary CFO context
  hook <name>  claude code hook entry points (session-start, pretool-bash, pretool-arm, pretool-cd, pretool-subagent, turnend-guard, stop-autoarm)
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
	taskLifecycle func(context.Context, home.Home, lifecycle.Request, string) (state.Lifecycle, error)
	reap          func(context.Context, home.Home, reap.Options) (reap.Result, error)
	speedHint     func(context.Context, string) string
	quota         func(context.Context) (quota.Report, string)
	// goblins is true when this binary runs under the name goblins, where no
	// command means the launcher; cfo alone keeps printing its usage for
	// scripts. startServe and openURL are the launcher's two effects on the
	// machine: a detached supervisor and a browser tab.
	goblins    bool
	startServe func(home.Home) (<-chan struct{}, error)
	openURL    func(string) error
	// openWindow shows the board in the desktop window, and returns
	// errNoWindow when none sits beside this binary.
	openWindow func(board, stateDir string, background bool) error
	// nativeCFO, liveCFO, focusCFO, startCFO and attachHerdr are how the
	// launcher finds a live registered CFO, in a native terminal or in Herdr,
	// and brings it to the front, starts the CFO in Herdr and hands the
	// terminal to herdr attached to a session.
	nativeCFO   func(string) (string, bool)
	liveCFO     func(string) (herdr.Endpoint, bool)
	focusCFO    func(context.Context, herdr.Endpoint) error
	startCFO    func(ctx context.Context, project, harness string) (bool, error)
	attachHerdr func(string) int
	// startNativeCFO and attachNative start the CFO in a native terminal and
	// show a native terminal in this console, for goblins --native and a CFO
	// registered in one.
	startNativeCFO func(h home.Home, project, harness string, args []string) error
	attachNative   func(stateDir, id string, stdout, stderr io.Writer) int
	// nativeTerminalRuns reports whether a native terminal's host answers,
	// so a CFO started in terminal cfo is shown before it registers, never
	// started twice.
	nativeTerminalRuns func(stateDir, id string) bool
	// settleCFO answers the known startup dialogs of a CFO just started in
	// native terminal cfo and returns what to tell the Overlord about them.
	settleCFO func(ctx context.Context, stateDir, harness string) []string
	// setupAgent runs the quick start's agent steps and returns the agent
	// the CFO starts as, and choose shows one of its screens and returns the
	// choice the person accepts.
	setupAgent func(ctx context.Context, stateDir, chosen string, rerun bool, list *onboarding.Checklist, stdout, stderr io.Writer) (string, error)
	choose     func(output io.Writer, step onboarding.Step) (int, error)
	// killTree ends a process and everything it started, for goblins stop
	// --force.
	killTree func(int) error
	// projectsRoot reads the machine's projects root. A runtime without one
	// (a test's) has no root, so a bare project name stays what it was before
	// names resolved: refused where a checkout is needed, a literal scope
	// where a credential scope is.
	projectsRoot func() (string, error)
	// repoActivity reads what GitHub says is happening in the repository a
	// checkout's origin names, for cfo tickets.
	repoActivity func(ctx context.Context, checkout string, now time.Time) (tickets.Activity, error)
	// overlapTimeout bounds the repoActivity read cfo spawn makes. Zero, in
	// every runtime but a test's, is the overlapTimeout constant.
	overlapTimeout time.Duration
	// repositoryOf names the GitHub repository a checkout's origin is, for
	// cfo tickets --allow-public-tickets.
	repositoryOf func(ctx context.Context, checkout string) (string, error)
	// switchAFK asks the supervisor to turn AFK mode on or off, and logAFK to
	// log a decision made under it; nil is the supervisor's pipe.
	switchAFK func(h home.Home, on bool, asked string) error
	logAFK    func(h home.Home, entry afk.Entry) error
	// availableMemory reads the memory a new process can have, in bytes, for
	// the turn cfo gate test takes before its tests, gateBudget is how long
	// the tests of a level may run, and gateRun runs one of the step's
	// commands in dir and returns its exit code, with an error unless it
	// passed.
	availableMemory func() (uint64, error)
	gateBudget      func(gatetest.Level) time.Duration
	gateRun         func(command []string, dir string, env []string, stdout, stderr io.Writer) (int, error)
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
			self, err := os.Executable()
			if err != nil {
				return spawn.Result{}, err
			}
			service := spawn.Service{
				Worktrees:   worktree.Service{Commands: commands, DataDir: h.Data},
				Harness:     harness.DefaultRegistry(),
				Auth:        auth.SpawnPreflight{DataDir: h.Data, Home: h.Root, Runner: commands},
				Commands:    commands,
				StateDir:    h.State,
				PolicyPath:  filepath.Join(h.Root, "config", "pipeline.json"),
				HostCommand: []string{self, "host"},
				PromptSince: nativePromptSince(h),
				Admit: func() error {
					memory, err := supervisor.MachineMemory()
					if err != nil {
						return err
					}
					return supervisor.CheckLaunch(h, memory)
				},
			}
			return service.Spawn(ctx, request)
		},
		switchTask: func(ctx context.Context, h home.Home, request spawn.SwitchRequest) (spawn.SwitchResult, error) {
			commands := execx.OSRunner{}
			self, err := os.Executable()
			if err != nil {
				return spawn.SwitchResult{}, err
			}
			service := spawn.Service{
				Worktrees:   worktree.Service{Commands: commands, DataDir: h.Data},
				Harness:     harness.DefaultRegistry(),
				Auth:        auth.SpawnPreflight{DataDir: h.Data, Home: h.Root, Runner: commands},
				Commands:    commands,
				StateDir:    h.State,
				HostCommand: []string{self, "host"},
				PromptSince: nativePromptSince(h),
			}
			return service.Switch(ctx, request)
		},
		sendText: func(ctx context.Context, h home.Home, target, text string) error {
			client := &herdr.Client{Commands: execx.OSRunner{}}
			receipt := supervisor.PrepareSendActivity(ctx, h, terminal.HerdrSessions(client), target)
			send := func() error {
				return fleet.Sender{Resolve: fleet.Resolver{StateDir: h.State}, Terminal: client}.Text(ctx, target, text)
			}
			if meta, native := fleet.NativeTask(h.State, target); native {
				send = func() error {
					return spawn.Service{StateDir: h.State, PromptSince: nativePromptSince(h)}.SendNative(ctx, meta, fleet.Stamp(text))
				}
			}
			if err := send(); err != nil {
				return err
			}
			if err := receipt(); err != nil {
				fmt.Fprintln(os.Stderr, "Message accepted; board activity receipt unavailable. Do not resend for this notice.")
			}
			return nil
		},
		sendKey: func(ctx context.Context, h home.Home, target, key string) error {
			if meta, native := fleet.NativeTask(h.State, target); native {
				return spawn.Service{StateDir: h.State}.SendNativeKey(meta, key)
			}
			client := &herdr.Client{Commands: execx.OSRunner{}}
			return fleet.Sender{Resolve: fleet.Resolver{StateDir: h.State}, Terminal: client}.Key(ctx, target, key)
		},
		authRefresher: func(h home.Home) spawn.AuthRefresher {
			return spawn.AuthRefresher{
				StateDir: h.State,
				DataDir:  h.Data,
				Panes:    spawn.NativeLiveness{StateDir: h.State},
			}
		},
		peek: peekTerminal,
		snapshot: func(ctx context.Context, h home.Home) (fleet.Snapshot, error) {
			return fleet.BuildSnapshot(ctx, h, fleet.NewTerminalEndpoint(h.State, &herdr.Client{Commands: execx.OSRunner{}}))
		},
		localRuntime: func(ctx context.Context, h home.Home) (runtime.Inventory, error) {
			commands := execx.OSRunner{}
			return runtime.Collector{
				Home:   h,
				Docker: runtime.Docker{Commands: commands},
				System: runtime.System{Commands: commands},
			}.Collect(ctx)
		},
		cleanup:       defaultCleanup,
		taskLifecycle: defaultTaskLifecycle,
		reap:          defaultReap,
		speedHint: func(ctx context.Context, name string) string {
			return telemetry.SpeedHint(ctx, execx.OSRunner{}, name)
		},
		quota:        quota.Reader{Commands: execx.OSRunner{}}.Read,
		projectsRoot: install.MachineProjectsRoot,
		goblins:      invokedAsGoblins(),
		startServe:   startDetachedServe,
		openURL:      openInBrowser,
		openWindow:   openWindow,
		nativeCFO:    supervisor.NativeCFO,
		liveCFO:      supervisor.LiveCFO,
		focusCFO:     focusCFOInHerdr,
		startCFO:     startCFOInHerdr,
		attachHerdr:  attachHerdr,
		killTree: func(pid int) error {
			return killTree(context.Background(), execx.OSRunner{}, pid)
		},
		startNativeCFO:     startNativeCFO,
		attachNative:       attachNative,
		nativeTerminalRuns: supervisor.NativeTerminalRuns,
		settleCFO:          settleNativeCFO,
		setupAgent:         setupAgent,
		choose:             onboarding.AskConsole,
		repoActivity:       readRepositoryActivity,
		repositoryOf:       tickets.GitHub{Commands: execx.OSRunner{}}.RepositoryOf,
		availableMemory: func() (uint64, error) {
			memory, err := supervisor.MachineMemory()
			return memory.Available, err
		},
		gateBudget: gateBudget,
		gateRun:    runGateCommand,
	}
}

// invokedAsGoblins reports whether this binary is the goblins.exe copy the
// install puts beside cfo.exe.
func invokedAsGoblins() bool {
	executable, err := os.Executable()
	return err == nil && strings.EqualFold(strings.TrimSuffix(filepath.Base(executable), filepath.Ext(executable)), "goblins")
}

func runWithRuntime(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if runtime.goblins && len(args) == 1 && args[0] == "--board" {
		return runBoardLauncher(stdout, stderr, runtime)
	}
	if runtime.goblins && len(args) > 0 && args[0] == "--window" && (len(args) == 1 || len(args) == 2 && args[1] == "--background") {
		return runWindowLauncher(stdout, stderr, runtime, len(args) == 2)
	}
	if runtime.goblins && len(args) > 0 && args[0] == "setup" {
		if len(args) != 1 {
			fmt.Fprintln(stderr, "usage: goblins setup")
			return 2
		}
		return runQuickstart(stdout, stderr, runtime, true, false, "")
	}
	if runtime.goblins && (len(args) == 0 || strings.HasPrefix(args[0], "-")) {
		fs := flag.NewFlagSet("goblins", flag.ContinueOnError)
		fs.SetOutput(stderr)
		native := fs.Bool("native", false, "start a new CFO in a native terminal shown here instead of in Herdr")
		harness := fs.String("harness", "", "the harness goblins starts the CFO as, remembered for later starts: claude, codex or pi")
		if err := fs.Parse(args); err != nil {
			return 2
		}
		if fs.NArg() != 0 {
			fmt.Fprintf(stderr, "goblins: unexpected argument %q; goblins takes only --native and --harness <claude|codex|pi>, or a command\n", fs.Arg(0))
			return 2
		}
		if *harness != "" && !slices.Contains(cfoHarnesses, *harness) {
			fmt.Fprintf(stderr, "goblins: --harness %q is not claude, codex or pi\n", *harness)
			return 2
		}
		return runQuickstart(stdout, stderr, runtime, false, *native, *harness)
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "attach":
		return runAttach(args[1:], stdout, stderr, runtime)
	case "status":
		return runStatus(args[1:], stdout, stderr, runtime)
	case "stop":
		return runStop(args[1:], stdout, stderr, runtime)
	case "serve":
		return runServe(args[1:], stdout, stderr, runtime)
	case "update":
		return runUpdate(args[1:], stdout, stderr, runtime)
	case "host":
		return runHost(args[1:], stderr)
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
	case "home":
		return runHome(args[1:], stdout, stderr)
	case "pipeline":
		return runPipeline(args[1:], stdout, stderr, runtime)
	case "drain":
		h, err := home.Resolve()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return runDrain(h, args[1:], stdout, stderr)
	case "afk":
		return runAFK(args[1:], stdout, stderr, runtime)
	case "auth":
		return runAuth(args[1:], stdout, stderr, runtime)
	case "connection-repair":
		return runConnectionRepair(args[1:], stdout, stderr)
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
	case "gate":
		return runGate(args[1:], stdout, stderr, runtime)
	case "deploy":
		return runDeploy(args[1:], stdout, stderr, runtime)
	case "evidence":
		return runEvidence(args[1:], stdout, stderr, runtime)
	case "supersede":
		return runSupersede(args[1:], stdout, stderr, runtime)
	case "spawn":
		return runSpawn(args[1:], stdout, stderr, runtime)
	case "title":
		return runTitle(args[1:], stdout, stderr, runtime)
	case "switch":
		return runSwitch(args[1:], stdout, stderr, runtime)
	case "pause", "resume":
		return runLifecycle(args[0], args[1:], stdout, stderr, runtime)
	case "kill":
		return runLifecycle("stop", args[1:], stdout, stderr, runtime)
	case "send":
		return runSend(args[1:], stdout, stderr, runtime)
	case "peek":
		return runPeek(args[1:], stdout, stderr, runtime)
	case "fleet-view":
		return runFleet(args[1:], stdout, stderr, runtime)
	case "runtime":
		return runRuntime(args[1:], stdout, stderr, runtime)
	case "tickets":
		return runTickets(args[1:], stdout, stderr, runtime)
	case "brief":
		return runBrief(args[1:], stdout, stderr, runtime)
	case "pr":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "cfo pr: check or merge subcommand is required")
			return 2
		}
		return runPR(args[1], args[2:], stdout, stderr, execx.OSRunner{}, runtime)
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
	case "deliver":
		return runDeliver(args[1:], stdout, stderr, runtime)
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
