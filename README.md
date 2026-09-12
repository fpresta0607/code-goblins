<h1 align="center">Code Goblins</h1>

<p align="center"><strong>Talk to one agent. Ship with a crew.</strong></p>

<p align="center">
  A Windows-native control plane for autonomous coding agents.<br/>
  One CFO coordinates Claude Code, Codex, Pi, and Kimi workers in isolated git worktrees, supervises them to completion, validates the result, and hands you finished work.
</p>

<p align="center">
  <img alt="Windows" src="https://img.shields.io/badge/platform-Windows-blue?style=flat-square" />
  <img alt="Go" src="https://img.shields.io/badge/core-Go-00ADD8?style=flat-square" />
  <img alt="License" src="https://img.shields.io/badge/license-MIT-green?style=flat-square" />
</p>

## Why Code Goblins

Most coding-agent tools make you manage more agents. Code Goblins is built to do the opposite.

You talk to one supervisor: the **CFO**. The CFO decomposes the objective, dispatches specialized **goblins** in parallel, gives each worker an isolated git worktree, watches for failures and blocked work, switches harnesses when necessary, runs the delivery pipeline, and brings decisions back to you only when human judgment is actually required.

The goal is not maximum agent count. The goal is **minimum human intervention per production-ready change**.

```text
                              YOU
                               │
                        one conversation
                               │
                               ▼
                     ┌──────────────────┐
                     │       CFO        │
                     │ plan · dispatch  │
                     │ supervise · ship │
                     └────────┬─────────┘
                              │
              ┌───────────────┼───────────────┐
              ▼               ▼               ▼
        ┌───────────┐   ┌───────────┐   ┌───────────┐
        │ Goblin A  │   │ Goblin B  │   │ Goblin C  │
        │ worktree  │   │ worktree  │   │ worktree  │
        │ Claude    │   │ Codex     │   │ Pi / Kimi │
        └─────┬─────┘   └─────┬─────┘   └─────┬─────┘
              └───────────────┼───────────────┘
                              ▼
                  review → test → lint → CI
                              │
                              ▼
                     PR / local delivery
```

## What makes it different

### One supervisor, not a wall of terminals

The CFO is the only human-facing control plane. Goblins report outcomes, questions, and failures into a durable wake queue; the CFO supervises and steers them without requiring you to poll every terminal.

### Native Windows orchestration

The fleet core is a compiled Go binary (`cfo.exe`). Goblins run as real Windows sessions in [Herdr](https://herdr.dev), avoiding a shell-script orchestration layer on the hot path.

### Isolated work by default

Every goblin receives its own in-repository git worktree at `<project>/.worktrees/gb-<id>`. Parallel workers do not edit the same checkout, and cleanup refuses to destroy unlanded work.

### Harness-agnostic workers

A task can run through Claude Code, Codex, Pi, or Kimi. `cfo switch` can change the harness, model, or effort level in-place while retaining the task identity, pane, worktree, and a handoff when native session resumption is unavailable.

### Restart-proof supervision

Task metadata, fleet state, and wake events live on disk. Closing the supervisor does not erase what the fleet was doing.

### Production-oriented gates

The `no-mistakes` path owns review, bounded repair cycles, tests, lint, documentation, push, PR creation, and CI. Review budgets are frozen per task so changing global policy cannot silently weaken an in-flight job.

The production-proof layer is intentionally fail-closed: delivery evidence must come from machine-readable PR state and terminal checks rather than a worker merely claiming that the task is finished.

### Project-scoped credentials

Projects declare the services they need. `cfo auth` probes them before dispatch, validates project identity where configured, and keeps credentials namespaced outside repositories. A blocking authentication failure prevents normal dispatch rather than stranding a worker halfway through a task.

### Recovery instead of babysitting

`cfo watch`, hooks, `cfo reap`, durable wake events, harness health, and explicit task states are designed around unattended operation. The system detects work that needs intervention and wakes the CFO instead of making the user stare at terminals.

## Quick start

Code Goblins is a standalone repository. Clone this repository directly; no upstream checkout or synchronization step is required.

```powershell
git clone https://github.com/fpresta0607/code-goblins.git
cd code-goblins
powershell -NoProfile -ExecutionPolicy Bypass -File install.ps1 -Bootstrap
cfo install
cfo doctor
```

`install.ps1 -Bootstrap` installs or builds the Code Goblins binaries and scriptable dependencies. `cfo install` wires the CFO into your user environment so a supervisor opened from another project can still manage the fleet.

Then open the project you actually want to build:

```powershell
cd C:\dev\my-project
herdr
claude   # or codex / pi / kimi for the CFO session
```

Tell the CFO what outcome you want. It handles the fleet mechanics.

## Typical autonomous delivery loop

```text
1. User gives the CFO an objective and constraints.
2. CFO resolves the project and writes explicit acceptance criteria.
3. cfo auth preflights required project services.
4. CFO spawns one or more goblins into isolated worktrees.
5. Goblins implement, investigate, test, and report through the wake queue.
6. CFO steers blocked work or switches harnesses when useful.
7. no-mistakes performs bounded independent review and repair.
8. Tests, lint, documentation and CI produce machine evidence.
9. CFO presents the finished outcome or the smallest unresolved decision.
10. Approved work is merged; unlanded work is never silently destroyed.
```

## Core commands

```text
cfo doctor
cfo auth <project> [--check|--fix] [--env]
cfo brief <id> --project <path> [--kind <ship|scout>] [--mode <mode>]
cfo spawn <id> --project <path> --brief <path> --harness <claude|codex|pi|kimi> [--mode <mode>] [--model <model>] [--effort <level>] [--class <class>] [--yolo]
cfo switch <id> [--harness <h>] [--model <m>] [--effort <e>]
cfo send <target> <text...>
cfo peek <target> [lines]
cfo fleet-view [--json]
cfo pipeline run <id> --intent <text>
cfo pipeline respond <id> --action <fix|approve> [--findings <ids>] [--instructions <text>]
cfo pr check <id> <url>
cfo pr merge <url> [--method <merge|squash|rebase>] [--delete-branch]
cfo cleanup <id>
cfo reap [--dry-run|--apply]
cfo drain
```

Run `cfo doctor` after installation for the current dependency and harness health report.

## Safety model

Code Goblins is designed for high autonomy without pretending that an LLM saying “done” is proof.

- Work happens in isolated worktrees.
- Authentication is checked before normal dispatch.
- Review/repair budgets are explicit and bounded.
- Pipeline approval fails closed when actionable findings remain.
- Dirty or unlanded work is not silently deleted.
- Local delivery is fast-forward only.
- PR delivery is expected to be backed by machine-readable CI evidence.
- Human approval remains the default for merges; `yolo` is an explicit posture, not an implicit permission.

For high-risk production systems, use repository branch protection and keep production deployment credentials outside worker reach. Code Goblins coordinates software delivery; it is not an operating-system sandbox.

## Architecture

The core is intentionally local-first:

- `cmd/cfo/` — the Windows-native fleet CLI and control plane.
- `internal/spawn/` — task dispatch and worktree preparation.
- `internal/herdr/` — terminal/session integration.
- `internal/fleet/` — fleet truth, targeting, steering and inspection.
- `internal/supervise/` / `internal/watch/` — unattended supervision and recovery.
- `internal/pipeline/` — durable validation policy and decision gates.
- `internal/auth/` — project-scoped credential preflight and injection.
- `internal/state/` / `internal/wake/` — restart-proof task and event state.
- `cmd/showcase-axi/` — repository-owned review surface.
- `.agents/skills/` — reusable capabilities exposed to the supported harnesses.

The control plane is local. Your coding harnesses may still call their model providers according to their own configuration.

## Development

```powershell
go vet ./...
go test ./... -count=1
go build ./cmd/cfo
go build ./cmd/showcase-axi
```

CI runs on `windows-latest`. The real-session acceptance suite is opt-in because it requires actual Herdr and harness installations.

## Project lineage

Code Goblins began from ideas and code in [First Mate](https://github.com/kunchenguid/firstmate), which is MIT licensed. That lineage is retained and credited under the license.

Code Goblins is now maintained as an **independent standalone project** with its own Windows-native Go control plane, supervision model, credential system, harness switching, durable state, delivery pipeline, review surface, and roadmap. You clone and update Code Goblins from this repository directly; First Mate is not an upstream dependency that users need to track or sync.

## Contributing

Issues and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).

If you are working on orchestration, the standard is simple: features should reduce human intervention **without weakening evidence that the delivered change is correct**.

## License

MIT. See [LICENSE](LICENSE). First Mate lineage remains acknowledged as required by its MIT license.
