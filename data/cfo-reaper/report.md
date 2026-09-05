# cfo reap: the fleet reaps its own orphans

Built `cfo reap`, the audit behind it, the safety gates that make acting on it survivable, and the two wires that make an orphan reach the CFO without a human asking.

## The defect, restated

Nothing in the fleet owned the question "what is still running that nobody is supervising".
`cfo fleet-view` reads task metadata, `herdr agent list` reads panes, and the operating system knows about processes, and no code joined the three.
A pane could be destroyed while its harness kept running, and the harness would then be invisible to every CFO surface while still holding memory and still able to spend tokens.
That is what PID 31032 was on 2026-09-05: 553 seconds of CPU, 445 MB resident, no `state/*.meta`, no wake records, no row in any listing.

## What was built

### `cfo reap` (default `--dry-run`)

One sweep cross-references four sources, because none of them sees everything:

| Source | What only it knows |
| --- | --- |
| `state/*.meta` plus each `state/*.status` | which tasks cfo believes exist, and whether they reported a terminal status |
| `herdr api snapshot` plus `herdr pane process-info` | which panes exist, which have a registered agent, and the shell pid and foreground process group behind each one |
| the Windows process table (one `Win32_Process` CIM query) | what is actually running, with parent links, start times and command lines |
| `.worktrees/` under the home, under `projects/*/`, and under every project a task record names | which worktree directories still exist on disk |

The pane process identity is read exactly the way `internal/herdr.HarnessRunning` already does it.
`HarnessRunning` was refactored to call a new exported `PaneProcessInfo` rather than duplicating the request, because the sweep needs the pids themselves and not the bool that technique reduces to.

### The five classifications

- `orphan_process`: a harness process whose ancestry reaches no live pane's shell and no live pane's foreground group. The dangerous one.
- `stale_server`: a long-lived module (`next`, `vite`, `webpack`, `nodemon`, npm/pnpm/yarn, vitest, jest) whose command line names a worktree whose task has finished or has no record at all.
- `orphan_worktree`: a worktree directory with no pane holding a live agent behind it.
- `orphan_meta`: a `state/<id>.meta` with no pane, no attributable process, and no worktree left on disk. Bounded that way deliberately, so a meta whose worktree still exists is reported once as `orphan_worktree` rather than twice.
- `orphan_status`: a `*.status` log with no matching meta. The digest's own scan was extracted into `state.ScanIDs` and both surfaces now call it, so the two cannot drift.

Classification is a pure function over a plain `Inventory` value.
That is what makes the table-driven tests possible: a test cannot conjure real processes, so the evidence is synthetic and the logic under test is the real one.

### The gates, which is where the correctness lives

Every refusal is recorded on the finding as a `HELD:` line rather than dropping it, so the report always says both what was found and why it was left alone.

1. **Never kill a process that might be working.**
   Processor time is sampled twice, three seconds apart, and anything that burned more than 100ms in that window is refused.
   Log age is not evidence and is never consulted: a goblin waiting on an API response writes nothing for minutes and is very much alive, while a goblin mid-turn burns processor time continuously.
   A process whose processor time cannot be read at all is also held, because "I could not tell" is not "it is idle".
2. **Never kill on incomplete evidence.**
   A harness process with no Herdr ancestry may be the Overlord's own editor session rather than a fleet process, and is held.
   If any pane exists but cannot report its process identity, its live harness is missing from the supervised set and would look exactly like an orphan, so every process finding is held while that is true.
   The sweep also excludes its own ancestry, or it would report the session it is running in.
3. **Never reap a worktree with uncommitted or unpushed work.**
   Both are checked: `git status --porcelain=v1 --untracked-files=all` and `git log --oneline HEAD --not --remotes`.
   This gate is the one thing `--force` cannot clear.
4. **Never reap a task that has not reached a terminal status**, unless `--force` names its id.

`--force` takes a value, always: one pid or one task id, repeatable.
There is no blanket override, so "force" always names the specific thing the operator accepts responsibility for.

### The actions

Worktree removal goes through the existing `cfo cleanup` path when a task record exists (tab close, worktree return, record archive, all already guarded), and through the same `worktree.Service.Return` primitive cleanup itself calls when the directory has no record behind it.
There is no second removal implementation.
Processes are ended with `taskkill /T /F`, because a dev server is usually a shell that spawned the actual server and killing only the parent leaves the port held.
An orphaned status log is moved under `state/archive/`, never deleted: it is the only record of what that goblin reported, and cleanup leaves it behind on purpose.

Everything reaped writes one line naming what and why, into the task's own status log and into a fleet-level `state/.reap.status`.
That log is dot-prefixed so the reaper's own record can never be reported as an orphan by the next sweep.

### Making it automatic

No new daemon. Two existing surfaces carry it:

- **The watcher** (`internal/watch`, hosted in-process by the Stop-owned auto-arm hook) runs the audit on its own timer, at most every `CFO_REAP_EVERY` seconds (default 600), persists the result to `state/.reap-audit.json`, and appends an `orphan` wake the first time a given finding set appears.
  The wake fires on a *change* in the finding set, keyed by a stable digest of it: a leak the CFO already saw and consciously left alone does not re-wake it every cycle, while one new unsupervised harness wakes it immediately.
  A sweep that fails is recorded as a failure, because "cannot see the fleet" and "the fleet is clean" must never render the same.
- **The session-start digest** gained an `ORPHANS` section between `FLEET STATE` and `CONTEXT`.
  It reads the persisted record rather than sweeping, because that package never shells out and composes inside a 1s budget.
  The status-log listing is capped at five with a `(+N more)` line: a long-lived home has hundreds of them, every line identical, and listing them all would bury the one unsupervised harness that actually costs something.

## Verified against the live machine

Built and run against the real `C:\dev\code-goblins` home:

```
ORPHANS: 4 orphan_worktree, 140 orphan_status
  orphan_status agent-display-fixes: status log with no matching meta | would archive the log
  ... (+135 more orphan status logs, all the same shape)
  orphan_worktree pdocs-fly-rightsize   ...\.worktrees\gb-pdocs-fly-rightsize: no live pane, and its task has no metadata record | would return the worktree through cfo cleanup
  orphan_worktree pdocs-help-docs       ...\.worktrees\gb-pdocs-help-docs: no live pane, and its task has no metadata record | would return the worktree through cfo cleanup
  orphan_worktree pdocs-utah-bootstrap-stall ...\.worktrees\gb-pdocs-utah-bootstrap-stall: no live pane, and its task has no metadata record | would return the worktree through cfo cleanup
  orphan_worktree slc-ordinance-sourcing ...\.worktrees\gb-slc-ordinance-sourcing: no live pane, and its task finished (done) | HELD: worktree has 11 commit(s) on no remote; pushing them is the only way this is safe to remove
Nothing was changed. Run cfo reap --apply to act on the findings above.
```

Three of the six orphaned worktrees named in the brief's audit are still there and were found; the other three have since been cleaned up.
The fourth finding is the gate doing its job on real data: `gb-slc-ordinance-sourcing` holds 11 commits that exist on no remote, and the sweep refuses to touch it.
The reaper's own worktree, whose pane is live, is correctly absent.
The `ORPHANS` section renders identically in `cfo session-start`.

The evidence's live orphan process and four stale dev servers no longer exist on the machine, so those two classes could not be confirmed against real processes.
They are covered by the synthetic fixtures instead, modeled directly on the audit: a `claude.exe` under a herdr-descended PowerShell with no pane, and a `node ...\.worktrees\gb-...\node_modules\next\dist\bin\next dev`.

## Tests

- Table-driven classification over synthetic inventories for all five classes, plus the negative cases that matter more than the positive ones: a healthy fleet produces nothing at all, a server in a working task's worktree is left alone, and the sweep never reports the session it runs inside.
- Gate refusals, each proving nothing was killed or removed: a busy process, an unmeasurable process, a dirty worktree, an unpushed branch, a non-terminal task, and an unresolved pane.
- `--force` proven to be per-target: the named pid is killed and the unnamed one beside it is not; and proven not to clear the unpushed-work gate.
- The pid-reuse guard in the ancestry walk, so a recycled pid cannot make an unrelated process read as fleet-supervised.
- A drift test that builds each harness adapter's real launch arguments and asserts the sweep's signatures still appear in them, so a flag renamed in `internal/harness` cannot silently blind the reaper.
- Watcher: a new orphan produces an `orphan` wake and a persisted record; the same orphan on a later sweep produces neither; a further orphan wakes again; a failed sweep is recorded as a failure.
- Digest: the `ORPHANS` section reports a never-swept home, a recorded sweep, and a failed sweep distinctly.
- CLI: dry-run is the default, `--apply` and each `--force` reach the sweep, a failed sweep still records, `--json` emits the typed result.

`go vet ./...` clean, `go test ./...` green.

## Things a reader should push on

- **Stale servers are matched by the worktree path appearing in the command line.**
  That is how `next dev` and `vite` actually launch from a worktree (they run that worktree's own `node_modules` copy), and it is what the brief's evidence showed.
  A server launched purely relative to its working directory carries no path and would be missed.
  Reading another process's working directory needs a PEB walk, which is a lot of unsafe code for a case that has not been observed; the limit is marked with a `ponytail:` comment naming the upgrade path.
- **kimi and pi build no distinctive launch flag**, so they are matched on executable name alone.
  That is weaker than claude's and codex's flag match, which is exactly why a match with no Herdr ancestry behind it is held rather than killed.
- **One CIM subprocess per sweep.**
  The Toolhelp32 snapshot `internal/proc` already takes carries no command line, and the command line is what separates a goblin's dev server from the operator's. That is the cost of seeing it.
- **`orphan_status` is not really a leak.**
  `cfo cleanup` leaves status logs behind deliberately and spawn treats them as history. They are reported because the brief asked for the class and because the digest already listed them, but archiving them is housekeeping, not reclamation, and the listing is capped so they stay out of the way.
