// Command goblins is the Chief Executive Officer's tool belt: the compiled,
// Windows-native replacement for upstream First Mate's bash script layer.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/actionsbilling"
	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/cirunner"
	"github.com/fpresta0607/code-goblins/internal/cutover"
	"github.com/fpresta0607/code-goblins/internal/defender"
	"github.com/fpresta0607/code-goblins/internal/devdrive"
	"github.com/fpresta0607/code-goblins/internal/digest"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
	"github.com/fpresta0607/code-goblins/internal/gatehome"
	"github.com/fpresta0607/code-goblins/internal/gatetest"
	"github.com/fpresta0607/code-goblins/internal/githubapi"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/installbase"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/measure"
	"github.com/fpresta0607/code-goblins/internal/onboarding"
	"github.com/fpresta0607/code-goblins/internal/openrouter"
	projectcfg "github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/runtime"
	"github.com/fpresta0607/code-goblins/internal/services"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/telemetry"
	"github.com/fpresta0607/code-goblins/internal/tickets"
	"github.com/fpresta0607/code-goblins/internal/verify"
	"github.com/fpresta0607/code-goblins/internal/watch"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// version is stamped by the release build:
//
//	go build -ldflags "-X main.version=v1.2.3" ./cmd/goblins
var version = "dev"

const usage = `usage: goblins <command> [args]

Run as goblins with no command, from any folder, it is the quick start: one Enter at a time it checks which of Claude Code, Codex and pi this machine has and is signed in to, offers to install or sign in to the one you choose, finds the supervisor or starts one in the background, and starts the CEO in the Code Goblins home when none runs, in a native terminal of its own that outlives every window. It ends on one screen with the board's link: Enter shows the CEO's terminal here, and B or Ctrl+click on the link opens the board. Later runs skip what is already set up, and a running CEO keeps its terminal and its harness. goblins setup shows the choice of agent again. goblins --harness claude|codex|pi names the agent instead of asking, remembered for later starts. goblins --board finds or starts the supervisor the same way and opens the board in the browser every time, without starting or showing a CEO in this terminal. Where the desktop window, goblins-window.exe, sits beside goblins, B opens the board in it instead of the browser; goblins --window shows the window without starting or showing a CEO here, and goblins --window --background keeps it in the tray. goblins --window --locate, which the window runs when it is started alone, from the Start menu or at login, finds or starts the supervisor and prints the board's address and the state folder for the window to show.

commands:
  help      [<command> [<subcommand>] | contract <topic>]: read only the requested embedded command or procedure contract; with no topic, list available references
  memory    retrieve [--project <name>] [--task <id>] [--topic <topic>] [--search <words> --limit <n>] [--seen <file>] [--cold] | retrieve --id <id> | sync | scope --project <name> [--ids <file>] [<id>...] | scope --fleet [--ids <file>] [<id>...] | scope --expiry <id>="<words>"... | scope --list | recall "<question>" [--project <name>] [--repo <name>] [--history] [--limit <n>] | recall --eval <file> | lesson accept <id> [--incident] [--supersedes <fact-id>] | lesson reject <id> --reason "<why>" | routes | budget [--packages <file>] | migration-plan | retention-plan --manifest <file>; exact source-backed memory and read-only recovery planning
  version   print the goblins version
  serve     run the persistent native supervisor and embedded browser board on loopback
  remote    on|status|off: private phone access to the board and CEO terminal over your Tailscale account
  host      run one goblin terminal in a process of its own; goblins starts it, not you
  attach    show a native terminal in this console, the CEO's unless one is named; --state <dir> names the fleet's state folder; Ctrl-] leaves it running
  status    whether the supervisor runs: its board, what the fleet is doing and its pid; exits 1 when none runs
  stop      ask the supervisor to stop and wait until it has; --force ends its process tree instead
  setup     as goblins setup: run the quick start again and choose the agent the CEO starts as
  resume    with no task named, as goblins resume: restart a running CEO in its terminal on its conversation, or bring a closed one back, then bring back every goblin whose terminal ended. goblins resume --harness <claude|codex|pi> starts fresh when the harness changes and remembers the choice. Run outside the CEO's own terminal. goblins resume <id> resumes a paused task
  update    run as the home's own build: update the home from the newest published release, checked against its SHA256SUMS (--check only says how this build stands; --to <tag> refuses a newer one published since); run by a verified candidate build: install it as this home's bin\goblins.exe, restart only the supervisor on it, and put the previous build back and restart that instead if anything fails; bin keeps the current build alone, the previous one kept in state\update for the rollback; --recover finishes an update that stopped part way by putting the previous build back
  hooks     check|install <claude|codex|pi> native lifecycle hooks
  native-hook <harness>  bounded hook entry point (JSON on stdin)
  codex-guard   the codex a goblin's armed codex guard runs while Codex's included allowance is used up: says why in one line, wakes the CEO once and starts nothing; the guard runs it, not you
  register  make this session the primary CEO the board delivers to; the SessionStart hooks do it, run it by hand when the board says the registration is stale
  install   wire the CEO home into the machine (GOBLINS_HOME, PATH, and the Command Center allow rules in your Claude Code settings, out of which it takes the CEO's hooks an older build wrote, since the CEO's terminal starts with its own) so the CEO is supervised wherever it is started: from any folder, a checkout included, it sets up %LOCALAPPDATA%\CodeGoblins from this binary (the CEO's contract, the default policy, the binary as bin\goblins.exe, the skills once in ~\.agents\skills with a junction from Claude Code's skills folder, and state\harnesses.json naming where each harness keeps its configuration); --projects-root <dir> records the folder that holds your checkouts so --project can take a bare name; --uninstall reverses the wiring, the board's native hooks, the Start-menu and desktop shortcuts and the skills it installed included, and keeps the home's files
  uninstall the same as install --uninstall
  home      migrate [--apply --plan <digest>] [--memory-from <dir>]: lay out a home whose data predates the layout; without --apply a dry run that lists every file it would move, create or change, proves none is dropped and prints the plan digest --apply --plan makes; move [--to <dir>] [--apply --plan <digest>]: move an older build's home, such as a checkout, to the per-user home, with the same dry run, digest and read-back
  cutover   --dry-run | --plan <digest> | --undo <folder>: move an installed home, its harnesses' hooks and settings, its skills, the user's settings, scheduled jobs, credentials, ticket labels and live task files from the command's old name to goblins, and the role's to ceo, run as this build with the fleet paused; --dry-run lists every change and the plan's digest and changes nothing, --plan makes that plan after keeping everything its undo needs, --undo restores the home exactly; files in other checkouts that name the old command are listed, never changed
  dev-drive say whether the home's worktrees, scratch and package caches are on a Dev Drive, a drive Windows 11 formats for developer work that Defender scans in performance mode (not an exclusion), whether this machine has or can have one, and why not; setup: ask for the next step, which the board puts in the Command Center as one item to run where it needs a person and takes itself where it does not; move --to <folder>: put new goblins' worktrees, scratch and caches in <folder> on a trusted Dev Drive, keeping every started goblin's folders where they are
  defender  [--since <time|day|length>] [--json]: list what Microsoft Defender recorded under the fleet's folders since then, its detections and the samples it sent to Microsoft, each file with the task and test that made it; it only reads Defender's own records
  doctor    check the tools goblins needs (git, gh, claude, herdr, codex, pi, tasks-axi, quota-axi, no-mistakes, gh-axi, chrome-devtools-axi) and each harness's version beside its newest; --fix installs the newest Codex and pi once each starts to its composer in a terminal of its own, while nothing runs from its install
  pipeline  config-drift | config-apply | migrate <id> | run <id> [--branch <b>] --intent-file <file> [--closes <issue>]... | respond <id> [--branch <b> | --run <run>] --action <fix|approve> [--findings <ids>] [--instructions <text>] [--scope-decision <answered-notify-sequence>] | recover <id> [--branch <b> | --run <run>] | terminal-preflight <id> --run <predecessor> | validate-authored <id> [--branch <b>] --admission-file <file> | validate-authored <id> --run <run> --findings <ids> | validate-terminal <id> [--branch <b>] --admission-file <file>; validation admission requires the registered primary CEO, except a goblin's own correction of a handed-back failure
  drain     print or acknowledge the wake queue and recovery episode, and list the lessons waiting for the CEO's word
  watch     run one triage cycle by hand (manual diagnostics; the hooks are the production entry)
  session-start  print the full session-start digest by hand (manual diagnostics; the SessionStart hook is the production entry)
  goblins auth <project> [--check|--fix] [--env]   preflight a project's services; --fix repairs what needs no human
  goblins auth store [--project <p>] <NAME> [value]   store one credential in a project's scope, or the shared scope without --project (omit the value to read it from stdin, hidden when typed at a console)
  goblins auth request --project <p> [--task <id>] --why "<text>" [--link <url>] [--env-file <file>] NAME [NAME...]   ask the Overlord for credential values by name; he pastes them on the board
  goblins auth list [--project <p>]        list stored credential keys, never values
  goblins auth copy <NAME> --to <project> [--from <project>]   copy a stored value into a project's scope; the source is left in place
  goblins auth browser <provider> --task <id> [--release | --sign-in]   drive the project's headless provider browser. --release closes it. --sign-in asks for a sign-in through the board's local window button
  goblins auth refresh <task-id>        regenerate a task's auth.ps1 from its project scope; storing or copying into a project scope does this for every live task of that project automatically
  goblins project check <project> [--area <area>] [--json] [--draft <file>]   assess what the home knows about a project: its record, its gate, its configs, its connectors and its instructions, one line each with the evidence and the fix. It reads only and exits 1 when a line is worse than low. --draft writes the record it can vouch for to a new file outside the home's projects
  goblins project check --unfiled [--json]   name each checkout under the projects root that the home holds no folder for, and what a spawn there would be given
  goblins project show|init <project>   print a project's record, or write an empty one
  goblins route [--project <project>] <brief>
  goblins verify <task-id> [--tier fast|full|deep]
  goblins security <task-id> [--deep]
  goblins hygiene <task-id>
  goblins gate tests-kept   run from a no-mistakes repository gate: exits 1 when the gate's own fix commits deleted or skipped a test, so the run parks for an ask-user decision
  goblins gate test [--level changed|fast|affected|full] [--plan]   this repository's gate test step: go vet and go test on the packages the branch changed, the packages that import them and the packages config/verify.json names for a changed file their tests read, without the fleet's home; it lists the changed files no Go check reads, and a changed file the policy does not account for requires every package; CI runs every package; --level changed runs only the tests the change added or edited, by name, and leaves the rest to CI, the default for a project whose hosted CI is its test evidence, fast leaves the slow packages' tests and the importers' to affected, full tests every package, --plan prints the plan and runs nothing; each run leaves a report and prints its path; above fast its tests wait for the run's turn on the machine, one run at a time
  goblins gate prepush [--plan] [--limit <duration>]   what a goblin runs before it pushes: picks what the branch's change can break and runs it one check at a time, stopping with one plain line at the first failure: go vet of the packages the change reaches, the tests that read the whole tree, the changed packages and the packages that import them, nearest first, and for a change under frontend the type check, the lint, the unit tests and the browser specs the change touched or names. A slow package runs without the tests this machine timed at 2 seconds or longer, unless the change touched their file. It takes its turn on the machine, starts no check under the memory floor or after its time limit, 15 minutes unless --limit says otherwise, and names what it left to CI. --plan prints the pick and runs nothing
  goblins gate turn [--as <name>] [--wait <duration>] [--memory <size>] [--low-priority] [--light] [--detach <log-file>] -- <program> [<argument>...]   run any heavy command in the line goblins gate test and goblins gate prepush take their turns in, one run at a time on the machine whoever started it: it waits while another run holds the turn and while free memory or commit is under the line's floor, 4 GiB on a line of one slot, with no limit of its own unless --wait bounds the wait for the turn or a gate's agent started the run, which then waits three minutes at most, at the lowest priority behind every ordinary waiting run with --low-priority, says on standard error which run it waits for, and then runs the command with its output, its input and its exit code untouched; exit 125 means it took no turn and ran nothing. --memory is the most memory the run is expected to use, which a line of two slots weighs. --light takes a turn beside the slots once free memory covers its --memory, and ends it at its --execution-limit, which it needs both of. --detach starts the run in the background, hidden in a console with no window, appends its output to <log-file> with a last line that says how it ended, writes its pid to <log-file>.pid and returns at once: the one way a goblin starts a heavy run in the background
  goblins gate turns [--json]  show which goblins gate test, goblins gate prepush and goblins gate turn runs hold the machine's turns, for how long and under what budget, how far their tests are, and which wait, or with --json the same as one JSON object of fixed keys, for the board
  goblins ci-runner job     run one CI job in this machine's desktop CI runner, as the watcher starts it in a turn at the lowest priority. It says the job's progress on standard error and its outcome on standard output, exits 0 when the job ran, 3 when no job came for the runner and 1 otherwise, and ending it ends the job
  goblins ci-runner status  say whether this machine's desktop CI runner runs a job, and how its last one ended
  goblins ci-runner parity --repo <owner/name> [--workflow <file>.yml] [--runs <1 to 100>]   add to the repository's parity record each job its desktop mirror workflow (desktop-mirror.yml unless --workflow says otherwise) ran beside the hosted job it copies, from the mirror's newest 30 finished runs unless --runs says otherwise: both conclusions, pytest counts, the merge each tested, their times and the desktop turn's wait. It says for each job how many runs in a row matched, and that its hosted copy may retire, his decision, after 20
  goblins deploy <task-id> [--target <name>]
  goblins evidence <task-id>
  goblins supersede <task-id> --reason <text>
  goblins spawn <id> --project <name|path> --brief <path> [--harness <claude|codex|pi>] [--mode <no-mistakes|direct-PR|local-only>] [--delivery-exception "<authority and reason>"] [--model <model>] [--effort <level>] [--class <ordinary|high-risk|mechanical>] [--title "<short title>"] [--overlap-ok "<why>"] [--yolo]   starts the goblin in a native terminal of its own; without --harness the lane table in data/routing.json picks harness, model and effort from the brief and the quota headroom; without --title the task takes its backlog row's title, cut to 100 characters with no harness at its end, and with neither it is named by its id
  goblins title <id> "<short title>"   give a task its short title: the board shows it while the task runs, and the supervisor writes it to the ticket it opened for the task, open or closed; the one way to rename an open ticket
  goblins switch <id> [--harness <h>] [--model <m>] [--effort <e>] [--force-dirty] [--restart] [--terminal]   change a running goblin's harness/model/effort in place, restart it onto a harness update, or take it over in a terminal
  goblins send <target> [--key <key>] <text...>
  goblins peek <target> [lines]
  goblins fleet-view [--json]
  goblins runtime [--json]   what is running on this machine and who owns it: containers by owner, the local services stacks goblins holds for tasks, listening dev servers and whether each is safe to stop, machine headroom, each project's deploy target, and how to run each project locally
  goblins services up <project> --task <id> [--wait <duration>] | down <project> --task <id>   start the local services a project declares for a task's full-stack check, or share them with the tasks that hold them, only while memory and commit stay above the floor with their cost added, waiting up to --wait and saying why. down releases the hold, and the last release stops what goblins started and the engine when goblins started it
  goblins shared [--json]   read the gate daemon, the heavy-run line, the Docker engine and the Scrawl server as the supervisor last checked them: busy, never stopped, for a live daemon that missed a reply, whether to go, wait or ask, and who holds and waits on each; it only reads, under any harness, and starts, stops or restarts nothing
  goblins sandbox run -- <program> [<argument>...]   for a task whose brief carries sandbox: needed, run the command in a container of its project's devcontainer image with only its worktree and scratch folder mounted and its .git link read-only, in the machine's heavy line as goblins gate turn runs it, and remove the container once it ended; exit 125 means it ran nowhere, never on the host instead, and goblins help sandbox says why it does
  goblins tickets <project> [--brief <file>] [--files <paths>] [--json]   read-only report of what others have in flight in the project's GitHub repository: whether it is collaborative, its active contributors, open issues, open and draft PRs with their files, and branches others pushed in the last 14 days; with --brief or --files it names the PRs, branches and issues that overlap that area
  goblins tickets <project> --allow-public-tickets   let the supervisor keep each task's ticket in the project's repository although it is public, where every issue is public; asked once per repository
  goblins issue file --project <project> --title <title> [--body-file <file>] [--evidence <text>]... [--root <key>]   file a root issue found outside your brief on the project's repository with the ops-root label, or comment on the open issue of the same root. On a public repository it refuses text naming a private project, a teammate, a local path or a secret
  goblins brief <id> --project <name|path> [--kind <ship|scout>] [--mode <no-mistakes|direct-PR|local-only>] [--surface <internal|product|mixed|unknown>] [--risk <low|sensitive-surface>] [--reason "<assessment>"] [--delivery-exception "<authority and reason>"]
  goblins brief ready <id>   validate a completed queued brief and finalize its ready state, preserving its authored launch and auth choices
  goblins pr check <id> <url>
  goblins pr merge <url> [--method <merge|squash|rebase>] [--delete-branch] [--verified "<what verified it>"]   where the base requires a merge queue it adds the pull request to the queue instead, never with --admin; while AFK mode is on this is the CEO's own merge word: it needs --verified, a goblin's pull request whose head holds its base's tip unless a merge queue tests the merge, and no --delete-branch, and it is logged with its evidence before it merges
  goblins pr train <project>   a merge train: merge every green pull request goblins finished onto main in queue order on a train branch, open a train PR, let CI test them together once, then merge the train PR, so main takes the commit CI tested and GitHub marks each PR that rode merged, and check main's tree equals the train's; a red run has its failed checks run again once and, red a second time, is halved until the PR that breaks it is found, every half that passes lands, and its goblin gets the failure; held (label hold), draft and conflicting PRs never ride; joins the train already running there, and waits until it is over
  goblins afk on [--asked "<his words>"] | off [--asked "<his words>"] | status | report | log --kind <left|his|merge|deploy|migration|install|answer|other> --what "<what>" --evidence "<evidence>" [--link <url>] [--option "<choice>"]... [--recommend "<choice>"] [--run <command.ps1|command.sh> [--admin]] | strike --at <when> --reason "<why>" | settle --at <when> --how "<what became of it>"   AFK mode, the Supreme Overlord's switch for running the fleet while he is away: on and off are his, made from a terminal of his own and refused in a goblin's; the registered CEO makes them only at his ask, with --asked and his words quoted exactly, which the switch and the report keep; off prints the report of the stretch; status says who turned it on, what was decided so far and what is held for him; log is the registered CEO recording a decision it made under the authority, with its evidence, and a his line, a step only he can take, is the card goblins question or goblins run-request makes: with --run a run item, run as administrator with --admin, and otherwise two or more --option choices and a --recommend that is one of them; strike strikes a line it logged by mistake, and settle settles a line it left for him and saw to later, which is then never asked in the Command Center
  goblins allowance-floor [<claude|codex> <percent>]   say each provider's weekly allowance floor, the percent of its week the fleet keeps back (5 unless set), or set one in the home's config/fleet.json; 0 keeps none, so goblins run until the provider refuses
  goblins start-hook accept <task-id> <file> [--fleet]   accept a start hook a goblin's seal stopped once you judged it safe: the file, by its name, its exact content and the distribution whose RECORD installs it, for the goblins of the task's project, or of every project with --fleet; their seals let it through at every install from then on and still stop it once any of the three differs
  goblins merge-local <id>
  goblins cleanup <id>   return a finished task's worktree, every extra worktree it recorded and its scratch folder, refusing while any of them holds uncommitted work and keeping work that is not on the default branch as a local archive tag
  goblins worktree add <id> <name> [--ref <commit>] | remove <id> <name>   give a task an extra worktree, <id>-<name>, beside its own in the home, recorded on the task so cleanup removes it with the task, or return one early
  goblins backlog done <id>   close a retired delivered task's queued row, preserving its evidence and continuation under Done
  goblins pause <id> | resume <id> | kill <id>   pause, resume or stop a task while preserving its work
  goblins reap [--dry-run] [--apply] [--force <pid|task-id>]... [--json]   find orphaned harness processes, stale dev servers, worktrees, task records and status logs; --apply retires the worktrees, records and logs, and ending a process needs its pid named with --force
  goblins process-plan   what the janitor's process sweep, and a cleanup or a relaunch of each task, would end on this machine now, with the rule that decides each; it reads only and ends nothing
  goblins notify <id> --done --pr <url> [--lesson "<one line>" --cites "<stamp or words of a status line>"]... | --done --closed <issue-url> --decision <sequence> | --done --report <file> | --blocked "<question>" | --failed "<reason>" | --working "<what>" | --waiting-on <task-id|run-id|overlord|ci|deploy|memory> "<why>" [--lavish <html-file>] [--link <https-url>] [--run <command-file>]   a goblin reports its outcome straight into the wake queue, or what it is working on or waiting on; a wait on the Overlord leads with one sentence, puts values he must enter in a Markdown table on the lines after it with each value in backticks, which his card copies, and gives the one link his card opens with --link; --run names a .ps1 or .sh file holding a command he must run, which his card runs with one click in a terminal on the card, where he types into it
  goblins helper start <parent-id> --brief <file> [--title "<short title>"]   a goblin asks the supervisor for one helper goblin: the supervisor starts it only when memory allows, one per goblin at a time and never a helper's, on a branch cut from the goblin's last commit, in local-only mode, reporting to the goblin; it says which helper and branch, or why not and when to ask again
  goblins helper merge <parent-id>   the goblin merges its helper's branch into its own with a merge commit, in its own worktree, and retires the helper; a conflict is left for it to resolve and commit, and asked again it only retires the helper; it stays the one who opens the pull request
  goblins question --id <stable-id> --text "<user question>" [--option "<choice>"]... [--recommend "<exact-choice>"]   registered CEO opens a user decision modal with Other; the answer returns as one normal native message, not a native prompt-tool response
  goblins answer <question-id|wake-seq> --option <choice> [--note "<text>"]   registered CEO answers a goblin's blocked question: delivered like goblins send (queued for a working goblin's next tool call counts as delivered), the notify retired, and the choice, who and when recorded for the board
  goblins answer <question-id> --option <choice> [--note "<text>"] --record-only [--in <where>]   registered CEO records on the board a choice already given another way, for a goblin's notify already acknowledged or answered, and sends nothing; --in names where the Overlord gave it, such as chat, which the CEO's own question needs, and the card reads as his answer there
  goblins review --id <stable-id> --title "<what to look at>" [--task <id>] [--image <path>]... [--lavish <url|html-file> [--reopen]] | --id <stable-id> --withdraw "<reason>" [--task <id>] | --clear <stable-id> --reason "<why>"   report an item that stays in the Command Center until the Overlord answers or clears it, or withdraw your own, or as the registered primary CEO clear any open item, audited; a Scrawl page named by its HTML file is polled by the supervisor, so the Overlord's feedback on it reaches the CEO as a review wake
  goblins deliver --id <stable-id> --title "<what it is>" --file <path> [--download] [--task <id>]   hand the Overlord a file as a Scrawl page that shows it, presented in the Command Center; what he writes there reaches you as a review wake, and --download offers it to download, for a generated asset or a file he asked to keep
  goblins run-request --id <stable-id> --title "<why>" --shell powershell|pwsh|bash [--admin] [--cwd <dir>] --command-file <path>   registered CEO asks the Overlord to run a command with one click in the Command Center, where it runs in a terminal on its card that he types into; the file is read once and runs as a script file, and the exit code and the end of what its terminal showed come back as his answer; --admin runs it elevated after Windows asks him to confirm
  goblins run-request --withdraw <id> --reason "<why>"   registered CEO takes a run item nobody ran off the Command Center, audited in state/runs.audit; Run on it is refused from then on, and a replacement is a new item under a new ID
  goblins present --id <stable-id> --kind browser|review --url <safe-url> [--task <id> [--generation <spawn-gen>]] [--state active|ended] [--ttl 5m] [--watch "<what to watch>"]   report a successful presentation without opening a browser or waiting; omit task only from verified primary CEO context; only one with --watch, a goblin asking the Overlord to watch it, reaches his Command Center
  hook <name>  claude code hook entry points (session-start, pretool-bash, pretool-powershell, pretool-arm, pretool-cd, pretool-subagent, turnend-guard, stop-autoarm)
`

func main() {
	cutover.AdoptOldSettings()
	leaveOwnConsole(os.Args[1:])
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "voice-worker" {
		return runVoiceWorker(args[1:], os.Stdin, stdout, stderr)
	}
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
	sendKey       func(home.Home, string, string) error
	authRefresher func(home.Home) spawn.AuthRefresher
	peek          func(home.Home, string, int) (string, error)
	// idle reports whether the harness in a native terminal, whose screens
	// are screens, or in a stream session waits for input now.
	idle          func(stateDir, id string, screens harness.Screens) (bool, error)
	snapshot      func(context.Context, home.Home) (fleet.Snapshot, error)
	localRuntime  func(context.Context, home.Home) (runtime.Inventory, error)
	cleanup       func(context.Context, home.Home, string, bool) (string, error)
	taskLifecycle func(context.Context, home.Home, lifecycle.Request, string) (state.Lifecycle, error)
	reap          func(context.Context, home.Home, reap.Options) (reap.Result, error)
	// processPlan reads what the fleet's process sweeps would end now, and
	// ends nothing.
	processPlan func(context.Context, home.Home) (processPlan, error)
	// leftRunning reads what a task's goblin started that still runs, for
	// its done or failed report to name; nil names nothing.
	leftRunning func(home.Home, string) ([]fleettree.Placed, error)
	// measureRun records what each role of a task's run spent, for its done
	// or failed report; nil records nothing.
	measureRun func(context.Context, home.Home, state.TaskMeta, string, string) (measure.Record, error)
	speedHint  func(context.Context, string) string
	quota      func(context.Context) (quota.Report, string)
	// keySpend reads the spend the OpenRouter key of one Pi credential source
	// reports at the provider, for the board's dial.
	keySpend     func(context.Context, openrouter.Source) openrouter.Reading
	actionsSpend func(context.Context, time.Time, string) actionsbilling.Reading
	// now is the clock a quota reading is judged by, so a test's reading
	// keeps the date it was taken on; nil is the real clock.
	now func() time.Time
	// startServe and openURL are the launcher's two effects on the machine:
	// a detached supervisor and a browser tab.
	startServe func(home.Home) (<-chan struct{}, error)
	openURL    func(string) error
	// openWindow shows the board in the desktop window, and returns
	// errNoWindow when none sits beside this binary.
	openWindow func(board, stateDir string, background bool) error
	// nativeCEO, liveCEO, focusCEO and attachHerdr are how the launcher
	// finds a live registered CEO, in a native terminal or in Herdr, brings
	// it to the front and hands the terminal to herdr attached to a live
	// Herdr CEO's session.
	nativeCEO   func(string) (string, bool)
	liveCEO     func(string) (herdr.Endpoint, bool)
	focusCEO    func(context.Context, herdr.Endpoint) error
	attachHerdr func(string) int
	// startNativeCEO and attachNative start the CEO in a native terminal and
	// show a native terminal in this console, for a new CEO and a CEO
	// registered in one.
	startNativeCEO startCEO
	attachNative   func(stateDir, id string, stdout, stderr io.Writer) int
	// nativeTerminalRuns reports whether a native terminal's host answers,
	// so a CEO started in terminal ceo is shown before it registers, never
	// started twice.
	nativeTerminalRuns func(stateDir, id string) bool
	// restartCEO restarts the CEO running in native terminal ceo and returns
	// the conversation it last registered with and, when it did not come back
	// on that conversation, why it started a new one.
	restartCEO func(h home.Home, harness string) (supervisor.CEOConversation, string, error)
	// admitLaunch says why the machine has no room yet for another harness,
	// or nil when it has, as a spawn admits one.
	admitLaunch func(h home.Home) error
	// settleCEO answers the known startup dialogs of a CEO just started in
	// native terminal ceo and returns what to tell the Overlord about them.
	settleCEO func(ctx context.Context, stateDir, harness string) []string
	// setupAgent runs the quick start's agent steps and returns the agent
	// the CEO starts as, and choose shows one of its screens and returns the
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
	// readDevDrive reads what this machine says about Dev Drives, for goblins
	// dev-drive and goblins doctor.
	readDevDrive func(context.Context) (devdrive.Machine, error)
	// gateHome is where the gate's home is now (gatehome.Root), and
	// carryGateHome moves it into a Dev Drive folder (carryGateHome), for
	// goblins dev-drive, goblins doctor, goblins install and the board's gate step.
	gateHome      func() (string, error)
	carryGateHome func(ctx context.Context, old, devDrive, lock string, tries int, every time.Duration, out io.Writer) (string, bool, error)
	// restartBoard restarts the supervisor serving a home onto what goblins
	// dev-drive move changed (restartBoardOntoTheDrive).
	restartBoard func(h home.Home, stdout io.Writer)
	// readDefender reads what Microsoft Defender recorded since a time, for
	// goblins defender.
	readDefender func(context.Context, time.Time) (defender.Report, error)
	// repoActivity reads what GitHub says is happening in the repository a
	// checkout's origin names, for goblins tickets.
	repoActivity func(ctx context.Context, checkout string, now time.Time) (tickets.Activity, error)
	// overlapTimeout bounds the repoActivity read goblins spawn makes. Zero, in
	// every runtime but a test's, is the overlapTimeout constant.
	overlapTimeout time.Duration
	// repositoryOf names the GitHub repository a checkout's origin is, for
	// goblins tickets --allow-public-tickets.
	repositoryOf func(ctx context.Context, checkout string) (string, error)
	// github runs gh and git for goblins issue file; nil runs the machine's.
	github execx.Runner
	// githubAPI reads and writes GitHub's API for every command and the
	// board: one client, so a process asks gh for a token at most once.
	githubAPI *githubapi.Client
	// requestHelper asks the supervisor for a goblin's helper.
	requestHelper func(home.Home, supervisor.HelperRequest) (supervisor.HelperStart, error)
	// requestBrowser asks the supervisor to lend or take back a login's
	// browser, and handBrowser points this terminal's browser session at
	// one, or with no address lets go of it.
	requestBrowser func(home.Home, supervisor.BrowserRequest) (supervisor.BrowserLease, error)
	handBrowser    func(context.Context, string) error
	// stepCard sends a card's asker's step to the supervisor; nil is the
	// supervisor's pipe.
	stepCard func(home.Home, supervisor.CredentialStep) error
	// projectServices builds the local services of a home, writing what a
	// start says while it waits to progress.
	projectServices func(home.Home, io.Writer) services.Service
	// sandboxEngine is the Docker engine a sandbox task's runs use, which
	// says what a start of it does on its writer; nil is the machine's docker.
	sandboxEngine func(io.Writer) sandboxEngine
	// dictationClient downloads dictation's engine and model; nil is the
	// one the voice package makes, which follows only https.
	dictationClient *http.Client
	// crawlerNpm runs npm ci for goblins crawler setup and goblins install; nil runs
	// the machine's.
	crawlerNpm execx.Runner
	// switchAFK asks the supervisor to turn AFK mode on or off, and logAFK to
	// log a decision made under it; nil is the supervisor's pipe.
	switchAFK func(h home.Home, on bool, asked string) error
	logAFK    func(h home.Home, entry afk.Entry) error
	// logTrainLanding asks the supervisor to log a pull request a merge
	// train landed while AFK mode is on; nil is the supervisor's pipe.
	logTrainLanding func(h home.Home, trainID, url string) error
	// pullRequestState asks GitHub whether a pull request is open, closed or
	// merged, for goblins afk log --kind merge; nil asks through gh.
	pullRequestState func(ctx context.Context, url string) (supervisor.PullRequestInfo, error)
	// strikeAFK asks the supervisor to strike through a line the CEO logged
	// by mistake.
	strikeAFK func(h home.Home, at time.Time, reason string) error
	// settleAFK asks the supervisor to settle a line the CEO left for the
	// Overlord and saw to later.
	settleAFK func(h home.Home, at time.Time, how string) error
	// availableMemory reads physical and commit availability for the turn
	// goblins gate test takes before its checks, gateBudget is how long
	// the tests of a level may run, and gateRun runs one of the step's
	// commands in dir and returns its exit code, with an error unless it
	// passed. gateProgress is how often a run that holds a turn says beside
	// it how far its tests are, and gatePlanLimit how long a plan may read
	// the checkout.
	availableMemory func() (supervisor.Memory, error)
	// gateDisk reads the free disk a gate test run in a folder has beside its
	// floor; nil reads the machine's.
	gateDisk      func(dir string) (supervisor.Disk, error)
	gateWaitLimit time.Duration
	gateBudget    func(gatetest.Level) time.Duration
	gateRun       func(command []string, dir string, env []string, stdout, stderr io.Writer) (int, error)
	gateProgress  time.Duration
	gatePlanLimit time.Duration
	// agentLineLimit is the longest a run that a gate's agent started waits
	// in line (gateAgentLineLimit).
	agentLineLimit time.Duration
	// pushRun runs one check of goblins gate prepush in dir and returns its exit
	// code, ending the check when its context ends. pushMemoryPoll is how
	// often a run that waits for the memory floor reads memory again, and
	// pushAgain how long the second run of a failed test may take.
	pushRun        func(ctx context.Context, command []string, dir string, env []string, stdout, stderr io.Writer) (int, error)
	pushMemoryPoll time.Duration
	pushAgain      time.Duration
	// interrupts starts delivering the interrupts this process is sent, such
	// as Ctrl+C and Ctrl+Break, to the channel it returns in place of ending
	// the process, until stop is called. goblins gate turn leaves the line on
	// one while it waits and keeps its turn through one while its command
	// runs.
	interrupts func() (signals <-chan os.Signal, stop func())
	// gateTurnSelf is the program and the leading arguments that run goblins
	// gate turn, which goblins gate turn --detach starts the run it detaches
	// with; nil is this binary's gate turn.
	gateTurnSelf []string
	// trainEvery is how often goblins pr train looks at its train's CI. Zero, in
	// every runtime but a test's, is the trainEvery constant.
	trainEvery time.Duration
	// askCIRunner asks this machine's desktop CI runner service, as goblins
	// ci-runner does.
	askCIRunner func(ctx context.Context, request cirunner.Request, reply func(cirunner.Reply)) error
}

// resolveProject turns a --project argument into a checkout directory: a path
// as written, a bare name looked up under the projects root.
func (r commandRuntime) resolveProject(arg string) (string, error) {
	return projectcfg.Resolve(arg, r.projectsRoot)
}

func defaultCommandRuntime() commandRuntime {
	github := githubapi.New(execx.OSRunner{})
	return commandRuntime{
		resolveHome:     home.Resolve,
		requestHelper:   supervisor.RequestHelper,
		requestBrowser:  supervisor.RequestBrowser,
		handBrowser:     handBrowserSession(execx.OSRunner{}),
		projectServices: defaultProjectServices,
		spawn: func(ctx context.Context, h home.Home, request spawn.Request) (spawn.Result, error) {
			commands := execx.OSRunner{}
			self, err := os.Executable()
			if err != nil {
				return spawn.Result{}, err
			}
			service := spawn.Service{
				Worktrees:    worktree.Service{Commands: commands, DataDir: h.Data, Root: h.Worktrees()},
				Harness:      harness.DefaultRegistry(),
				Auth:         auth.SpawnPreflight{DataDir: h.Data, Caches: h.Caches(), Runner: commands},
				Commands:     commands,
				HomeRoot:     h.Root,
				StateDir:     h.State,
				DataDir:      h.Data,
				Board:        hookBoard(h.State),
				ProjectsRoot: os.Getenv(install.ProjectsRootVariable),
				ScratchRoot:  h.Scratch(),
				PolicyPath:   filepath.Join(h.Root, "config", "pipeline.json"),
				HostCommand:  []string{self, "host"},
				PromptSince:  nativePromptSince(h),
				CEOSession:   func() (string, string) { return supervisor.RegisteredCEOSession(h.State) },
				Progress:     os.Stderr,
				Admit: func() error {
					memory, err := supervisor.MachineMemory()
					if err != nil {
						return err
					}
					disk, err := supervisor.MachineDisk(h)
					if err != nil {
						return fmt.Errorf("spawn: free disk cannot be read, so nothing starts: %w", err)
					}
					return supervisor.CheckLaunch(memory, disk)
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
				Worktrees:    worktree.Service{Commands: commands, DataDir: h.Data, Root: h.Worktrees()},
				Harness:      harness.DefaultRegistry(),
				Auth:         auth.SpawnPreflight{DataDir: h.Data, Caches: h.Caches(), Runner: commands},
				Commands:     commands,
				HomeRoot:     h.Root,
				StateDir:     h.State,
				DataDir:      h.Data,
				Board:        hookBoard(h.State),
				ProjectsRoot: os.Getenv(install.ProjectsRootVariable),
				ScratchRoot:  h.Scratch(),
				HostCommand:  []string{self, "host"},
				PromptSince:  nativePromptSince(h),
				CEOSession:   func() (string, string) { return supervisor.RegisteredCEOSession(h.State) },
				EndLeft: func(ctx context.Context, meta state.TaskMeta) ([]string, error) {
					return endLeftProcesses(ctx, h, meta)
				},
				Took: supervisor.NativeTook,
			}
			return service.Switch(ctx, request)
		},
		sendText: func(ctx context.Context, h home.Home, target, text string) error {
			meta, err := nativeTask(h, target)
			if err != nil {
				return err
			}
			receipt := supervisor.PrepareSendActivity(h, target)
			stamped, since := fleet.Stamp(text), time.Now()
			err = (spawn.Service{StateDir: h.State, PromptSince: nativePromptSince(h), Took: supervisor.NativeTook}).SendNative(ctx, meta, stamped)
			switch {
			case errors.Is(err, fleet.ErrQueuedForToolCall):
				if receiptErr := receipt.Queued(stamped, since); receiptErr != nil {
					fmt.Fprintln(os.Stderr, "Message queued; board activity receipt unavailable. Do not resend for this notice.")
				}
				return err
			case err != nil:
				return err
			}
			if err := receipt.Taken(); err != nil {
				fmt.Fprintln(os.Stderr, "Message accepted; board activity receipt unavailable. Do not resend for this notice.")
			}
			return nil
		},
		sendKey: func(h home.Home, target, key string) error {
			meta, err := nativeTask(h, target)
			if err != nil {
				return err
			}
			return spawn.Service{StateDir: h.State}.SendNativeKey(meta, key)
		},
		authRefresher: func(h home.Home) spawn.AuthRefresher {
			return spawn.AuthRefresher{
				StateDir: h.State,
				DataDir:  h.Data,
				Panes:    spawn.NativeLiveness{StateDir: h.State},
			}
		},
		peek: peekTerminal,
		idle: func(stateDir, id string, screens harness.Screens) (bool, error) {
			record, err := host.ReadRecord(stateDir, id)
			if err != nil {
				return false, err
			}
			if record.Stream {
				session, err := host.ReadStreamState(record)
				return err == nil && session.IsIdle(), err
			}
			screen, err := host.ReadScreen(record)
			return err == nil && screens.IsIdleAtEmptyComposer(screen), err
		},
		snapshot: func(ctx context.Context, h home.Home) (fleet.Snapshot, error) {
			return fleet.BuildSnapshot(ctx, h, fleet.NewTerminalEndpoint(h.State, &herdr.Client{Commands: execx.OSRunner{}}))
		},
		localRuntime: func(ctx context.Context, h home.Home) (runtime.Inventory, error) {
			commands := execx.OSRunner{}
			// A store whose folder cannot be named is measured as not named.
			verificationStore, _ := verify.StoreDir()
			return runtime.Collector{
				Home:              h,
				Docker:            runtime.Docker{Commands: commands},
				System:            runtime.System{Commands: commands},
				VerificationStore: verificationStore,
			}.Collect(ctx)
		},
		cleanup:       defaultCleanup,
		taskLifecycle: defaultTaskLifecycle,
		reap:          defaultReap,
		processPlan:   defaultProcessPlan,
		leftRunning:   defaultLeftRunning,
		measureRun:    measureRun,
		speedHint: func(ctx context.Context, name string) string {
			return telemetry.SpeedHint(ctx, gatedb.Runner{Next: execx.OSRunner{}}, name)
		},
		quota:        quota.Reader{Commands: execx.OSRunner{}}.Read,
		actionsSpend: actionsbilling.Reader{GitHub: github}.Read,
		keySpend: func(ctx context.Context, source openrouter.Source) openrouter.Reading {
			return openrouter.Reader{Source: source}.Read(ctx)
		},
		projectsRoot:  install.MachineProjectsRoot,
		readDevDrive:  func(ctx context.Context) (devdrive.Machine, error) { return devdrive.Read(ctx, execx.OSRunner{}) },
		gateHome:      gatehome.Root,
		carryGateHome: carryGateHome,
		restartBoard:  restartBoardOntoTheDrive,
		readDefender: func(ctx context.Context, since time.Time) (defender.Report, error) {
			return defender.Read(ctx, execx.OSRunner{}, since)
		},
		startServe:  startDetachedServe,
		openURL:     openInBrowser,
		openWindow:  openWindow,
		nativeCEO:   supervisor.NativeCEO,
		liveCEO:     supervisor.LiveCEO,
		focusCEO:    focusCEOInHerdr,
		attachHerdr: attachHerdr,
		killTree: func(pid int) error {
			return killTree(context.Background(), execx.OSRunner{}, pid)
		},
		startNativeCEO:     startNativeCEO,
		attachNative:       attachNative,
		nativeTerminalRuns: supervisor.NativeTerminalRuns,
		settleCEO:          settleNativeCEO,
		restartCEO:         restartCEO,
		admitLaunch: func(h home.Home) error {
			memory, err := supervisor.MachineMemory()
			if err != nil {
				return err
			}
			disk, err := supervisor.MachineDisk(h)
			if err != nil {
				return fmt.Errorf("free disk cannot be read, so nothing comes back: %w", err)
			}
			return supervisor.CheckLaunch(memory, disk)
		},
		setupAgent:      setupAgent,
		choose:          onboarding.AskConsole,
		repoActivity:    repositoryActivity(github),
		repositoryOf:    tickets.GitHub{Commands: execx.OSRunner{}}.RepositoryOf,
		githubAPI:       github,
		availableMemory: supervisor.MachineMemory,
		gateWaitLimit:   time.Hour,
		agentLineLimit:  gateAgentLineLimit,
		gateBudget:      gateBudget,
		gateRun:         runGateCommand,
		gateProgress:    5 * time.Second,
		gatePlanLimit:   planLimit,
		pushRun:         runPushCommand,
		pushMemoryPoll:  2 * time.Second,
		pushAgain:       5 * time.Minute,
		askCIRunner: func(ctx context.Context, request cirunner.Request, reply func(cirunner.Reply)) error {
			return cirunner.Ask(ctx, cirunner.PipeName, request, reply)
		},
		interrupts: func() (<-chan os.Signal, func()) {
			signals := make(chan os.Signal, 1)
			signal.Notify(signals, os.Interrupt)
			return signals, func() { signal.Stop(signals) }
		},
	}
}

func runWithRuntime(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) > 0 && args[0] == "help" {
		return runHelp(args[1:], stdout, stderr, codegoblins.Contract)
	}
	defer aboveTheWork(args)()
	if len(args) == 1 && args[0] == "--board" {
		return runBoardLauncher(stdout, stderr, runtime)
	}
	if len(args) == 2 && args[0] == "--window" && args[1] == "--locate" {
		return runWindowLocator(stdout, stderr, runtime)
	}
	if len(args) > 0 && args[0] == "--window" && (len(args) == 1 || len(args) == 2 && args[1] == "--background") {
		return runWindowLauncher(stdout, stderr, runtime, len(args) == 2)
	}
	if len(args) > 0 && args[0] == "setup" {
		if len(args) != 1 {
			fmt.Fprintln(stderr, "usage: goblins setup")
			return 2
		}
		return runQuickstart(stdout, stderr, runtime, true, false, "")
	}
	if len(args) > 0 && args[0] == "resume" && (len(args) == 1 || strings.HasPrefix(args[1], "-")) {
		fs := flag.NewFlagSet("goblins resume", flag.ContinueOnError)
		fs.SetOutput(stderr)
		harness := fs.String("harness", "", "the harness to restart the CEO as: claude, codex or pi")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if fs.NArg() != 0 {
			fmt.Fprintln(stderr, "usage: goblins resume [--harness <claude|codex|pi>]")
			return 2
		}
		if *harness != "" && !slices.Contains(ceoHarnesses, *harness) {
			fmt.Fprintf(stderr, "goblins: --harness %q is not claude, codex or pi\n", *harness)
			return 2
		}
		return runQuickstart(stdout, stderr, runtime, false, true, *harness)
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fs := flag.NewFlagSet("goblins", flag.ContinueOnError)
		fs.SetOutput(stderr)
		harness := fs.String("harness", "", "the harness goblins starts the CEO as, remembered for later starts: claude, codex or pi")
		if err := fs.Parse(args); err != nil {
			return 2
		}
		if fs.NArg() != 0 {
			fmt.Fprintf(stderr, "goblins: unexpected argument %q; goblins takes only --harness <claude|codex|pi>, or a command\n", fs.Arg(0))
			return 2
		}
		if *harness != "" && !slices.Contains(ceoHarnesses, *harness) {
			fmt.Fprintf(stderr, "goblins: --harness %q is not claude, codex or pi\n", *harness)
			return 2
		}
		return runQuickstart(stdout, stderr, runtime, false, false, *harness)
	}
	if refusal := refuseAnotherAgentsSession(args, runtime.resolveHome); refusal != "" {
		fmt.Fprintln(stderr, refusal)
		return 1
	}
	if refusedWhileInstalling(args, stderr, runtime) {
		return 1
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
	case "remote":
		return runRemote(args[1:], stdout, stderr, runtime)
	case "update":
		return runUpdate(args[1:], stdout, stderr, runtime)
	case "host":
		return runHost(args[1:], stderr)
	case "codex-guard":
		return runCodexGuard(args[1:], stderr, time.Now())
	case "native-hook":
		return runNativeHook(args[1:], os.Stdin, stdout, stderr, runtime)
	case "hooks":
		return runNativeSetup(args[1:], stdout, stderr, runtime)
	case "version":
		fmt.Fprintf(stdout, "goblins %s\n", version)
		return 0
	case "install":
		return runInstall(args[1:], stdout, stderr, runtime)
	case "uninstall":
		return runUninstall(args[1:], os.Stdin, stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr, runtime)
	case "dictation":
		return runDictation(args[1:], stdout, stderr, runtime)
	case "crawler":
		return runCrawler(args[1:], stdout, stderr, runtime)
	case "dev-drive":
		return runDevDrive(args[1:], stdout, stderr, runtime)
	case "defender":
		return runDefender(args[1:], stdout, stderr, runtime)
	case "allowance-floor":
		return runAllowanceFloor(args[1:], stdout, stderr, runtime)
	case "start-hook":
		return runStartHook(args[1:], stdout, stderr, runtime)
	case "home":
		return runHome(args[1:], stdout, stderr)
	case "cutover":
		return runCutover(args[1:], stdout, stderr, func() (cutover.Machine, error) {
			return cutoverMachine(installbase.NewEnvStore(execx.OSRunner{}))
		})
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
	case "ci-runner":
		return runCIRunner(args[1:], stdout, stderr, runtime)
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
	case "backlog":
		return runBacklog(args[1:], stdout, stderr, runtime)
	case "switch":
		return runSwitch(args[1:], stdout, stderr, runtime)
	case "pause", "resume", "kill":
		return runLifecycle(args[0], args[1:], stdout, stderr, runtime)
	case "send":
		return runSend(args[1:], stdout, stderr, runtime)
	case "peek":
		return runPeek(args[1:], stdout, stderr, runtime)
	case "fleet-view":
		return runFleet(args[1:], stdout, stderr, runtime)
	case "services":
		return runServices(args[1:], stdout, stderr, runtime)
	case "shared":
		return runShared(args[1:], stdout, stderr, runtime)
	case "sandbox":
		return runSandbox(args[1:], stdout, stderr, runtime)
	case "runtime":
		return runRuntime(args[1:], stdout, stderr, runtime)
	case "tickets":
		return runTickets(args[1:], stdout, stderr, runtime)
	case "issue":
		return runIssue(args[1:], stdout, stderr, runtime)
	case "brief":
		return runBrief(args[1:], stdout, stderr, runtime)
	case "pr":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "goblins pr: check, merge or train subcommand is required")
			return 2
		}
		return runPR(args[1], args[2:], stdout, stderr, execx.OSRunner{}, runtime)
	case "merge-local":
		return runMergeLocal(args[1:], stdout, stderr)
	case "cleanup":
		return runCleanup(args[1:], stdout, stderr, runtime)
	case "worktree":
		return runWorktree(args[1:], stdout, stderr, runtime)
	case "reap":
		return runReap(args[1:], stdout, stderr, runtime)
	case "process-plan":
		return runProcessPlan(args[1:], stdout, stderr, runtime)
	case "notify":
		return runNotify(args[1:], stdout, stderr, runtime)
	case "helper":
		return runHelper(args[1:], stdout, stderr, runtime)
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
	case "memory":
		return runMemory(args[1:], stdout, stderr)
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
		// Only the CEO's own session takes the session lock with its digest.
		// Any other reads it: the Overlord's own terminal, and a session
		// that is not the CEO's.
		program, err := supervisor.OwnSession(h.State)
		if err != nil {
			err = digest.ComposeReadOnly(h, err.Error(), stdout)
		} else {
			err = digest.Compose(h, program.PID, "", stdout)
		}
		if err != nil {
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
			fmt.Fprintln(stderr, "goblins watch: not a primary home")
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
		fmt.Fprintf(stderr, "goblins: unknown command %q\n%s", args[0], usage)
		return 2
	}
}
