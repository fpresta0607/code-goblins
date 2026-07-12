# Persistent-secondmate self-supervision

This is the authoritative contract for how a persistent secondmate keeps supervising its own delegated children while its model session is idle, without the captain being present and without away mode (`state/.afk`).
The mechanism reuses the away-mode daemon; only the differences live here.

## The gap this closes

A persistent secondmate's supervision liveness used to be coupled to its harness's model-turn loop.
On Claude the secondmate arms `bin/fm-watch-arm.sh` as a background task; the watcher blocks until an actionable wake and then exits, and the secondmate is supposed to take a new turn to drain the wake and re-arm (`docs/supervision-protocols/claude.md`).
That next turn only happens if something re-invokes the model.
For the captain's primary that is the captain typing; for an idle, unattended secondmate there is nothing, and Claude Code does not autonomously start a model turn when an idle session's background task completes.
So when the armed watcher exited on an actionable wake, supervision died silently and finished children sat unattended.
The turn-end guard (`bin/fm-turnend-guard.sh`) cannot cover this: it is a point-in-time check that correctly allows the turn while the watcher is still live, and it is inert in secondmate homes anyway.

The fix makes supervision a real OS process whose liveness does not depend on the model taking turns.

## Contract

Self-supervise mode is the away-mode daemon (`bin/fm-supervise-daemon.sh`) pointed at the secondmate's OWN pane and gated on `state/.self-supervise` instead of `state/.afk`.

- **Owner of the daemon terminal lifecycle:** `bin/fm-afk-launch.sh` - the same single owner as away mode.
  `start-self-supervise` captures the secondmate's own pane as the supervisor target, writes `state/.self-supervise` (never `state/.afk`), and launches the daemon in a non-visible tracked terminal (a herdr `--no-focus` tab or a detached tmux session) recorded by exact id.
  `stop-self-supervise` is the mirror exit; `reconcile` closes a recorded-but-dead terminal by exact id after a crash.
  It is idempotent: a live daemon just refreshes the flag.
- **Owner of the supervision loop:** `bin/fm-supervise-daemon.sh` - it owns the watcher and re-arms it indefinitely (the `while true` loop), classifies wakes, and on an actionable wake injects a marked (`FM_INJECT_MARK`) resume into the secondmate's own pane.
  Both the launcher record and the daemon are strict per-home singletons.
- **Injection gate:** `inject_msg` injects when away mode OR self-supervise mode is active (`{ afk_active || self_supervise_active; }`).
  In self-supervise mode there is no captain-return exit; the secondmate always treats a marked injection as an internal supervise-resume, runs `bin/fm-wake-drain.sh` on the durable, lossless `state/.wake-queue`, and advances its children on its own turn.
- **The daemon owns the watcher.** In self-supervise mode the secondmate does NOT separately arm `bin/fm-watch-arm.sh`, exactly as under `state/.afk` (AGENTS.md section 8).
- **Flag decoupling.** The daemon entry `bin/fm-afk-start.sh` and `bin/fm-afk-launch.sh` write/check `$FM_SUPERVISE_FLAG` (default `.afk`); `start-self-supervise` sets it to `.self-supervise`.
  This keeps the secondmate's own session from ever seeing `state/.afk` (which would make it think the captain is away).

## Lifecycle

- **Start on dispatch.** The secondmate ensures the daemon is running (idempotent `start-self-supervise`) whenever it has in-flight children.
  Because this runs on the dispatch turn - while the secondmate is active - there is no idle-gap dependency: the daemon is up before the secondmate goes idle, and then supervises independently.
- **Self-exit when idle.** With self-supervise active and away mode NOT active, the daemon self-exits cleanly after `FM_SELF_SUPERVISE_IDLE_EXIT_SECS` (default 180s) of zero in-flight work, so an empty-queue secondmate costs nothing.
  Its next dispatch brings it back.
  This never applies in away mode, where the daemon must persist while the captain is out even with no work in flight.
- **Crash recovery.** The durable `state/.wake-queue` preserves every child event across a daemon gap.
  A crashed daemon is re-established by the idempotent start-on-dispatch and by session-start recovery: `bin/fm-afk-launch.sh reconcile` closes any leaked terminal by exact id, and the secondmate re-runs `start-self-supervise`.
  If the secondmate model session restarts, the daemon keeps the watcher armed and keeps injecting; the returning session reconciles its own children and idles while the daemon continues poking it.

## Approval authority is unchanged

The daemon only owns the watcher loop and injects a resume poke into the secondmate's own pane.
It never runs `bin/fm-pr-merge.sh`, `bin/fm-merge-local.sh`, `bin/fm-teardown.sh`, or any state-changing project/GitHub command.
Every merge, teardown, and every destructive / irreversible / security-sensitive decision is made by the secondmate model on its own turn, and the secondmate still escalates captain-owned decisions to the main firstmate's status file per its charter.
This is the same invariant away mode already holds ("Afk never changes approval authority", AGENTS.md section 8).

## Empirical validation

**2026-07-12.** Validated on tmux 3.6a via `tests/fm-self-supervision-e2e.test.sh` on a dedicated private tmux socket (`tmux -L fm-selfsup-e2e-<pid>`); it never touches the live fleet or any herdr session.
ShellCheck 0.11.0 clean (`bin/fm-lint.sh`).

Command run:

```
bash tests/fm-self-supervision-e2e.test.sh
```

Observed output:

```
ok - Scenario A: self-supervise daemon autonomously wakes the secondmate's own pane (no captain, no .afk) and stays live for the next event
ok - Scenario B: an empty-queue self-supervise daemon self-exits cleanly
all self-supervision e2e tests passed
```

Scenario A asserts the incident's exact shape now recovers: with `state/.self-supervise` present and `state/.afk` absent throughout, an in-flight child writes `done` after the secondmate's own pane is idle; the daemon injects a sentinel-prefixed resume into that pane with no captain and no away mode; a second child event (`blocked`) produces a second injection, proving supervision stays live and re-arms rather than firing once; and the daemon never mutates the child's own meta/status (no approval-authority expansion).
Scenario B asserts an empty-queue self-supervise daemon self-exits after the idle grace and logs `self-supervise idle exit`.

The root-cause reproduction of the stall itself (the pre-fix failure, in an isolated throwaway home plus a non-`default` herdr lab, `default` byte-identical before/after) is recorded in the promotion scout report `data/secondmate-autonomous-supervision-s7/report.md`.
