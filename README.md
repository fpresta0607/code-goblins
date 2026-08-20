<h1 align="center">code-goblins</h1>

<p align="center">
  <a href="https://img.shields.io/badge/platform-Windows-blue?style=flat-square"
    ><img
      alt="Platform"
      src="https://img.shields.io/badge/platform-Windows-blue?style=flat-square"
  /></a>
  <a href="https://img.shields.io/badge/go-1.26-blue?style=flat-square"
    ><img
      alt="Go"
      src="https://img.shields.io/badge/go-1.26-blue?style=flat-square"
  /></a>
</p>

<h3 align="center">Talk to one agent. Ship with a crew of goblins.</h3>

## What it is

code-goblins turns one coding agent into a fleet.
You talk to a single agent - the **CFO** (Chief Fuckaround Officer) - and it runs a crew of worker agents - **code goblins** - in parallel, each in its own isolated git worktree, supervised to completion.
The CFO hands you finished PRs, approved local merges, or standalone investigation reports.

code-goblins is a Windows-native rewrite of [firstmate](https://github.com/kunchenguid/firstmate): one compiled Go binary, `cfo.exe`, replaces upstream's bash script layer.

Upstream works, but it is slow on Windows: Git Bash emulates `fork()`, so every subprocess spawn and command substitution costs 20-50x what it does on Linux, and the scripts shell out to `jq`, `grep`, and `git` hundreds of times per operation.
code-goblins collapses that layer into a single binary that does JSON, string, and file work in-process.

## Status

Windows-native v1 is the active target.
The core fleet loop is implemented and green: spawn, steer, inspect, deliver, the durable wake queue, and restart-proof state.
The full design and the explicit v1 scope live in [docs/superpowers/specs/2026-08-12-windows-native-fork-design.md](docs/superpowers/specs/2026-08-12-windows-native-fork-design.md).

## What works today

- **One fleet binary** - `cfo.exe`, downloaded by `install.ps1` (or built from source with `go build ./cmd/cfo`). `install.ps1 -Bootstrap` also installs `showcase-axi.exe` (the repo-owned review surface) and the rest of the toolchain.
- **Real Windows sessions** - goblins run in [Herdr](https://herdr.dev), one tab per goblin.
- **Isolated worktrees** - every goblin gets a clean worktree from [treehouse](https://github.com/kunchenguid/treehouse).
- **Four harnesses** - Claude Code, Codex, Pi, and Kimi, each with typed, validated launch mapping; claude, codex, and kimi start as named native Herdr agents (`gb-<id>`) and receive their brief through `herdr agent prompt`, while pi is typed into the prepared pane shell (Herdr's Windows agent start cannot run pi's npm `.cmd` shim).
- **Supervision without babysitting** - Claude Code hooks plus `cfo watch` wake the CFO only when something needs attention; a turn-end guard refuses to let a turn end blind while work is in flight.
- **Restart-proof state** - tasks, metadata, and the wake queue live on disk under `$CFO_HOME`.
- **In-place harness switching** - `cfo switch <id> --harness claude --model opus` stops a goblin's harness on its own terms and relaunches it in the same pane and worktree, resuming its session when the harness can and handing it a written handoff when it cannot. A harness being refused by its provider shows as `harness-erroring` in `cfo fleet-view`, and `data/routing.json` holds the standing answer.
- **Per-project authentication** - each project declares its services in `data/projects/<name>/auth.json`; `cfo auth` probes them, adopts credentials the machine already holds, and `cfo spawn` injects the usable ones into the goblin's pane before the harness starts. Credentials live in Windows Credential Manager (or `~/.cfo/credentials/` with owner-only ACLs), never in a repository and never in the output.
- **AXI integrations** - `tasks-axi` (backlog) and `quota-axi` (dispatch) stay thin subprocess integrations in `cfo`; `gh-axi` (GitHub) and `chrome-devtools-axi` (browser) ship as skills in `.agents/skills/`; `showcase-axi` (review surface) is repo-owned and built alongside `cfo.exe`.

## Commands

```text
cfo install [--uninstall]            wire this checkout into the machine so a session in any repo is supervised: CFO_HOME and PATH at user scope, and the CFO hooks merged into ~/.claude/settings.json
cfo doctor                           check the tools cfo needs and how to install them; probe each harness's spawn health (ok/broken); print the measured speed table when telemetry exists; print the active switch rules from data/routing.json
cfo auth <project> [--check|--fix] [--env]   preflight a project's services from data/projects/<name>/auth.json; --fix adopts credentials the machine already holds and asks once for the rest
cfo auth store <NAME> [value]        store one credential (omit the value to read it from stdin)
cfo auth list                        list stored credential names
cfo spawn <id> --project <path> --brief <path> --harness <claude|codex|pi|kimi> [--mode <no-mistakes|direct-PR|local-only>] [--model <model>] [--effort <level>] [--yolo]
cfo switch <id> [--harness <h>] [--model <m>] [--effort <e>] [--force-dirty]   change a running goblin's harness/model/effort in place, keeping its id, pane, and worktree
cfo send <target> [--key <key>] [--no-auto-submit] <text...>
cfo peek <target> [lines]
cfo fleet-view [--json]
cfo brief <id> --project <path> [--kind <ship|scout>] [--mode <no-mistakes|direct-PR|local-only>]
cfo pr check <id> <url>
cfo pr merge <url> [--method <merge|squash|rebase>] [--delete-branch]
cfo merge-local <id>
cfo cleanup <id>                       close the task tab and return its clean, proven-inactive worktree through treehouse
cfo notify <id> --done --pr <url> | --blocked "<question>" | --failed "<reason>"   a goblin reports its outcome (PR URL, question, or failure) straight into the wake queue
cfo drain                            print or acknowledge the wake queue
cfo session-start                    print the session-start digest
cfo watch                            run one triage cycle by hand
cfo hook <name>                      Claude Code hook entry points (session-start, pretool-arm, pretool-cd, pretool-subagent, turnend-guard, stop-autoarm)
cfo version
```

## From clone to first goblin

```powershell
git clone https://github.com/fpresta0607/code-goblins
cd code-goblins
powershell -NoProfile -ExecutionPolicy Bypass -File install.ps1 -Bootstrap
```

`install.ps1 -Bootstrap` downloads (or builds) `cfo.exe` and `showcase-axi.exe`, then installs every missing tool `cfo doctor` checks that has a scriptable installer - git, gh, claude, herdr, treehouse, codex, pi, tasks-axi, quota-axi, no-mistakes, gh-axi, and chrome-devtools-axi.
Kimi is the one `cfo doctor` check with no scriptable installer, so it is printed as a manual step instead of being installed.
It also creates the `.claude/skills` / `.codex/skills` junctions that point at the bundled `.agents/skills/`.
The Claude Code hooks are wired separately by `cfo install`, described below, because they are written to your own user configuration rather than to this repository.
The script is idempotent and safe to rerun.

```powershell
cfo doctor
```

Green means the toolchain is ready.
Anything still missing is printed with its exact install command.

Prove the loop end to end with a trivial local-only task:

```powershell
cfo brief smoke --project . --mode local-only
# put a one-line task in data/smoke/brief.md, e.g. "add a line to README.md"
cfo spawn smoke --project . --brief data/smoke/brief.md --harness pi --mode local-only
# once the goblin has landed its branch:
cfo cleanup smoke
```

### What still needs manual steps

`install.ps1` installs binaries, not accounts, and it assumes Node.js (npm) and winget are already installed, so do these once by hand:

- **Node.js (npm).** Seven tools install via `npm install -g`; if `npm` is missing, install Node.js first: `winget install OpenJS.NodeJS.LTS`.
- **winget.** It installs git and gh; winget ships with Windows App Installer, which is preinstalled on Windows 10 and 11.
- **Go.** Only needed for the source-build fallback when no release binary exists: `winget install GoLang.Go`.
- **Harness sign-ins.** claude, codex, pi, and kimi each need their own login before they can run goblins.
- **GitHub auth.** `gh auth login` (gh-axi reuses the same token).
- **Kimi Code CLI.** It has no scriptable installer; get it from [kimi.com](https://www.kimi.com) and sign in.
- **no-mistakes per project.** Each project you want gated needs `no-mistakes init` (plus a push target) before `no-mistakes` delivery mode works.

### Run the CFO from any repo

The CFO's supervision arrives through Claude Code hooks: the session-start digest, the wake queue, the turn-end guard, and the watcher that re-arms itself while goblins are in flight.
Nothing in this repository registers them for you, so until you run `cfo install` a session opened outside this checkout has none of them, and it has them silently - nothing announces that supervision is off.

```powershell
cd c:/dev/code-goblins
cfo install
```

That is the whole setup, and it is what lets you drive the fleet from a terminal inside the project you are actually working on, with that project's own editor, MCP servers, virtualenv, and `CLAUDE.md`.

It changes exactly four things, and prints each one:

- **`CFO_HOME`**, at user scope, pointing at this checkout. Every hook resolves `cfo.exe` through it.
- **Your user PATH**, with this checkout appended so `cfo` runs from anywhere. The raw registry value is read and rewritten with its type preserved. `setx`, which truncates any value it writes at 1024 characters, is never used.
- **`~/.claude/settings.json`**, which is your personal Claude Code configuration and not this project's. The CFO's hooks are merged into whatever hooks are already in it; every hook you already had, and every other key in the file, is left exactly as it was. The file is copied to `settings.json.cfo-install.bak` before it is written, and rewritten as standard two-space JSON, so expect a formatting change even though nothing but the hooks moved. (If you set `CLAUDE_CONFIG_DIR`, that directory is used instead.)
- **This checkout's `.claude/settings.json`**, if it still carries a hooks block from an older version. With the user-scope hooks in place, both files would fire every hook twice inside code-goblins: two session digests, two wake handlers. Its `permissions` block is left alone.

Running it a second time reports "already installed" and writes nothing.

To back out:

```powershell
cfo install --uninstall
```

It removes the CFO's hooks from your settings and leaves yours untouched, unsets `CFO_HOME`, and takes this checkout back off your PATH.

Goblins never receive these hooks, even though the hooks are now global: `cfo spawn` stamps every goblin pane with `CFO_ROLE=goblin`, and every hook exits immediately when it sees it.
A goblin is the work being supervised, not the supervisor.

### Daily flow

```sh
cd c:/dev/code-goblins
herdr
```

Herdr is your cockpit. Launch the CFO in a pane, then ask away:

```sh
claude    # or: codex, pi, kimi
```

The CFO reads its contract and does the rest - goblins appear as Herdr tabs (`gb-<id>`), each in a clean treehouse worktree.
`cfo spawn` targets the Herdr session named `default` (or `$HERDR_SESSION`), so keep the CFO and its goblins in the same session.

## Cut from v1

These upstream features are not yet ported to the Go binary:

- Grok and OpenCode harness adapters (Kimi is supported)
- tmux, zellij, Orca, and cmux session backends
- Secondmates (persistent and remote)
- Relay (public X / Discord mentions)
- AFK mode

## Repo layout

- `cmd/cfo/` - the `cfo.exe` entry point and command handlers.
- `cmd/showcase-axi/` - the `showcase-axi.exe` entry point for the review surface.
- `internal/` - one package per subsystem (herdr, treehouse, spawn, fleet, monitor, wake, lock, state, home, watch, harness, auth, routing, axi, execx, fsx, claudehook, digest, doctor, guard, crewstate, supervise, telemetry, proc, showcase).
- `docs/superpowers/` - the design spec and implementation plans.
- `tests/acceptance/` - the opt-in real-session Windows acceptance script.
- `.agents/skills/` - the fleet's skills, synced from user scope except `showcase`, which this repo owns; kimi and pi read it directly, and `install.ps1` junctions it for claude and codex.
- `AGENTS.md.example` / `CLAUDE.md.example` - templates for your global user config.
- `AGENTS.md` - the CFO's operating contract; `CLAUDE.md` points to it.

## Development

```sh
go vet ./...
go test ./...
go build ./cmd/cfo
go build ./cmd/showcase-axi
```

CI runs on `windows-latest` and gates `go vet`, `go test ./... -count=1`, and `go build` of both binaries.
Unit tests use fakes for subprocess and Herdr behavior.
The real-session acceptance suite (`tests/acceptance/plan3_windows.ps1`) needs real Herdr, treehouse, and harness binaries and refuses to run without `CFO_PLAN3_REAL=1`.

## Contributing

Contributions are welcome - see [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT - see [LICENSE](LICENSE).
Derived from [firstmate](https://github.com/kunchenguid/firstmate), also MIT-licensed.
