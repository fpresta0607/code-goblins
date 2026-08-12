# firstmate-native: Windows-native First Mate fork

Date: 2026-08-12
Status: approved

## Goal

Reimplement First Mate's core fleet loop as a Windows-native project so it runs at native speed on Windows, replacing the bash script layer entirely.
Upstream First Mate is ~147,000 lines of bash across 280 scripts and is slow on Windows because Git Bash emulates `fork()`.
Measured on the target machine: ~28ms per subprocess spawn and ~18ms per command substitution, versus under 1ms on Linux.
The scripts spawn `jq`, `grep`, and `git` hundreds of times per operation, so every operation is 20-50x slower than on Linux.
The fix is architectural, not a line-by-line translation: collapse the script layer into one compiled binary that does JSON, string, and file work in-process.

## Decisions record

- Goal: full Windows-native fork as its own project, accepting divergence from upstream.
- Core technology: a single compiled `fm.exe`.
- Language: Go.
- Harness support in v1: Claude Code only.
- Session backend in v1: Herdr (Windows preview).
- Feature scope in v1: core fleet only, meaning spawn, supervise, steer, deliver via PR or local merge, backlog, wake queue, and restart-proof state.

## 1. Repo and upstream relationship

The project is a GitHub fork of `kunchenguid/firstmate` named `fpresta0607/firstmate-native`, cloned locally to `C:\dev\firstmate-native`.
The existing `C:\dev\firstmate` clone stays untouched as the upstream reference installation.
Upstream is kept as the `upstream` git remote so behavior and future fixes remain diffable.
As each subsystem comes online in Go, its bash scripts are deleted from the tree; git history preserves them for reference.
`AGENTS.md`, the bundled skills, and docs are adapted in place because the instruction layer stays text.
Upstream's LICENSE is retained with fork attribution.

## 2. fm.exe architecture

One pure-Go binary with no cgo, built with `go build ./cmd/fm` on any Windows machine with the Go toolchain.
Subcommands replace scripts one-to-one where the concept survives: `fm spawn`, `fm watch`, `fm drain`, `fm send`, `fm peek`, `fm fleet-view`, `fm pr check|merge|poll`, `fm merge-local`, `fm brief`, `fm session-start`, and an `fm hook ...` family for the harness contract.
Internal packages: `state`, `lock`, `wake`, `spawn`, `watch`, `backend/herdr`, `claudehook`, `gitops`.
All JSON, string, and file work happens in-process.
The only child processes ever spawned are `git`, `gh`, `herdr`, and `claude`.
The watcher (`fm watch`) is a single resident process using filesystem notifications and timers instead of poll-sleep loops, with zero idle CPU and instant wake.
Performance targets: any hook invocation under 50ms, session-start digest under 1s.

## 3. State model

On-disk shapes match upstream wherever `AGENTS.md` references them: `state/*.meta`, `state/*.status`, `data/backlog.md`, `data/projects.md`, `data/captain.md`, `data/learnings.md`, and the durable wake queue.
This keeps upstream docs and the supervision mental model valid.
Two deliberate Windows-native replacements:

- The MSYS symlink-based session lock becomes an atomic create-exclusive lock file with PID-plus-creation-time identity.
  This removes the MSYS lock fragility that produces spurious read-only sessions (such as "cannot locate harness process in ancestry").
- `/proc`-based process custody becomes Windows API process queries.

## 4. Claude Code integration

`.claude/settings.json` rewires the existing hook wiring, four events carrying six hook commands, to the binary:

- SessionStart runs `fm hook session-start`.
- PreToolUse (Bash matcher) runs `fm hook pretool-arm` and `fm hook pretool-cd`.
- PreToolUse (all tools) runs `fm hook pretool-subagent`.
- Stop runs `fm hook turnend-guard`, then `fm hook stop-autoarm` with `asyncRewake` preserved.

`fm` reads the hook JSON on stdin and honors Claude Code's exit-code contract, where exit 2 means block or rewake.
The Stop-hook-owned watcher continuity model ports over unchanged in behavior.

## 5. Herdr backend

Crewmates run in Herdr: one workspace per home, one tab per crewmate.
Steering and peeking use `pane send-text` and `pane read`.
Event waits use `pane wait-output` with a polling fallback, the same degraded path upstream's Windows CI spike already defines.
Herdr is driven via its CLI with `--json` output parsed in-process.
Worktrees are plain `git worktree` managed by fm directly; the treehouse dependency is dropped.

## 6. Error handling and testing

Go table-driven unit tests per package for the logic that matters: state parsing, lock lifecycle, wake queue, spawn decisions, and hook exit codes.
The port carries upstream's test intent, not its ~100 bash test files.
One opt-in end-to-end smoke test requires real `herdr` and `claude` installs.
CI runs on `windows-latest` with `go vet` and `go test`.
Errors fail loud with structured logs under `state/`; no silent fallbacks.

## 7. Explicitly cut from v1

- Grok, Pi, Codex, and OpenCode harness adapters.
- tmux, zellij, cmux, and Orca backends.
- Secondmates, local and remote.
- Relay (X and Discord).
- AFK mode.
- treehouse.
- shellcheck tooling and the bash test suite.

The architecture leaves room for these later: backends and harness adapters are single packages behind small interfaces.
Nothing speculative gets built in v1.
