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
- **Per-project authentication** - each project declares its services in `data/projects/<name>/auth.json`; `cfo auth` probes them, adopts credentials the machine already holds, and `cfo spawn` injects the usable ones into the goblin's pane before the harness starts. Credentials live in Windows Credential Manager (or `~/.cfo/credentials/` with owner-only ACLs), never in a repository and never in the output.
- **AXI integrations** - `tasks-axi` (backlog) and `quota-axi` (dispatch) stay thin subprocess integrations in `cfo`; `gh-axi` (GitHub) and `chrome-devtools-axi` (browser) ship as skills in `.agents/skills/`; `showcase-axi` (review surface) is repo-owned and built alongside `cfo.exe`.

## Commands

```text
cfo doctor                           check the tools cfo needs and how to install them; probe each harness's spawn health (ok/broken) and print the measured speed table when telemetry exists
cfo auth <project> [--check|--fix] [--env]   preflight a project's services from data/projects/<name>/auth.json; --fix adopts credentials the machine already holds and asks once for the rest
cfo auth store <NAME> [value]        store one credential (omit the value to read it from stdin)
cfo auth list                        list stored credential names
cfo spawn <id> --project <path> --brief <path> --harness <claude|codex|pi|kimi> [--mode <no-mistakes|direct-PR|local-only>] [--model <model>] [--effort <level>] [--yolo]
cfo send <target> [--key <key>] [--no-auto-submit] <text...>
cfo peek <target> [lines]
cfo fleet-view [--json]
cfo brief <id> --project <path> [--kind <ship|scout>] [--mode <no-mistakes|direct-PR|local-only>]
cfo pr check <id> <url>
cfo pr merge <url> [--method <merge|squash|rebase>] [--delete-branch]
cfo merge-local <id>
cfo cleanup <id>                       close the task tab and return its clean, proven-inactive worktree through treehouse
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
It also wires the Claude Code hooks in `.claude/settings.json` and creates the `.claude/skills` / `.codex/skills` junctions that point at the bundled `.agents/skills/`.
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
- `internal/` - one package per subsystem (herdr, treehouse, spawn, fleet, monitor, wake, lock, state, home, watch, harness, auth, axi, execx, fsx, claudehook, digest, doctor, guard, crewstate, supervise, telemetry, proc, showcase).
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
