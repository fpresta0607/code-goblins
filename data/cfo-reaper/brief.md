# Brief: cfo must reap its own orphans

Build orphan detection and reaping into the `cfo` binary. Today the fleet leaks processes, panes and worktrees, and nothing notices until a human asks. That is the defect.

Do not use em dashes anywhere, in code, comments, docs or output.

## The evidence that motivated this, gathered 2026-09-05

A manual audit of a live machine found all of the following, none of it visible to `cfo` or to `herdr agent list`:

1. **A live, unsupervised goblin.** PID 31032, `claude.exe --dangerously-skip-permissions --strict-mcp-config` (cfo's own launch flags), parent a PowerShell carrying herdr's prompt shim, started 07:14:51, 553 seconds of CPU, 445 MB resident. `herdr pane list` reported only two panes and this was not one of them. Its pane was destroyed; the harness process outlived it. No `state/*.meta`, no supervision, no wake records, and it is still burning resources.
2. **Four stale dev servers** in worktrees whose goblins finished: two node processes under `C:\dev\PocketPiggies\.worktrees\gb-pp-money-summary-period`, one under `...\precisiondocs\.worktrees\gb-pdocs-interview-echo`, one under `...\gb-pdocs-interview-echo-r2`.
3. **Six orphaned worktrees** with no herdr pane and no task meta: `gb-pdocs-fly-rightsize`, `gb-pdocs-help-docs`, `gb-pdocs-interview-echo`, `gb-pdocs-interview-echo-r2`, `gb-pdocs-utah-bootstrap-stall`, `verify-merged-main`.

The Overlord's framing, which is the requirement: "CFO needs to oversee and complete and actually kill CPU processes that are no longer running or stale, or the panes have been closed. If we have no monitoring or wait for a human to visualize what's running, that's a problem."

## What to build

**`cfo reap`**, plus the audit that backs it.

**1. `cfo reap --dry-run` (and make dry-run the default).** Enumerate and classify every fleet resource, and print what it would do and why. It must cross-reference four sources, because no single one sees everything:
- `state/*.meta` - tasks cfo believes exist, and their terminal status
- `herdr pane list` / `agent list` - panes and registered agents
- the operating system process table - harness processes by launch signature and by foreground process group, the same technique `internal/herdr.HarnessRunning` already uses (see `pane.process_info`, comparing `foreground_process_group_id` to `shell_pid`)
- the worktree directories under `.worktrees/` in the project and each `projects/*/`

**2. The classifications**, each needing a different action:
- `orphan_process`: a harness process whose pane no longer exists. This is the dangerous one. It is invisible, unsupervised and may still be spending tokens.
- `stale_server`: a long-lived child (a `next dev`, a vite server) rooted in a worktree whose task reached a terminal status.
- `orphan_worktree`: a worktree directory with no live pane and no non-terminal meta.
- `orphan_meta`: a `state/*.meta` with no pane and no process.
- `orphan_status`: a `*.status` log with no matching meta. The session digest already reports these, so reuse that logic rather than duplicating it.

**3. Reaping, gated by safety.** `cfo reap --apply` acts. The gate is the hard part and it is where the correctness lives:
- **Never kill a process that might be working.** Sample CPU over an interval and require it to be genuinely idle, or require the operator to pass `--force` naming the specific PID. A goblin waiting on an API response looks idle by log age but is not by CPU delta. Read `internal/monitor` first; the fleet already learned this lesson.
- **Never reap a worktree with uncommitted or unpushed work.** Check both. A goblin's unpushed branch is the whole product of its run.
- **Never reap anything whose meta is non-terminal**, unless `--force`.
- Prefer the existing `cfo cleanup` path for worktrees rather than a second removal implementation.
- Everything reaped gets a line in the status log saying what and why.

**4. Make it automatic, which is the actual ask.** A detected orphan must reach the CFO without a human asking. Wire the audit into the existing supervision surface: the session-start digest already prints fleet state, and `hooks/gate-watch.py` already polls herdr on a timer. Report orphans there. Do not add a new daemon; use what exists.

**5. Tests.** Table-driven classification tests over synthetic fixtures for each of the five classes, plus tests proving the safety gates refuse: a busy process, a dirty worktree, an unpushed branch, a non-terminal meta.

## Constraints

- Match the existing package layout and style. `internal/cleanup`, `internal/state`, `internal/herdr` and `internal/monitor` already hold most of the primitives; compose them rather than duplicating.
- `internal/herdr.HarnessRunning` and `ReportAgent` landed today in PR #31 and are directly relevant: the first is how you detect a live harness without a name table, the second is how a pane gets registered when herdr's detection manifest cannot see it.
- Killing processes is destructive and irreversible. Default to reporting. Make the operator opt in to acting.

## Deliverable

Write your report to `C:\dev\code-goblins\data\cfo-reaper\report.md` in the **primary repo**, not inside your worktree, and commit and push it on your branch. A prior scout wrote its report inside its worktree and `cfo cleanup` destroyed it.

Push only to `fpresta0607/code-goblins`. Confirm the target repo is one the Overlord owns before opening a PR.

Report done with `cfo notify cfo-reaper --done --pr <url>`.
