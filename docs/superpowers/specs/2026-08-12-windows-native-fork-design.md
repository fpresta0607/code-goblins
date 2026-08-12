# code-goblins: Windows-native First Mate fork

Date: 2026-08-12
Status: approved, revised same day with the rebrand and the treehouse, AXI, and easy-run requirements

## Goal

Reimplement First Mate's core fleet loop as a Windows-native project so it runs at native speed on Windows, replacing the bash script layer entirely.
Upstream First Mate is ~147,000 lines of bash across 280 scripts and is slow on Windows because Git Bash emulates `fork()`.
Measured on the target machine: ~28ms per subprocess spawn and ~18ms per command substitution, versus under 1ms on Linux.
The scripts spawn `jq`, `grep`, and `git` hundreds of times per operation, so every operation is 20-50x slower than on Linux.
The fix is architectural, not a line-by-line translation: collapse the script layer into one compiled binary that does JSON, string, and file work in-process.

## Naming and roles

The fork is fully rebranded.

- The human is the Supreme Overlord.
- The primary agent the Supreme Overlord talks to is the Chief Fuckaround Officer, CFO for short.
- Spawned worker agents are Code Goblins.

These names replace captain, first mate, and crewmate everywhere: `AGENTS.md`, skills, docs, state file prose, and `cfo.exe` output.
The repo is `code-goblins` and the binary is `cfo.exe`.

## Decisions record

- Goal: full Windows-native fork as its own project, accepting divergence from upstream.
- Core technology: a single compiled `cfo.exe`.
- Language: Go.
- Harness support in v1: Claude Code only.
- Session backend in v1: Herdr (Windows preview).
- Feature scope in v1: core fleet only, meaning spawn, supervise, steer, deliver via PR or local merge, backlog, wake queue, and restart-proof state.
- Naming: repo `code-goblins`, binary `cfo.exe`, roles Supreme Overlord / Chief Fuckaround Officer / Code Goblins.
- Worktrees: treehouse remains the single worktree allocator.
- Ease of running: prebuilt release binary, `install.ps1`, and `cfo doctor`; no Go toolchain required to use it.
- AXI tools: the tasks-axi and quota-axi integrations are ported, and the instruction layer prefers gh-axi for agent-performed GitHub operations.

## 1. Repo and upstream relationship

The project is a GitHub fork of `kunchenguid/firstmate` renamed to `fpresta0607/code-goblins`, cloned locally to `C:\dev\code-goblins`.
The existing `C:\dev\firstmate` clone stays untouched as the upstream reference installation.
Upstream is kept as the `upstream` git remote so behavior and future fixes remain diffable.
As each subsystem comes online in Go, its bash scripts are deleted from the tree; git history preserves them for reference.
`AGENTS.md`, the bundled skills, and docs are adapted in place because the instruction layer stays text.
Upstream's LICENSE is retained with fork attribution.

## 2. cfo.exe architecture

One pure-Go binary with no cgo, built with `go build ./cmd/cfo` on any Windows machine with the Go toolchain.
Subcommands replace scripts one-to-one where the concept survives: `cfo spawn`, `cfo watch`, `cfo drain`, `cfo send`, `cfo peek`, `cfo fleet-view`, `cfo pr check|merge|poll`, `cfo merge-local`, `cfo brief`, `cfo session-start`, `cfo doctor`, and a `cfo hook ...` family for the harness contract.
Internal packages: `state`, `lock`, `wake`, `spawn`, `watch`, `backend/herdr`, `claudehook`, `gitops`.
All JSON, string, and file work happens in-process.
The only child processes ever spawned are `git`, `gh`, `herdr`, `claude`, `treehouse`, and the axi CLIs named in section 6.
The watcher (`cfo watch`) is a single resident process using filesystem notifications and timers instead of poll-sleep loops, with zero idle CPU and instant wake.
Performance targets: any hook invocation under 50ms, session-start digest under 1s.

## 3. State model and Windows file system behavior

On-disk shapes match upstream wherever `AGENTS.md` references them: `state/*.meta`, `state/*.status`, `data/backlog.md`, `data/projects.md`, `data/overlord.md` (upstream's `data/captain.md`), `data/learnings.md`, and the durable wake queue.
This keeps upstream docs and the supervision mental model valid.
Deliberate Windows-native replacements:

- The MSYS symlink-based session lock becomes an atomic create-exclusive lock file with PID-plus-creation-time identity.
  This removes the MSYS lock fragility that produces spurious read-only sessions (such as "cannot locate harness process in ancestry").
- `/proc`-based process custody becomes Windows API process queries.

Windows file system rules apply everywhere:

- No symlinks anywhere; Windows gates symlink creation behind privileges, so the design never depends on them.
- Long-path aware: the binary manifest enables `longPathAware`, and all path handling tolerates paths past 260 characters.
- All state writes are atomic: write to a temp file, then rename, with a bounded retry on sharing violations from antivirus or indexer scans.
- Parsers tolerate CRLF and LF line endings equally.
- Path comparisons are case-insensitive.

## 4. Claude Code integration

`.claude/settings.json` rewires the existing hook wiring, four events carrying six hook commands, to the binary:

- SessionStart runs `cfo hook session-start`.
- PreToolUse (Bash matcher) runs `cfo hook pretool-arm` and `cfo hook pretool-cd`.
- PreToolUse (all tools) runs `cfo hook pretool-subagent`.
- Stop runs `cfo hook turnend-guard`, then `cfo hook stop-autoarm` with `asyncRewake` preserved.

`cfo` reads the hook JSON on stdin and honors Claude Code's exit-code contract, where exit 2 means block or rewake.
The Stop-hook-owned watcher continuity model ports over unchanged in behavior.

## 5. Herdr backend and treehouse worktrees

Code Goblins run in Herdr: one workspace per home, one tab per goblin.
Steering and peeking use `pane send-text` and `pane read`.
Event waits use `pane wait-output` with a polling fallback, the same degraded path upstream's Windows CI spike already defines.
Herdr is driven via its CLI with `--json` output parsed in-process.

Each Code Goblin gets a clean isolated worktree from treehouse, and treehouse is the single worktree allocator; no other component creates worktrees.
`cfo.exe` shells to the treehouse CLI the same way upstream's scripts do.
Verifying treehouse's behavior on Windows is an early implementation milestone; a gap there gets fixed or reported, not silently worked around with a second allocator.

## 6. AXI tool integration

The backlog keeps upstream's tasks-axi integration: `.tasks.toml` stays, and full task bodies remain readable through `tasks-axi show <id> --full`.
Quota-aware dispatch keeps the quota-axi integration.
The instruction layer routes agent-performed GitHub operations through gh-axi, matching the Supreme Overlord's global tooling rules.
`cfo.exe` itself shells to plain `gh` for its own GitHub operations, since the AXI ergonomics target agents, not binaries.

## 7. Easy to run

Using code-goblins must not require a Go toolchain.

- CI builds `cfo.exe` on tags and publishes it as a GitHub Release asset.
- `install.ps1` downloads the release binary, verifies `git`, `gh`, `herdr`, `claude`, and `treehouse`, and wires `.claude/settings.json`.
- `cfo doctor` re-checks the environment on demand and says exactly what is missing and how to install it.
- Quick start is three steps: clone the repo, run `install.ps1`, launch Claude Code in the repo.

The Go toolchain is only needed for hacking on cfo itself.

## 8. Error handling and testing

Go table-driven unit tests per package for the logic that matters: state parsing, lock lifecycle, wake queue, spawn decisions, and hook exit codes.
The port carries upstream's test intent, not its ~100 bash test files.
One opt-in end-to-end smoke test requires real `herdr` and `claude` installs.
CI runs on `windows-latest` with `go vet` and `go test`.
Errors fail loud with structured logs under `state/`; no silent fallbacks.

## 9. Explicitly cut from v1

- Grok, Pi, Codex, and OpenCode harness adapters.
- tmux, zellij, cmux, and Orca backends.
- Secondmates, local and remote.
- Relay (X and Discord).
- AFK mode.
- shellcheck tooling and the bash test suite.

The architecture leaves room for these later: backends and harness adapters are single packages behind small interfaces.
Nothing speculative gets built in v1.
