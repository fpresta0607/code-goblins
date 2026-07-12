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

## Lifecycle (code-level, no model action)

Autonomous supervision must not depend on the secondmate model remembering to start the daemon, so every lifecycle transition is wired in code.

- **Start on dispatch.** `bin/fm-spawn.sh` calls `bin/fm-afk-launch.sh ensure-self-supervise` automatically at the end of every child (ship/scout) dispatch in a secondmate home (detected by the `.fm-secondmate-home` marker).
  It captures the secondmate's OWN pane at the top of the spawn, while the process's pane env is still pristine - the herdr/tmux pane vars get reassigned to the CHILD's pane during backend provisioning, so it cannot be re-derived at the end - and passes it explicitly as `FM_SUPERVISOR_TARGET`/`FM_SUPERVISOR_BACKEND`.
  Best-effort: the child is already spawned, so a daemon-start hiccup never fails the dispatch.
- **Recorded supervisor target.** At `--secondmate` spawn, `bin/fm-spawn.sh` writes `<home>/state/.self-supervise-target` = `<backend>\t<target>` (the secondmate's own pane), refreshed on every respawn.
  This is the authoritative, env-independent record the reconcile path reads, since that path does not run in the secondmate's pane.
- **Reconcile at session start.** `bin/fm-bootstrap.sh`'s secondmate-liveness sweep calls `ensure-self-supervise` for every live secondmate, passing the secondmate's OWN recorded pane/backend from its meta - never the sweep's own captain-pane env.
  A live secondmate whose daemon died has it restarted (`bin/fm-afk-launch.sh reconcile` closes any leaked terminal by exact id, then a fresh start); a respawned secondmate gets its daemon on its first child dispatch.
- **`ensure-self-supervise`** is the single idempotent entry both callers use: it starts the daemon only when the home has in-flight child work, resolves the pane from an explicit `FM_SUPERVISOR_TARGET`/`FM_SUPERVISOR_BACKEND` or the recorded target (never this process's env), refreshes the record, and no-ops when the daemon is already live.
- **Self-exit when idle.** With self-supervise active and away mode NOT active, the daemon self-exits cleanly after `FM_SELF_SUPERVISE_IDLE_EXIT_SECS` (default 180s) of zero in-flight work, so an empty-queue secondmate costs nothing.
  The next dispatch idempotently brings it back.
  This never applies in away mode, where the daemon must persist while the captain is out even with no work in flight.
- **Crash recovery.** The durable `state/.wake-queue` preserves every child event across a daemon gap.
  A crashed daemon is re-established by the idempotent start-on-dispatch and by the session-start reconcile sweep above; if the secondmate model session restarts, the daemon keeps the watcher armed and keeps injecting while the returning session reconciles its own children and idles.

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

The code-level auto-wire is proven end-to-end by `tests/fm-self-supervision-autowire-e2e.test.sh`, which drives the real `ensure-self-supervise` path (including detached daemon-terminal creation) on a private tmux socket:

```
bash tests/fm-self-supervision-autowire-e2e.test.sh
```

Observed output:

```
ok - A: dispatch auto-start -> autonomous wake -> stays live -> crash reconcile restart -> idle self-exit, all with no model action and no .afk
ok - B: two secondmate homes run isolated per-home daemons; neither touches the other's pane or state
all self-supervision auto-wire e2e tests passed
```

Scenario A drives the whole lifecycle with NO model action: the exact `ensure-self-supervise` call `bin/fm-spawn.sh` makes auto-starts the daemon targeting the secondmate's OWN pane (the recorded target is asserted to be that pane, not a captain/other pane); a child `done` while idle injects only into that pane; a second event injects again; the daemon is `kill -9`'d (crash) and a reconcile `ensure-self-supervise` restarts a fresh daemon whose injection resumes; and removing the child work makes the daemon self-exit.
Throughout it asserts `state/.afk` is never created and the child meta is never mutated.
Scenario B runs two isolated secondmate homes and asserts per-home singleton daemons: a child event in one home injects only into that home's pane, and stopping one home's daemon leaves the other's live and still injecting.

Per-backend pane-context is covered on both backends.
The tmux full lifecycle above proves it for tmux.
For herdr, `tests/fm-self-supervision-herdr-panectx.test.sh` provisions a non-`default` `fm-lab-*` session via `bin/fm-herdr-lab.sh` and asserts that `ensure-self-supervise`, given the secondmate's own herdr pane, records the correct `herdr\t<session>:<pane>` target and creates the daemon terminal in that secondmate's own lab session - never the live `default` - with `state/.afk` never set and the default fleet-state tripwire byte-identical before and after.
The herdr injection transport it builds on is covered by `tests/fm-afk-inject-herdr-e2e.test.sh`, and the `<session>:<pane>` pane capture by `tests/fm-daemon.test.sh`.

The root-cause reproduction of the stall itself (the pre-fix failure, in an isolated throwaway home plus a non-`default` herdr lab, `default` byte-identical before/after) is recorded in the promotion scout report `data/secondmate-autonomous-supervision-s7/report.md`.
