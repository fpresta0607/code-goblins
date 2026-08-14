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

- **One binary** - `cfo.exe`, built from source with `go build ./cmd/cfo`. A prebuilt release and `install.ps1` are planned.
- **Real Windows sessions** - goblins run in [Herdr](https://herdr.dev), one tab per goblin.
- **Isolated worktrees** - every goblin gets a clean worktree from [treehouse](https://github.com/kunchenguid/treehouse).
- **Three harnesses** - Claude Code, Codex, and Pi, each with typed, validated launch mapping.
- **Supervision without babysitting** - Claude Code hooks plus `cfo watch` wake the CFO only when something needs attention; a turn-end guard refuses to let a turn end blind while work is in flight.
- **Restart-proof state** - tasks, metadata, and the wake queue live on disk under `$CFO_HOME`.
- **AXI integrations** - `tasks-axi` and `quota-axi` stay thin subprocess integrations for the backlog and quota-aware dispatch.

## Commands

```text
cfo doctor                           check the tools cfo needs and how to install them
cfo spawn <id> --project <path> --brief <path> --harness <claude|codex|pi> [--mode <no-mistakes|direct-PR|local-only>] [--model <model>] [--effort <level>] [--yolo]
cfo send <target> [--key <key>] <text...>
cfo peek <target> [lines]
cfo fleet-view [--json]
cfo brief <id> --project <path> [--kind <ship|scout>] [--mode <no-mistakes|direct-PR|local-only>]
cfo pr check <id> <url>
cfo pr merge <url> [--method <merge|squash|rebase>] [--delete-branch]
cfo merge-local <id>
cfo drain                            print or acknowledge the wake queue
cfo session-start                    print the session-start digest
cfo watch                            run one triage cycle by hand
cfo hook <name>                      Claude Code hook entry points (session-start, pretool-arm, pretool-cd, pretool-subagent, turnend-guard, stop-autoarm)
cfo version
```

## Quick start

Prerequisites: Git, the GitHub CLI (`gh auth login`), Go, [Herdr](https://herdr.dev), and [treehouse](https://github.com/kunchenguid/treehouse).
`cfo doctor` reports exactly what is missing and how to install it.

```powershell
git clone https://github.com/fpresta0607/code-goblins
cd code-goblins
powershell -NoProfile -ExecutionPolicy Bypass -File install.ps1
```

`install.ps1` builds (or downloads) `cfo.exe` and verifies the toolchain; the Claude Code hooks in `.claude/settings.json` are already wired to it.

### Daily flow

```sh
cd c:/dev/code-goblins
herdr
```

Herdr is your cockpit. Launch the CFO in a pane, then ask away:

```sh
claude    # or: codex, pi
```

The CFO reads its contract and does the rest - goblins appear as Herdr tabs (`fm-<id>`), each in a clean treehouse worktree.
`cfo spawn` targets the Herdr session named `default` (or `$HERDR_SESSION`), so keep the CFO and its goblins in the same session.

## Cut from v1

These upstream features are not yet ported to the Go binary:

- Grok and OpenCode harness adapters
- tmux, zellij, Orca, and cmux session backends
- Secondmates (persistent and remote)
- Relay (public X / Discord mentions)
- AFK mode

## Repo layout

- `cmd/cfo/` - the `cfo.exe` entry point and command handlers.
- `internal/` - one package per subsystem (herdr, treehouse, spawn, fleet, monitor, wake, lock, state, home, watch, harness, axi, execx, fsx, claudehook, digest, doctor, guard, crewstate, supervise, proc).
- `docs/superpowers/` - the design spec and implementation plans.
- `tests/acceptance/` - the opt-in real-session Windows acceptance script.
- `AGENTS.md` - the CFO's operating contract; `CLAUDE.md` points to it.
- `bin/`, `tests/*.test.sh`, `skills/`, `.agents/` - upstream firstmate reference material for the port; not executed by the Go binary.

## Development

```sh
go vet ./...
go test ./...
go build ./cmd/cfo
```

CI runs on `windows-latest` and gates `go vet`, `go test ./... -count=1`, and `go build`.
Unit tests use fakes for subprocess, Herdr, and pane behavior.
The real-session acceptance suite (`tests/acceptance/plan3_windows.ps1`) needs real Herdr, treehouse, and harness binaries and refuses to run without `CFO_PLAN3_REAL=1`.

## Contributing

Contributions are welcome - see [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT - see [LICENSE](LICENSE).
Derived from [firstmate](https://github.com/kunchenguid/firstmate), also MIT-licensed.
