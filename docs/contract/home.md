## The CFO home

`$CFO_HOME` is the one folder that holds everything the fleet writes: `%LOCALAPPDATA%\CodeGoblins` for every install, from the desktop installer, the one-line install or `cfo install` run in a checkout alike, unless `CFO_HOME` names another folder.
New installations keep the home outside the source checkout; existing checkout homes follow the [legacy checkout activation boundary](ownership.md#legacy-checkout-activation).
The home is local and private by default, and nothing in it is pushed anywhere.
`cfo install` keeps the home, and the Dev Drive folder its worktrees, scratch and caches moved to, open only to the account it runs as, SYSTEM and Administrators, and so does `cfo dev-drive move` for the folder it makes: a folder made under a drive's root, as a home `CFO_HOME` puts there or the Dev Drive folder is, lets Authenticated Users modify it, so any second account on the machine could read and write the fleet's records and every goblin's work.
The first install that narrows a folder walks everything in it, which takes minutes for a folder holding hundreds of thousands of files, and an install that finds it narrowed reads only the folder itself.
A file or folder in it whose access list the install may not change, such as one an elevated program made, keeps its access with everything it holds: the install names the first few, finishes, and leaves `.cfo-narrowing-unfinished` in the folder so the next install narrows it again; the move records the move all the same, says so and exits 1, and the next install or move narrows the folder again.
It is laid out the same way on every machine:

| Path | What it holds |
| --- | --- |
| `bin\` | `cfo.exe` and `goblins.exe`, the same program under two names, and the desktop window, on PATH. Build retention follows the README's build update guidance. |
| `state\` | The fleet's own record, which `cfo` writes and you never edit: task records, status logs, the wake queue and the board's database; `harnesses.json`, where each harness keeps its configuration and skills; `janitor.json`, the janitor's last sweep; `services.json`, the local services stacks `cfo services` holds for tasks; `accepted-start-hooks.json`, the start hooks `cfo start-hook accept` let through; `codex-guard\`, each live task's [codex guard](dispatch.md#pausing-and-automatic-resume); `archive\`, each retired task's text record; and `backups\`. |
| `data\` | Your data, laid out as the next table shows. |
| `config\` | `pipeline.json`, the gate policy, and `fleet.json`, the fleet's settings: `disk_floor_gb` (15, checked on the home's drive and on its Dev Drive), `disk_wake_gb` (10), `caches_cap_gb` (20), `temp_patterns`, the fleet's folders in the machine's temporary folder, `github_owners`, the organizations whose repositories the fleet owns beside the account `gh` works as, and `weekly_floor_percent`, the percent of `claude`'s and `codex`'s weekly allowance the fleet keeps back (5 unless set, read and set with `cfo allowance-floor`). No setting counts goblins: memory alone decides how many run, and `cfo install` and `cfo update` take a `max_live_goblins` an older build left there out of the file and say so, since this build refuses a key it does not read. `dev-drive.json`, which `cfo dev-drive move` writes, names the folder on a Dev Drive that holds the next three rows' folders instead of the home. |
| `worktrees\<project folder>\<task id>\` | Each goblin's git worktree of a checkout, with its extra worktrees, `<task id>-<name>`, beside it. A task an older build spawned keeps the worktree its record names, under its project's `.worktrees`. Whatever removes a goblin's folders, `cfo cleanup`, `cfo worktree remove`, a failed spawn's teardown, the janitor, `cfo reap` or `cfo uninstall`, removes a link in them as a link and never what it points at, and removes a worktree as `cfo cleanup` does. |
| `scratch\<task id>\` | Each goblin's `TEMP`, `TMPDIR` and `GOTMPDIR`: its build output, test homes, logs and whatever its tools put in a temporary folder, which go with the task. |
| `scratch\.tmp\` | Every goblin's `TMP`, the one temporary folder they share and the one nothing removes: Git Bash makes the `TMP` of its first shell `/tmp` for every shell of the user, so it can be no task's folder ([Dispatching](dispatch.md#dispatching)). What asks Windows for the temporary folder lands here, as a Go program's `os.TempDir` does, while a Go test's `t.TempDir()` follows `GOTMPDIR` and stays with its task, and the janitor removes the ones `temp_patterns` names a day after their last write. |
| `caches\` | The package caches every goblin shares, one folder per tool, kept under `caches_cap_gb`. With a Dev Drive, `worktrees\`, `scratch\` and `caches\` are in `CodeGoblins\` at its root for every goblin started since the move, and a goblin started before keeps its folders here until it ends. |

The target for a home is small: the binaries, records under 200 MB, capped caches, and about 0.2 GB for each running goblin, which leaves when its work merges.
`cfo runtime` shows what each bucket holds, and the session digest's `STORAGE:` line says it in one line.
`data/` is your data, laid out the same way in every home:

| Path | What it holds |
| --- | --- |
| `data/backlog.md` | Open work in three sections. `## Queued` is work waiting to start, and `tasks-axi` manages its rows; they are in priority order, top first, so dispatch from the top. The Supreme Overlord reorders them by dragging Tasks on the board, which moves each row with its indented detail lines and changes nothing else in the file; the row of a task that already has a live task record is in progress, not queued, and stays where it is. `## Parked` is work set aside on purpose: nothing lists it as work to start, and moving a row back under Queued revives it. A row parked in place with `(hold-kind: parked)` counts as parked too. `## Done` is finished work: when cleanup retires a delivered task, its row moves there by itself with its detail lines. |
| `data/overlord.md` | The Supreme Overlord's standing directives, word for word. |
| `data/memory/` | The CFO's memory: `MEMORY.md`, its index, and one file per fact (see [Memory](../../AGENTS.md#memory)). |
| `data/memory-archive.md` | Recoverable history governed by the [memory contract](memory.md). |
| `data/memory/startup.json` | Verified warm ranges and cold routes over original source owners, optional memory layers; see [memory](memory.md). No automatic legacy migration. |
| `config/startup-memory-budget`, `config/startup-context-budget` | Estimated-token allowances for combined memory and complete captured startup packages; see [budgets](memory.md#budgets-and-exceptions). |
| `data/routing.json` | The execution lanes, yours to tune. |
| `data/projects/<project>/` | One folder per project, named for its checkout's folder: `auth.json`, `worktree.json`, `project.json` and `services.json`. |
| `data/<task>/` | One folder per task that is queued or under way: `brief.md` (what to do), `report.md` (what a scout found or a ship task delivered), `decisions.md` (the rulings made for it, each with its date) and `deliverables/` (files handed to the Overlord). |
| `data/archive/finished/<task>/` | A finished task's folder. |
| `data/archive/parked/<task>/` | A brief set aside before it was dispatched. |
| `data/archive/filed.md` | Every move filing made, and any pass that failed. |

The watcher files a laid-out home's data every ten minutes, so finished and abandoned work leaves `data/` without anyone asking:

- A finished task's folder moves to `data/archive/finished/<task>/`.
  Finished means no task record is left in `state/`, a status log or a state archive entry shows the task was dispatched, and its brief has not changed since; a brief written again for the same id is a new brief.
- A brief nothing ever dispatched moves to `data/archive/parked/<task>/` once it has sat unchanged for three days, or as soon as a `## Parked` row names it, and gets a `## Parked` row saying where its brief went, so the board stops showing it as Queued.
- A finished folder stays while an open backlog row names it by task id, or while anything still read names a path into it, such as `data/<task>/handoff.md`: all of `backlog.md`, `overlord.md`, both memory folders (`data/memory/` and Claude Code's own), the brief of every live or undispatched task, and every open Command Center question, review item and run card.
  A finished folder kept in place is still read, so what its own `brief.md` and `handoff.md` name by path stays too, and so on.
  A brief with a queued row is queued work however old it is.

To revive a parked brief, move its folder back to `data/<task>/` and its row back under `## Queued`.
A folder a process holds open stays put for the next pass, and the failure is written once in `data/archive/filed.md`.

`cfo install` lays out a new home's `data/`, and repairs a laid-out one by creating what is missing without overwriting any file; `data/.layout` marks a folder it laid out.
A `data/` that already held work before this layout existed is left exactly as it is, because laying it out would move the operator's files.
`cfo home migrate` lays out such a home, and only on its owner's word: see the [command reference](../commands/home/migrate.md).
Keeping `data/` in a private repository of your own, as a backup, is an opt-in and never a requirement: the fleet works the same without one.
`data\.gitignore` keeps binaries, archives and logs out of such a backup: `cfo install` adds its block of patterns once and keeps every line you wrote.

### The janitor

The watcher runs the janitor in the background at most once an hour and keeps its record in `state\janitor.json`, which `cfo runtime`, the session digest and the board read.
Every check that decides a removal refuses when it cannot read what it checks, and it never touches Docker volumes, the Overlord's checkouts, caches outside the home, or uncommitted work; work not on a project's default branch is kept as a local tag, `archive/<branch>`, before its worktree goes.

- A worktree in the home's `worktrees` folder that no task records goes once its work is safe: clean and on the default branch, or clean with commits of its own, first kept as an archive tag.
  One holding uncommitted work is never touched and is reported.
  A worktree a project registers away from those folders, which a harness's own tooling left in a checkout or a scratchpad and no task of this home records, goes by the same rule.
  A worktree a live session may still be using is kept and named in the record with why, and one in a checkout is left out of the strays, while one in the home is still reported: one a process works in, one a process works anywhere in a folder of the same repository that holds it, as a session's subagents each work in a worktree under the session's checkout, and one whose HEAD, index, HEAD reflog or a file git sees in it, tracked or untracked, changed within the hour.
  A worktree in the home also waits an hour from when it was made, since a spawn makes it a moment before its record, while one in a checkout is dated by its last change alone.
  A worktree an older build put in a checkout's `.worktrees` is only reported, however clean, and only when this home's records name it, as `cfo reap` finds it: it is found through a checkout every home on the machine shares, and `cfo cleanup` or `cfo reap` retires it.
- Build cleanup follows the README's build update guidance.
- A folder that `temp_patterns` names, in the machine's temporary folder or in `scratch\.tmp`, and a finished task's scratch folder, go once nothing in them was written for a day, no running process names them and no running Git Bash has them as `/tmp`.
  `scratch\.tmp` itself, the folder every goblin's `TMP` names, is never removed, whatever is in it.
  A folder that is still `/tmp` for a running Git Bash, as the scratch folder of a goblin started before `TMP` named the shared folder can be, is kept and reported until the last shell of that Git Bash ends: the janitor asks each running Git Bash by its own `cygpath`, which changes nothing a running shell sees, and when one cannot be asked no temporary folder goes.
  `cfo cleanup` and a failed spawn's teardown leave such a scratch folder the same way and say so.
- A retired task keeps its text record: a day after cleanup archived it, its folder in `state\archive` keeps its Markdown, plain-text and status files and loses its scratch, logs, build output and evidence, and once filing moves its data folder to `data\archive\finished`, that folder keeps the same and its `deliverables`.
  A document handed to the Overlord with `cfo deliver` is kept in `state\reviews` either way.
  A folder holding a git repository is never trimmed and is reported.
- `state\backups` keeps the newest two backups of each kind.
- A local services stack `cfo services` started that no live task holds is stopped, keeping its named volumes, and so is the Docker engine cfo started for it once nothing it did not start runs on it.
- A browser session's folder under `.chrome-devtools-axi\sessions` in the user's profile goes once the bridge its pid file names no longer runs and nothing wrote to it for a day.
  One that holds anything but the tool's own three files is never touched.
- Nothing of a goblin that works is idle, however still it sits.
  Every background command a harness starts through Git Bash is detached from its terminal's host, and nothing the sweep can read says which of them the goblin still waits on: a gate waiter that blocks for hours, a watch on a quiet file, a test it reads from.
  They end with the goblin, at its pause, stop, cleanup or relaunch, and show on the board under it meanwhile.
  A goblin works unless both hold: the last outcome it reported is `done` or `failed`, and the monitor reads it at its prompt with no turn in progress and nothing asked.
  The CFO's terminal and a run item's always work, and so does a goblin whose state cannot be read.
- The browser bridge of a running terminal whose goblin has delivered and rests is ended once the rest and the bridge's disuse have both lasted an hour, by when the tool last wrote the session's files.
  A bridge keeps its page drawing whether or not anything drives it, so its processor time never says it is idle.
- A process a terminal of the home left running is ended once the terminal has no host: a goblin's, the CFO's or a run item's, whether it was paused, retired or failed.
  A pause, a stop, a cleanup and a relaunch each end what their own goblin left, so this pass ends what one of them ran out of time on, and what a goblin that failed left.
  It is proven the terminal's own exactly as a pause proves it ([Pausing and automatic resume](dispatch.md#pausing-and-automatic-resume)): by the terminal's mark, by its place in the task's folders, or by a parent that is the terminal's own.
  A machine service, a gate's agent and a desktop program the Overlord uses are never among them.
- A detached tree of a terminal that still runs, one whose parents no longer reach the terminal's host, is ended once its goblin has rested and the tree has done nothing, both for an hour, which takes two sweeps.
  A tree did something when a process joined it or left it, or when the processes it still holds used a tenth of a second of processor time between them.
  It is never judged by its processes' time added up, which falls when one that used the processor exits.
- A terminal a pause, a resume, a stop, a switch or a cleanup is changing is left to that command.
- `caches` is trimmed back under `caches_cap_gb` with each tool's own prune (`uv cache prune`, `pnpm store prune`, `npm cache verify`, `go clean -cache`, `go clean -modcache`), the tool used longest ago first; Playwright's browsers are measured and never deleted.
  Go's two prunes delete their whole cache, so each prune starts only while the machine's heavy line has one slot that no run holds or waits for, and runs as `cfo gate turn --as "the janitor, pruning the <tool> cache" --wait 30s -- <prune>`, whose turn no run that waits its turn builds beside, and which the line reclaims if it dies.
  A run that takes the line between that read and the prune's turn keeps the prune waiting 30 seconds at most, after which it runs nothing and the sweep trims no further, leaving the rest to the next sweep.
  The removal of the home's old caches after a move to a Dev Drive starts each folder only while the line is free the same way, though a run that takes its turn once a removal has started is not stopped.
  A sweep that finds the line not free, or a line of two slots, deletes no more and leaves the rest to the next sweep.
  It notes why, and `cfo runtime` shows the sweep's notes.
  A run outside the line, as a test run by name, is not seen and can build beside a prune.
- A bucket holding an entry the sweep cannot read, as a folder another account owns, is named under `cfo runtime`'s table of buckets, since its size, and the home's, is then only what could be counted.
  The sweep notes when the caches could not all be read, as their size against `caches_cap_gb` is then a lower bound.
- It reports, and wakes the CFO with an `orphan` wake when it reports something it had not reported before: a worktree no task records, a new folder or file in the projects root that is no checkout, a retired task's folder it would not trim, and a `data` folder over 200 MB.
- It names, and never ends, the processes nothing proves the fleet's own: a browser bridge with no mark whose parent is gone, and a gate agent's process whose gate is gone.
  They reach you as one `orphan` wake for all of them, raised once for each process, with its pid, its age, the memory of its tree and its command, with the value of any argument named as a secret left out.
  End by pid what is the fleet's leftover and leave what is the Overlord's.
  Nothing about the sweep is shown to him.
