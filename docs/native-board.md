# Native supervisor and board

`cfo serve` runs one native Go supervisor and serves the embedded React/TypeScript board on `http://127.0.0.1:4310`.
Use `--listen 127.0.0.1:0` for an ephemeral port; the command prints its actual URL.
Node, Vite, Docker, and a browser are not runtime dependencies of the supervisor.
Docker remains an optional project environment managed by the existing worktree profiles.
`serve` does not care where it was started: started from a Herdr pane, such as the CFO's own, it first drops that pane's variables (`HERDR_ENV`, `HERDR_PANE_ID`, `HERDR_TAB_ID`, `HERDR_WORKSPACE_ID`, `HERDR_STARTUP_CWD`, `HERDR_SOCKET_PATH` and `HERDR_BIN_PATH`) and keeps every other `HERDR_` variable, such as `HERDR_SESSION` and the user's configuration in `HERDR_CONFIG_PATH`, so a terminal it opens, or any other program it runs, is not refused by herdr as nested inside that pane.

The service takes the existing `.watch.lock` before opening recovery state.
Another `serve` holding it is refused, so two supervisors never run, and a `serve` started while this home's board answers as its recorded supervisor names that board and exits 1 before taking an address; a watcher holding it, the one the CFO's `stop-autoarm` hook hosts or a `cfo watch`, hands it over, because the supervisor supersedes it.
`serve` writes `state/serve.handover` naming its pid, start time and host, and writes it again every second while it waits; the watcher's wait wakes on that file, so it yields the lock at once, with nothing appended and no episode published, and no watcher takes the lock while a live `serve` waits for it.
On 2026-10-01 an install stopped `serve`, the CFO's Stop hook took the lock in the gap, and every new `serve` refused to start until the hook's window ended, which can run for hours.
A watcher in the middle of a cycle answers at once in `state/serve.handover.ack`, naming itself and the `serve` by pid and start time, and stops its monitor scan, orphan sweep and filing pass; `serve` waits up to five more minutes for a watcher that answered, so the Stop hook that rewakes an idle CFO is never ended for being slow.
A watcher from a binary older than the handover never reads the request, so after 30 seconds without an answer `serve` ends that one process, and only once it has proved it through the one handle it ends it by: the lock names it as a watcher by pid, start time and host, the process was created at that start time, and its command line is `cfo` or `goblins` running `hook stop-autoarm` or `watch`.
A holder running anything else, a terminal's host, a `serve`, a gate's test or another program, is left running and `serve` refuses as before; starting the board never ends a worker, a terminal's host, or a watcher's parent, and pending wakes stay in the durable queue.
While `serve` holds it, the Claude CFO's `stop-autoarm` hook still rewakes the idle CFO: it waits on the wake queue, rewakes once for each record no earlier rewake covered, and hosts the watcher itself again if `serve` stops.
It counts `serve` as the watcher until its heartbeat is older than the stall window (15 minutes, `CFO_WATCHER_STALL`), because `serve` stamps its heartbeat in the loop that runs its reconcile cycle and a slow cycle can hold it past the turn-end guard's five minutes; past the window it reports supervision down, naming `serve`'s pid and saying to restart it, since nothing can take the watcher from a live `serve`.
A watcher changing hands is not supervision being down: the lock is free between a watcher letting it go and `serve` taking it, `serve`'s record is empty until it is written, and a `serve` that has just taken the lock has not stamped its heartbeat yet.
So while a live `serve` waits for the lock, or a record is there that cannot be read, or a live process holds it, the hook looks at the watcher again for up to three seconds (`CFO_CLAUDE_AUTOARM_SETTLE_MS`) before that look counts against it; a lock nobody holds or asks for, or one a dead process left, counts at once.
When it does report supervision down, the banner's `Watcher:` line says what the last look found: no holder, an unreadable record, a holder that is not running, or a heartbeat that is missing, stale or dated in the future.
When its wait ends with its window and nothing queued, it rewakes the CFO to end a turn, which re-arms it.
Closing the browser disconnects a view, while Ctrl-C in the supervisor terminal, or `goblins stop` from any terminal, stops that process.
Once it holds the singleton and listens, `serve` records its pid and the board's address in `state/board.json`, and removes the record when it exits.
`goblins` with no command reads that record and asks the board's `/api/alive`, which answers with the supervisor's pid without building the fleet's snapshot: only a successful answer naming the recorded pid is the running supervisor, and anything else at the address, a page not found, an answer that names no pid or another one, makes the record stale.
On 2026-10-01 a healthy board took eight seconds to build its snapshot, the launcher's three-second wait read it as dead, and `goblins` tried to start a second supervisor.
A live supervisor gets a status line from the snapshot within three seconds, or one saying the fleet's status is still loading, or that the board could not read the fleet's state, and `goblins` starts and opens nothing and goes on to the CFO.
Otherwise it starts `serve` detached from its terminal, in a hidden console of its own that the programs `serve` runs share, so no console window opens, with its output appended to `state/serve.log`, waits up to a minute for the board to answer, which covers a watcher handing it the lock, and opens no browser: `goblins` opens the board only when it is chosen on its last screen.
Two `goblins` started at once each start a `serve`: the one whose start finds `serve.log` held by the other's, or whose `serve` exits finding the other taking the lock over or holding it, waits for the other's board instead of failing.
The detached `serve` listens on the board's address, which is the same every time: `127.0.0.1:4310`, or the numeric loopback address the `CFO_BOARD_ADDRESS` environment variable names (port 0 there asks the OS for a free port, as a scratch home or a test does; the record holds the real address).
An address already in use is never replaced with another: `goblins` starts nothing and says who holds it.
It asks the listener's `/api/alive`, which a supervisor answers with its pid and its home, so the message names the Code Goblins fleet of another home by its folder, an older supervisor, which answers with no home, by its pid alone, and says "another program" for anything else.
Only this home's own supervisor, started a moment ago by another `goblins` and not yet recorded, is waited for instead.
This home's own supervisor that holds the address but records no board within that wait is named by its pid, with the PowerShell line that ends it (`Stop-Process -Id <pid>`), since `goblins stop` decides from the record that is missing.
`cfo serve` without `--listen` uses the same address.
`cfo update` restarts the supervisor on the address the one it stops was serving, read from the record, so a home on an address of its own keeps it whatever shell runs the update; with no supervisor running it uses the board's address.
A later `cfo update --recover` starts the previous build's supervisor on the address the record names, whether or not that supervisor still answers, and on the board's address only when no record can be read.
A record whose address does not answer, left by a supervisor that ended without removing it, is replaced by the next start, and a record naming anything but a plain loopback board address is ignored.
`goblins --board` finds or starts the supervisor the same way, opens the board root in the browser every time, and exits 1 naming the link when the browser cannot be opened; it starts, shows and attaches no CFO.
The quick start (`goblins` with no command) says first, under the banner, what it found and what it started, one ticked line each: `Supervisor already running` or `Supervisor started`, then, after the agent steps when no CFO runs, `CFO already running` and where, or `CFO started as` the agent, in the home.
Then `goblins` brings the Overlord to the CFO, after the quick start's agent steps when no CFO runs (docs/install.md, The quick start).
A CFO whose registration in `state/primary.json` names a live process is reused, never started a second time: `goblins` brings its registered workspace and tab to the front and hands its terminal to `herdr`, attached to the session the CFO registered in.
It decides from the registration alone and asks neither the board nor Herdr, so a supervisor that has not checked the registration yet or a Herdr that cannot answer changes nothing.
Otherwise it makes sure Herdr's server runs and starts the remembered harness as the CFO in a fresh `cfo` tab it creates in the CFO home, since Herdr starts an agent in its pane's own directory; it asks for no project, because the CFO works across every project from its home.
An old `cfo` tab with no agent in any of its panes is closed when every pane sits at its shell prompt, and renamed to `shell` when anything else runs in one, `goblins` itself included; a `cfo` tab with an agent in any pane is left as it is and no second CFO is started beside it.
It brings the CFO's tab to the front, and every run ends on one screen: the home and the board's address as an OSC 8 link, with **Open the CFO terminal**, which Enter takes and which hands the terminal to `herdr` attached to the fleet's session, or to the CFO's native terminal, and **Open the board**, which B takes.
Run inside a Herdr pane there is nothing to attach, so `goblins` only brings the CFO to the front.
`goblins status` reads the board record and prints the board's link, the status line and the supervisor's pid, and exits 1 when the supervisor the record names does not answer as itself there.
A supervisor started in the background has no terminal for Ctrl-C to reach, so `goblins stop` writes `state/serve.stop` naming the record's pid and waits up to 30 seconds for the board to stop answering.
The supervisor checks for that request on its notification tick, which comes at least every two seconds between cycles, and stops as it would on Ctrl-C, removing its record; a request naming any other pid is left over from a supervisor that already ended, so it is removed and stops nothing.
A supervisor also removes any request left from before it started, so a reused pid cannot stop it.
`goblins stop --force` ends the recorded pid's process tree with `taskkill /T /F` and removes the record instead, and a record whose supervisor does not answer as itself is removed without stopping anything; a supervisor whose snapshot is slow still answers, so its record is never taken for stale.
Restarting with the same CFO home recovers durable events, evaluations, actions, and lineage.

## Native hook setup

Run `cfo hooks check claude`, `cfo hooks check codex`, or `cfo hooks check pi` to inspect the installed capability contract.
Run `cfo hooks install <harness>` to add the corresponding native lifecycle integration.
The verified minimum contracts are Claude Code 2.1.278, Codex 0.154.0, and Pi 0.85.1.
The check reads the harness's version through PowerShell with the execution policy bypassed, so the script shim npm installs for Codex and Pi runs even where the policy is Restricted.
Kimi Code is not a harness the fleet runs for now: it ran only in Herdr, and no native terminal knows its screens.
Bringing it back needs its screens captured for a native terminal (its startup prompts, its composer and the marks of a turn, as `internal/harness/screens.go` holds them for Claude Code, Codex and pi), an adapter that builds its launch and says how it is stopped and resumed, a hook that reports each prompt it takes, which is how a delivery is confirmed, and its name back in what lists the harnesses: `cfo spawn` and `cfo switch`, `cfo doctor`, the installer, the ticket labels and the board's Start.

Default destinations are `~/.claude/settings.json`, `~/.codex/hooks.json`, and `~/.pi/agent/extensions/cfo-native.ts`.
Use `--config-dir <absolute-directory>` for a custom harness home or an isolated test configuration.
Setup preserves unrelated JSON hooks/settings, takes a first backup before changing existing JSON, and replaces only its owned helper.
Its hooks go back where they stood in each event's list, as `cfo install`'s do in the same `~/.claude/settings.json`, so rerunning either changes nothing when nothing changed.
It does not change models, gate policy, approval settings, or Codex hook trust.
Review the exact installed Codex definitions in `/hooks` before they can run.

Claude and Codex helpers pipe hook stdin to `cfo native-hook <harness>` using bounded PowerShell commands.
Pi's extension calls the same executable directly from its native event handlers.
Hooks record identity and lifecycle metadata, not prompts, tool arguments, or transcripts.
They atomically spool an event and return without Git, network, gate, or browser work.
The installer records the absolute executable/home/state paths, so reinstall the owned helper after moving the executable.

| Harness signal | Native meaning |
| --- | --- |
| Claude/Codex SessionStart | Session started |
| UserPromptSubmit / PostToolUse | Activity evidence |
| Stop | Settled; requests task evaluation |
| SessionEnd | Harness session ended |
| Codex Interrupt | Interrupted; requests evaluation |
| SubagentStart / SubagentStop | Reported child started / settled |
| Pi session_start / agent_start / tool_execution_end | Started / active |
| Pi agent_settled with no pending messages | Settled after continuations and retries |
| Pi session_shutdown | Session ended |

Distinct hook invocations receive distinct durable IDs, including two Stops within one native turn after a hook-requested continuation.
Replaying the same spool record retains its ID and is idempotent.
A Stop never establishes that the task shipped.

## Evidence and recovery

The supervisor reuses the existing monitor, watch signal signatures, wake queue, task metadata, native terminal steering, and frozen pipeline reader.
One Windows filesystem watcher consumes the spool, with a two-second recovery interval for missed notifications.
If the native-inbox state folder disappears, ingestion or the next board update recreates it and names the repair on the board; earlier hook events may be missing.
If that folder cannot be read or recreated, the board reports the folder problem and delayed hook events while other task updates and reconciliation continue, and ingestion resumes when access returns without restarting serve.
One slow reconciliation pass runs each minute; there is no browser-driven task polling engine.
Unresolved tasks continue reconciling after SessionEnd, after becoming ready, and after their native session is retired.
Verified terminal tasks leave that polling path until new session activity supplies a reason to reevaluate.

Fresh, matching Herdr monitor evidence distinguishes a working harness from an unavailable or stale one.
An abrupt process crash does not change the recorded native event into an invented end event.
The board shows runtime unavailability separately and continues evaluating independent task/gate evidence.
Silence, a vanished process, a Stop, and a prior `notify --done` are never substitutes for delivery evidence.

Review, tests, lint, documentation, push, PR, and CI must agree with the task's exact HEAD under its frozen policy.
Unrecovered pipeline custody blocks native worker terminal input even if a run is failed or cancelled.
The browser can request evaluation or send contextual review comments to the verified CFO; it cannot approve gates, drag a task to done, or merge.
An explicitly connected terminal sends ordinary terminal input to that existing session, subject to the identity and custody boundaries below.

For a ship task, a merged PR remains **merged-awaiting-verification** until the supervisor verifies landed main content.
It checks repository identity, current remote default-branch HEAD, matching locally available main objects, and the exact tree entry (mode, type and object) or absence of every task-changed path on main, then rechecks the remote head.
A mode-only or type-only change with identical bytes therefore stays unverified until main holds the same entry.
Missing local objects produce an explicit verification-pending reason; the supervisor does not fetch or modify the worktree to hide missing evidence.
The pipeline's `terminal_head_verified_at` proves its terminal worktree head, not that the task's content was read on main.
Manual task modes retain their existing review/delivery authority and are not automatically marked done by native hooks.

Full-task diffs use a retained, validated project merge base when available, otherwise an established `origin/HEAD` merge base.
If neither is available, preview reports that limitation instead of substituting `HEAD^` and omitting earlier task commits.
Individual commit previews are separate, explicit views.

## Persistence bounds

The active store retains up to 512 session nodes, 512 retired-parent markers, 512 tracked tasks/evaluations, 256 actions, and 2,048 recently seen event IDs.
The serialized store is limited to 8 MiB and recent malformed-event diagnostics to 20 entries.
Ended sessions or sessions inactive for seven days can roll out when they have no pending action.
Needed unresolved task references survive session retirement, and missing parents remain explicitly retired or unreported.
Task admission may replace terminal history; 512 unresolved tasks or 512 recent sessions deliberately apply backpressure rather than silently dropping ownership.

The inbox admits 4,096 small records, each at most 64 KiB.
Ingestion sorts its bounded read window by event time before its 256-record consumption batch, retries records awaiting start/metadata evidence, and rotates the window if concurrent writers briefly exceed admission capacity.
Transient persistence failures retain the inbox record and roll memory back to the last durable state.
The next cycles retry the save, and the board reports a persistence failure only once saves have kept failing for 30 seconds; other errors still show at once.
Malformed records leave a bounded diagnostic and do not wedge subsequent valid work.
An interrupted evaluation is safe to replay; an interrupted external delivery becomes uncertain and is not resent automatically.
Uncertain actions remain visible for operator inspection and can eventually exhaust action capacity if left unresolved.

## Board and orchestration

The header switches between Board and Orchestration, with one main view visible at a time and one contextual pane on the right.
Board groups actual tasks into Tasks, In progress and Completed, side by side as a kanban by default.
Paused, pausing and resuming tasks sit at the bottom of In progress, under a thin Paused divider with their count, like a page break; the divider and its cards show only while a task is paused, pausing or resuming, and each keeps its Resume and Stop.
The layout button in the header, left of the Command Center, switches the board between the kanban and a stacked layout, one column under another; its tooltip names the layout it switches to, and the browser remembers the choice in local storage, falling back to the kanban when storage is unavailable.
A board 960 px wide or narrower, such as a phone or a narrow window beside the panel, stacks either way, so a card never squeezes its title or status; the CFO's bar and a column's heading wrap too, so nothing on the board is clipped or scrolls sideways at any width.
While the board is that narrow and the kanban is the layout chosen, the layout button would change nothing, so it is dimmed, does nothing and says why in its tooltip: Too narrow for columns side by side, so the board is stacked. Close or narrow the panel, or widen the window.
While the stacked layout is chosen the button is never dimmed and always switches to the kanban, so the kanban can be reached again in a window that holds it.
Beside a kanban board an open panel yields by default: in a workspace wider than 1410 px the board keeps 980 px (the 961 its three columns need, 2 for its border and up to 17 for its scroll bar) and the panel takes the rest, never under its own 420 px, so a window 1707 px wide (a 2560 by 1600 screen at 150 percent) shows the board at 980 and the panel at about 690.
A workspace no wider than 1410 px cannot hold the three columns beside a panel, so it keeps the even split, as the stacked layout and Orchestration do at every width.
The rule is `.workspace.with-pane.kanban` in `frontend/src/styles.css`, which `App.tsx` switches on with the `kanban` class while the Board view shows the kanban; a width set by dragging the divider is remembered and wins, and so does an open Changes section.
Until 2 October 2026 the panel took half at every width, which left the board 835 px on that window, one column under either layout.
Tasks lists backlog rows and briefs nothing has started: a `data/<id>/brief.md` with no live task record, status log or archive entry.
An existing delivered outcome keeps a stale Queued row out of Tasks and refuses Start, while its completed history remains available.
After verifying delivery and returning the worktree with cleanup, the CFO runs `cfo backlog done <id>` to move its legacy bold or checkbox Queued row to canonical checked form under Done, retaining its entire continuation, delivery evidence and archived source.
The command refuses live metadata, missing or unreadable delivery evidence, duplicate source rows and parked work, and repeating a completed closeout changes nothing.
Tasks and In progress are in priority order, top first, and every list of tasks the board shows follows it; Completed stays newest first.
In progress never pages: every goblin's card is in the column at once, in priority order, and the board scrolls when they run past the screen, with the Paused group after the last of them.
Tasks, the Paused group and Completed show a list of up to ten cards whole, with no pager, and the board scrolls when they run past the screen.
Past ten, each of them shows a page: as many of its cards as fit, Completed its newest, in the visible board below the list when the columns are side by side and in one screen below the column's heading when they are stacked, less room for the pager, and never fewer than five cards, when the board scrolls; each page holds as many of its own cards as fit, row by row with each row as tall as its tallest card, and starts where the page before it ended, so a tall card shortens only its own page.
The floor of five is there because the page was once sized by the visible height alone, and on 2 October 2026 the memory meter and tall cards left a page of 19 queued tasks room for two.
A card's height is measured at the list's width, those not shown yet in a hidden container of no height beside the list, so the pages come from real heights.
A pager under a list, when one page does not hold it all, says which cards show, 1–5 of 18, with earlier and next buttons, and a sideways touch swipe of at least 48 px turns the page while a vertical one scrolls; in Tasks a swipe that starts on a card's rank is a drag, not a page turn.
A drag places a card anywhere in its list: held within 64 px of the top or bottom edge of what scrolls the list (the board's canvas, the CFO's panel or the page), the card waits while the list scrolls under it, up to 20 px a frame and only while it is held there, and in Tasks a card held for 0.6 seconds over a page arrow turns the page, takes the last place on the page it turned to, and turns again for as long as it stays there.
A keyboard move past the page's edge carries the page with the card, keeping its focus.
A card or list that fails to render shows a warning in its place, This card could not be shown or This list could not be shown, with an icon-only Retry, while the rest of the board stays usable.
Tasks lists the backlog's Queued rows in file order, then briefs without a row; In progress lists the goblins in the attention order kept in `state/attention.json`, then any goblin not placed yet, and `cfo fleet-view` lists its goblins in that order too.
Dragging a card, or Alt+Up and Alt+Down on a focused one, sends the whole list's new order to `POST /api/order` with the board's token, which the Host, Origin and token checks guard like every other change.
A Tasks order rewrites only the order of the rows in `data/backlog.md`'s Queued section, each row moving with its indented detail lines while notes, parked rows, rows with live metadata or a delivered outcome and every other section stay where they are, and a brief without a row gets one, `- [ ] <id> - <id> (repo: <project>)`, at the place it was dropped.
Task edits also emit unchecked checkbox rows, which both CFO and tasks-axi read, while CFO continues reading existing bold rows.
An order that is not exactly the queue the file holds, because a row was added, removed or renamed after the board showed it, is refused with 409 and changes nothing; an In progress order naming a goblin with no live task record is refused the same way.
The board shows the dropped order until a snapshot from the revision the save answered with arrives, and a refused order goes back with the reason under its column.
The snapshot's `memory` is the machine's available physical memory, the standby list included, and its available commit (RAM plus page file, which every process's private memory is charged against and which a new process needs even while physical memory looks free), both read with `GlobalMemoryStatusEx`, beside the fleet's 4 GB floor and the 5 GB mark at which the CFO starts the next queued task; the meter at the head of Tasks shows it as a number and a bar spanning twice the 5 GB mark, or the machine's memory if that is less, marked at the floor and at the mark, whose fill turns amber under the mark and red under the floor, and the first queued task that is not blocked is marked Next up.
It also carries the kernel's paged and nonpaged pool sizes, read with `GetPerformanceInfo`, since a paged pool that keeps growing is a driver leaking memory, and, while commit is the tighter of the two, `holders`: the three apps holding the most commit, each counting its first process and every process it started, so the Codex app's MCP servers count as its program rather than as python, read from one system process list.
The meter shows whichever of free memory and free commit is the tighter, memory on a tie, labelled "Commit free (memory plus page file)" when it is commit and followed by one line naming those apps; Start's tooltip, the Next chip and Resume's tooltip follow the tighter one too, and one amber line warns when the paged pool passes 4 GB, memory Windows holds that no goblin can use and that a restart of the PC frees.
The snapshot's `subscriptions` lists `claude` and `codex` only while a registered CFO, or a goblin whose terminal the monitor saw active, busy, idle, parked or harness-erroring within the last two minutes, runs that harness; a paused, ended, archived or launching task, a saved model and quota data alone never show one.
No billing proof is read: every goblin's terminal strips the harness billing keys before the harness starts, so a harness the fleet starts runs on its subscription.
Each entry carries that provider's weekly `quota-axi` window (`seven_day` for Claude, `weekly` for Codex) as `percent_remaining`, `resets_at`, `read_at` and `status` (`available`, `stale`, `unavailable` or `auth_required`), and nothing else: no account, balance or provider error.
`serve` reads `quota-axi` once a minute in the background under a 15-second bound, and a snapshot only reads what that last read cached.
The Tasks column draws each as a ring around the provider's mark beside the memory meter, filled to the remaining percentage, with a tick at 5 percent that turns the ring amber at or under it, and a dashed ring with **?** for anything but a fresh reading; its tip gives the reset time in the viewer's time zone and the reading's age.
`serve` also wakes the CFO about the machine and the forge, through the same wake queue as every other wake, so `cfo drain` shows them and each says what to do next in one line.
`memory_ready` (wake kind `memory`): `serve` reads the memory meter once a minute, and once available memory and commit both read at or above the 5 GB mark on two readings in a row, while a queued task could start or a live goblin's latest report is `waiting on memory`, it wakes the CFO once, naming the next startable task in queue order and the goblins to release; it wakes again only after a reading falls under the 4 GB floor and crosses back, and never twice within fifteen minutes.
`ci_finished` (wake kind `ci`): every two minutes `serve` asks GitHub through the authenticated `gh` about each watched repository, which are the repositories live goblins work in, kept for six hours after the last one leaves.
Each poll makes one PR list, one batched GraphQL comparison when the repository has open PRs, and one main-run list per watched repository, plus one run read for each red run it reports; it lists every open PR, even in a repository with no live goblin.
When every check on a live goblin's open pull request has concluded (the one it recorded, reported `done`, or opened from its worktree's branch), it wakes the CFO once, keyed by the goblin, with the result and the names of the failing checks; a rerun that finishes with a new result wakes again.
When the newest push run of a workflow on a repository's default branch finishes red, it wakes the CFO once for that run, keyed `main:<repository>`, naming the workflow, the failing jobs and the run id; a cancelled run is not red.
One pull request wakes at most once every five minutes, and one workflow on one repository's default branch wakes at most once every five minutes, so a push that turns two workflows red raises two wakes, each naming its own workflow and run.
A repository with no `origin` remote is a local one and is not watched: `gh` is asked nothing about it, and it raises no error and no wake.
A repository that has an `origin` and still cannot be read (its `origin` is not GitHub, `gh` is signed out, a `gh` call fails, or its default branch cannot be found) keeps one line in the supervisor's error on the board, naming it, from the first failing poll until a poll reads it again.
Once the same failure has been met on two polls in a row it wakes the CFO once as `ci_unreadable` (wake kind `ci`), keyed `repo:<repository>`, with the error and the next step: sign `gh` in with `gh auth login`, point `origin` at GitHub, or set `origin`'s default branch with `git remote set-head origin -a`.
It does not wake again while the failure stays the same; a different failure met on two polls in a row wakes again, and so does one that cleared and came back, never twice within five minutes for one repository, and a failure clearing raises no wake.
The failure is compared by its exact text, so one whose text differs on every poll keeps its line on the board and does not wake, and one whose text changes, as when a goblin starts in a repository `gh` cannot read, wakes once more.
A 403, 429 or exhausted allowance pauses every later GitHub read in that repository, across restarts, until its retry or reset time, or an hour when GitHub gives no usable time; the reads it holds fail with the same text every time, so a refusal that never clears still wakes once as `ci_unreadable` on its second retry.
`pr_health` (wake kind `pr`): a PR that conflicts with its base or falls behind the default branch wakes the CFO once per condition and head, naming the owning goblin and the safe update, or a teammate's author and link.
`pr_unread` (wake kind `pr`), keyed `repo:<repository>`: an open PR head whose comparison was not read, and a listing that stops at its limit of 100 open PRs, keep one line in the supervisor's error on the board, during a backoff too, until a poll reads them all.
A head whose own comparison comes back missing or invalid in an otherwise readable response, conflicting or not, wakes the CFO once, and the listing limit once until the listing falls under 100 and comes back, never twice within five minutes for one repository; the wake names the heads, the reason and the next step, and recurring poll errors raise no further wake.
A comparison request that fails as a whole (a refusal, a timeout, a server error or a response with no comparisons) keeps only its line on the board and wakes for no head, so a head it left unread still wakes once if its own comparison later fails; a refusal reaches the CFO as `ci_unreadable`.
It never raises `ci_unreadable`, and CI wakes and readable PR health from that repository continue.
`pr_overlap` (wake kind `pr`), keyed by the goblin: the same poll checks new teammate PRs and issues against running goblins, sharing one repository activity read at most every ten minutes under a thirty-second overall deadline and the existing allowance/refusal backoff.
The area is the branch's committed change from its default-branch merge base, including renamed and deleted paths, with issue words from the brief's Task and Acceptance criteria.
An item must have opened strictly after the goblin's current spawn generation began; an item opened during the generation stays eligible if later committed changes first make it overlap.
Pre-existing open items remain visible in the ordinary ticket report and do not wake on polling startup, restart or area changes.
The wake names the teammate, item link, overlap and goblin, asking the CFO whether to continue, wait or narrow the work.
Fresh live evidence is checked before and after the read, including after a per-PR done report; stopped or paused goblins, bots, the signed-in viewer, branch-only work and the goblin's own recorded or claimed issue in that repository never produce these wakes.
Each item wakes once per goblin generation across restart, acknowledgement, closure and area changes.
Unknown item or generation timestamps, unread authors, changing branch inputs and incomplete reads keep a board line without guessing times or raising `ci_unreadable`, while readable overlaps continue.
What the fleet wakes remember, in `state/fleet-wakes.json`, survives a restart, so a restart neither repeats a wake nor loses one, and a failing repository's line shows again at once.
The board lists no queued wakes of any kind: these reach the CFO as the others do, through its Stop hook or the line typed into a Codex or pi CFO's terminal.
A Claude Code CFO's Stop hook arms only while a task is in flight, so a red main with no goblin running reaches it at its next turn end with one.
A snapshot with no commit limit has not reported commit, so the meter shows memory rather than zero commit, and the labels under the bar give way toward the left so that neither leaves the box when a mark is at the end of the bar.
The snapshot's `disk` is the free space and size of the drive the home is on, read with `GetDiskFreeSpaceEx`, beside `config/fleet.json`'s disk floor (`disk_floor_gb`, default 15 GB), under which no goblin and no gate test run starts, and the lower mark at which the CFO is woken (`disk_wake_gb`, default 10 GB).
In the memory meter's box, under memory, **Disk free** shows it as a number and a bar spanning twice the floor, or the whole drive if that is less, marked at the floor and the mark, whose fill turns amber under the floor and red under the mark; Start's tooltip and the Next chip name free disk when it is what is short.
`disk_low` (wake kind `disk`): `serve` reads the disk with the memory meter, and when free disk falls under the mark it wakes the CFO once with what is free, what each part of the home holds and what the janitor last freed; it wakes again only after a reading back at or above the floor, and never twice within fifteen minutes.
Every queued card that is not blocked carries the same Start, a play button among its controls; a blocked card shows what it waits on instead.
A queued task has not started, so nothing of it can be stopped: its card and its panel carry Remove where a started task has Stop, and Remove asks first, with Remove from queue and Cancel, then takes the task out of the queue and keeps its brief, through the same `POST /api/tasks/lifecycle` stop action a started task's Stop sends.
The board starts nothing on its own: the CFO dispatches queued work in the Tasks order.
Start on a queued card sends `POST /api/tasks/start` with the task's ID, guarded by the Host, Origin and token checks like every other change, and the supervisor dispatches it through `cfo spawn <id> --project <p> --brief <data/<id>/brief.md> --harness <h> [--model <m>] [--effort <e>] [--mode <m>]`, the same binary the CFO runs.
The project is the brief's `## Project` line, or the backlog row's repo; harness, model, effort and mode are what the backlog row names, such as `(harness: codex, model: gpt-6-astra)`, then what the brief names on lines of their own, such as `mode: direct-PR` under its Delivery heading, then the fleet's defaults, `claude` on `claude-opus-5-5` at `xhigh` (the default model and effort apply to Claude Code only).
A task with no brief yet gets one written from its backlog row, and the CFO is told through the wake queue, before `cfo spawn` runs.
It is refused with 409 and the reason, before anything runs, when the task already has a live task record, is not queued, waits on a blocker its backlog row names, names no project or a harness, mode, model or effort `cfo spawn` cannot take, when less than 5 GB of memory or of commit is free, naming which, or while another Start or a Resume runs; a board started without a dispatcher, such as a test fixture's, refuses every Start.
The answer's `passing` is true for a cause that passes by itself, memory or commit under the 5 GB mark or another Start or Resume running, and the card drops that reason once a newer snapshot shows Start unblocked; any other reason stays on the card until its Start is pressed again.
The supervisor waits for `cfo spawn` to end, which confirms the goblin works before it returns, and never cuts it short, since a killed spawn strands its task; the card shows Starting meanwhile.
Then it tells the CFO through the wake queue, as `cfo notify` does, with a notify keyed by the task: `started: the Overlord started this from the board, ...` after which the task goes to the top of the attention order, or `start failed: <the last line cfo spawn printed>`, which the card also shows; neither is a question.
An accepted Start answers 202 with the revision from which every snapshot shows it, and the board opens the new goblin's Terminal view once the snapshot shows its session; an older snapshot that still carries the last Start's failure neither shows that failure on the card nor stops the wait.
Completed lists verified delivery, and within the last week the newest 20 entries of history: tasks cleanup finished, from a status log left without its record or one the archive holds, and pull requests merged into a fleet repository, read from merge commits on origin's default branch of this home and each checkout under the projects root, locally.
History is rebuilt away from the supervisor's loop, so it never delays native events, the heartbeat or a snapshot: at start, within 10 seconds of a task record being removed or a gate seeing its task merge, and otherwise every minute.
A rebuild reads a checkout with git again only when a fetch or a remote change rewrote its git files, and one git cannot read is tried again after 30 minutes.
A merge commit several of those repositories hold, such as a fork's copy of its upstream's history, is listed once, under the repository its pull request was opened in: the one whose origin publishes that pull request's head, `refs/pull/<number>/head`, as the merge's second parent.
Each origin's pull request heads come from one `git ls-remote` call, which is kept and made again only for a pull request it does not list or lists with another head, such as one listed while it was still open, at most every 10 minutes, so a fork carrying a week of its upstream's merges costs one call per repository; when no origin says, the merge goes to the first repository listed, this home first, and a failed call is reported.
A merged pull request whose live task already shows the merge, in phase merged or done, appears only on that task's card, which stays In progress as merged-awaiting-verification until landed content is verified.
Any other merged pull request keeps its Completed card, even when a live task reported it: a task whose gate missed the merge, or that is blocked, failed or waiting on a question, keeps its own proven state on its card.
A history card is its pull request link, since it has no live worktree to review.
A finished task finds its merge among every merge of the week, not only the newest 20, so an older merge still marks it merged.
A finished task's GitHub pull request that no merge commit shows is asked about with `gh pr view` on a history refresh: merged (a squash merge leaves no merge commit) reads Merged, closed without merging reads Closed, and open reads Finished.
A merged or closed answer is kept; an open one, or an ask that failed, which the board reports as an error, is asked again after 10 minutes.
The asks of one refresh share a 5-second budget, so a slow GitHub never holds up the supervisor; a pull request not asked before it runs out reads Finished and is asked on the next refresh, and an ask it cuts off counts as failed.
The card wears GitHub's icons and colors: the purple merge icon for Merged, the red closed pull request icon for Closed, and the pull request icon otherwise.
A live task's pull request link wears the merge icon too once its gate shows the merge, in phase merged and after its landed content is verified.
A task no native hook has reported takes its status from the fleet's own records: a question it is still waiting on in the wake queue, then what its gate proved, then what Herdr sees in its pane or, for a native task, what the monitor reads from its terminal, and it is evaluated once a minute like any other.
Each card shows its short title, then a muted line with the task's repo and status, the status's dot between them, and its pull request, linked only when the reported value is an https URL; the task's own latest status line is in its panel.
A title shows without the harness a backlog row names after a semicolon, such as "; Claude Code", on every card, panel, node and alert, since the harness mark shows it; the adjust form keeps the row's own title.
A queued card that waits on something adds no line about it, at the Overlord's word (2026-10-05); the CFO's note on the wait is in its panel behind More, under its status.
A paused or finished card shows when, beside a clock, and its status says what; the Next up chip sizes to its label and never wraps it.
The title shows up to three lines and the repo and the status wrap onto further lines; a title cut at three lines ends in an ellipsis and shows in full in the board's tip on hover or keyboard focus.
Every tip on the board is one box that floats over the whole page (`frontend/src/tips.ts`), shown for whichever part carrying `data-tip` the pointer or the keyboard's focus is on, so no scrolling box cuts a tip off and a dimmed control does not dim its tip; a part inside the Command Center shows its tip inside that dialog, which is drawn over the page.
The tip holds its whole text: it grows to its text up to 320 px, or the window's width less 16 px, then wraps, breaking a long word if it must, at 16 px on a solid surface a step lighter than the cards, with an edge.
It is kept inside the window on every side and is gone the moment the pointer or the focus leaves its part.
A tip on a card, its title's and its controls' and harness mark's, opens over the card or under it, whichever edge its part is nearer, so it never lies on the card; any other tip opens under its part, or over it for the Orchestration canvas's controls, the voice dock and a diff's comment mark, and along its part it sits at the start, the end or the middle, as the part asks.
A tip takes the other side when its own has no room on the screen or would cover the memory meter or a control or link of another card, and before that it slides along its card past what is in its way; over the first card of Tasks it may open past the meter, and when no place is clear at its full width it is tried again no wider than its part.
A tip too tall to be clear anywhere, as the tip of a very long title can be between two cards, takes the place where it covers least.
While it shows it follows its part as the page scrolls, resizes or changes, says what its part's tip says now, and goes when that part leaves the page; a part pressed with a mouse or a pen shows no tip until the press is released, so a card being dragged shows none, while a finger shows a tip for as long as it is down.
Until 2 October 2026 only cards had the floating tip and every other tip was drawn by a stylesheet rule inside its part, where a scrolling box could cut it off, a window edge could run through it and a dimmed button made it see-through.
Each card in Tasks and In progress also shows a quiet clock under its status, in whole minutes, hours and days (just started, 47m, 2h 14m, 1d 3h), counted from the snapshot's `since`: when a live task's worktree folder was created, which `cfo spawn` makes fresh for each goblin and a switch keeps, so the clock counts the whole session across switches, or its spawn generation's time when that folder cannot be read; or when a queued task's `data/<id>/brief.md` was created.
A queued row with no brief, or a live task with neither a readable worktree nor a generation that records a time, gets no clock rather than a guessed one, and a completed card shows none.
Each card carries the mark of the harness its goblin runs in its corner, under its controls: Codex's mark for Codex (a cloud holding a terminal prompt, drawn in the board's own icon set), Claude's for Claude Code, π for pi, Kimi's for Kimi and a terminal for any other harness; its tip names the harness, then the model and effort it runs, and a click on it selects the card.
Under the clock, a card's pull request takes a row of its own that wraps within the card, and the card's controls sit beside its text or, on a card under 420 px wide, under it: every part of a card has its own place, so none is drawn over another, and the card's text is never smaller than 16 px.
Beside a live goblin's pull request, that row says where its hosted checks stand, as the supervisor's CI poll read them every 2 minutes: Checks running, Checks failed, Checks cancelled or Checks passed, in blue, red, amber or mint, with the failed or cancelled checks named in its tip.
A failure opens the first failed check's own page, where its log is, when that page is on GitHub, and the pull request's checks otherwise; every other state opens the pull request's checks, and nothing off GitHub is opened.
Approved follows it once a reviewer approved the pull request, and the goblin's panel shows both beside its pull request.
Before them, a goblin in a repository that runs `cfo gate test` shows the newest run of its change, read from the run's report in the store: its gate's run of the commit its pull request is at, which names no task, or a run it made itself.
Local tests passed or Local tests failed, in mint or red, has the level, how long the run took and how long it waited for its turn in its tip, after what failed, and opens the end of the run's log, redacted, served by the board from the store and from nowhere else.
Once a task's pull request is merged, its card, in Completed too, says how the deploy of that merge stands, apart from its checks and in their look: Deploying, Deployed, Deploy failed or Deploy cancelled, with the deploy workflows and the commit in its tip, opening the run that failed, else the newest one.
The same CI poll reads it from the push runs on the repository's default branch: the newest run of each workflow whose name says deploy, for the commit the merge pushed, found by the commit's title, "Merge pull request #N" or a squash's "(#N)".
A deploy that no workflow on the default branch runs shows nothing: a site shipped with the Vercel CLI, for one, leaves no run to read, and its card stops at Merged.
In a repository other people work in, that row also carries the task's ticket, GitHub's issue mark in the color of where it stands and its number, opening the issue, and the avatar of each teammate whose open work meets a live goblin's branch, ringed in amber, opening that work, with who and what in its tip.
The goblin's panel names its project's people beside the project's name, each a GitHub avatar and username opening their profile, whoever is in the goblin's area first and ringed, and puts the ticket beside the pull request.
Avatars come only from GitHub's avatar host, the one image source outside the board its content policy allows; one that does not load shows the person's initial.
The title is the task's short title: a queued task's is its backlog row's, and `cfo spawn` keeps the one given with `--title`, else the row's, on the task as `title=` in its metadata so it outlives the row; `cfo title` changes it there, and a running task's recorded title wins over its row's.
A task with neither is named by its id.
The status line and the pull request come from the current generation's lines only, so a respawned task id shows neither its earlier generation's activity nor its pull request until it reports again.
Cards state progress in plain words, never engine words: Not started, Working, In review gate (with its step, such as In review gate: tests), Waiting on the CFO, Waiting on a goblin by its id, Waiting on CI, Waiting on deploy, Checks passed, Delivered and No fresh evidence.
A goblin waiting on another goblin names it once, in its card's status line, and links to it from its panel: a button beside its status there opens the goblin it waits on, and Orchestration draws a dashed line from the waiting card to that goblin's card, apart from the family tree.
A goblin that waits on the Overlord, or has an open question to him, shows Waiting on the CFO, since the CFO carries every question to him.
A goblin whose last reply asked the CFO something in prose, which the monitor reads and wakes the CFO with as `goblin_asks`, shows Waiting on the CFO with that question, as a goblin that asked with `cfo notify --blocked` does, until it reports again or the CFO acks the wake.
A reported wait on the Overlord ends once the Command Center item it raised closes: his answer reached the goblin, the item was cleared, or the answer on its page went to the CFO to relay; the card then shows what the goblin is doing, and an answer still on its way keeps the wait.
It keeps its phase's colour, without the amber emphasis that belongs to the CFO's bar, so a wait on the Overlord, a goblin, CI or a deploy is shown in the same calmer sand colour.
The CFO is pinned above the Board's columns in a plain bar, a card like the columns under it: the CFO's portrait, its harness mark, a line that says how many goblins the CFO supervises, and one icon button for its terminal.
With nothing waiting on the Overlord the line opens with All quiet, and the bar has no Open Command Center.
While something in the Command Center waits on him the bar says none of what it is: Open Command Center appears on it beside the terminal icon, glows in the amber of waiting and carries how many items wait, and it opens the Command Center on the first of them.
The glow breathes, and holds still for a reader who asked for reduced motion.
While no CFO runs the bar says so and offers Start the CFO instead.
Its terminal icon and its portrait open the CFO's terminal and hand it the keyboard.
The mark of the harness the registered CFO runs, the snapshot's `cfo_harness`, sits beside its portrait, and its tip adds the model of the CFO's newest session in that harness.
While [AFK mode](#afk-mode) is on nothing glows on the bar whatever waits on the Overlord, and its line says since when it is on and who turned it on, the Overlord from his board or his terminal or the CFO at his ask, how many decisions the CFO logged and how many items are held.
Held for you, a disclosure under the bar's line, lists each held item that still waits on him as the Command Center's inbox draws one: whose it is, what it asks, the choice the CFO recommends for its question, what became of it and its goblin's latest report since, and a button that opens that item in the Command Center.
It never opens by itself, and shows Nothing is held for you when it is empty.
A click or key on the board after five minutes with none, counted from when AFK mode turned on, is taken for the Overlord coming back: the click or key does what he meant, and the board then offers to turn AFK mode off or stay.
When the CFO turned it on at his ask, his first click or key since the switch brings the same offer at once, titled The CFO turned AFK on, so a switch made on his words meets him before anything else; his next waits five minutes as usual.
The offer says who turned it on, with his words when the CFO did at his ask, and takes the focus itself, so keys he was typing press neither button, and Escape stays.
When AFK mode turns off while the page is open, the report of the stretch opens as one page over the board, read from `GET /api/afk/report`: who turned it on and off, how many of each thing there is, what was held with what was recommended for it and what became of it as it stands now, which he reads first, then each heading that holds something with its rows, each row linking to what it names when that is an https link and folding its evidence to two lines with Show more, then what each goblin finished and what was spent.
An answer is named by its goblin, since the log keeps a question's id.
Selecting a card or node opens the same goblin panel from either view: a header with the goblin, its plain status and icon actions, then a Terminal view and a Task view one tap apart on a pill at its top, Terminal first.
The CFO's header carries the toggle of AFK mode beside its status, in the Task view and the Terminal view alike: a small switch labelled AFK, in the board's green while it is on.
Turning it on asks first, in a question whose Cancel has the focus, and turning it off asks nothing; a refusal is shown under the header in the supervisor's words, and a switch that cannot be read shows off and says that a press on the toggle resets it.
The header that carries it wraps, so on a panel at its narrowest the toggle drops under the status rather than cover it, and it is there at every width.
While AFK mode is off and a report is kept, an icon button beside the toggle opens that report again.
The header's status is the one place the panel says the task's state (`frontend/src/task-words.ts` translates what the fleet wrote, at display time, and changes no record).
A pause, resume or stop whose lifecycle record failed reads Pause failed, Resume failed or Stop failed, whatever the goblin's own last report says, on its card, node and panel alike.
The Task view header shows one plain sentence under the status: the goblin's latest report without its leading state word (working:, waiting on ...:, blocked:, failed:, done:), split where a semicolon chained it, in sentence case, with a pull request link as PR #n, other links as their site, a path as the name it ends with and a commit hash left out, kept to whole sentences up to 280 characters; a paused task's sentence says what resumes it, from its pause condition, and that its last saved notes are kept when no fresh one was saved; a failed action says what failed and what to do next, with Open the log, which opens and shows Activity.
Under Working and Pause failed the header shows no sentence, at the Overlord's word on 2026-10-05 ("dont need text under working", "dont need text under pause fialed"); that sentence is the first line behind Details, the same at every width.
Details under the sentence shows the report or the supervisor's problems exactly as written; a read that fails says what could not be read in one sentence with the error behind Details.
The Terminal view header is compact, showing only the goblin, its status and the icon buttons, since the live screen shows the latest output.
The Task view holds the workspace, connections, changes, activity and commit history, each a section closed until it is opened; the Terminal view is that goblin's live native terminal, edge to edge.
The Task view opens with one action row under the header, the task's own controls as labelled buttons: Remove for a queued task, whose Start is on its card alone, and Pause or Resume, and Stop, for a task that has started.
A queued task's Adjust this task form follows that row, always open, with Save changes under its text box; the Adjust pencil is on the card only, where it opens the panel.
A pause or stop lists what it kept under What’s preserved, each as a short label without its path or hash, and what it ended under Stopped resources; it repeats neither the status nor its reason.
The panel sends no note to the CFO: the supervisor still accepts the `note` action of `POST /api/tasks/adjust` and a snapshot's task notes still show under the form, but nothing on the board files one.
The CFO's Task view holds its workspace and connections and then every queued task, the same list as the Tasks column, in the same order, with the same memory meter, drag, keyboard moves and Start; an order or a start made in either shows in both.
Board and Orchestration both open a goblin, and the CFO, on its Terminal view, and an alert opens its task on the Task view, where the alert's reason is; Orchestration's Terminal view defaults to the registered CFO; once opened, both views stay mounted, so switching keeps scroll position and selection.
There is still no standalone message composer: typing happens in the terminal itself.
Open in VS Code and Open folder require a deliberate click and resolve the selected goblin's fresh, isolated Git worktree.
The API accepts task identity and an editor enum, never a browser-provided path or command; it starts Code.exe directly with literal arguments and removes Electron Node/development flags from its inherited environment.
Successful launch means the application was requested, not that a window was observed.
Queued tasks show their known project and Not started yet, without querying nonexistent task metadata, and their panel is the Task view alone: the Terminal pill appears once the task has a terminal.
Operational wake records remain intact; only a deliberate CFO question is offered to the Overlord, and the Command Center opens when he chooses it.
Task-semantic goblin avatars are presentation choices, not inferred native role evidence; a task no keyword classifies gets a stable artwork of its own instead of the shared app icon.

Orchestration nodes use explicit native parent/root IDs and the launch environment's reported relationships.
A live task record no native hook reported hangs under the CFO, because cfo spawn is how every task record was dispatched: under the reported CFO session, or under a supervisor root drawn when none reported, which opens the CFO terminal and names a stale registration.
Completed history and unstarted briefs stay on the board, not in the tree.
Directory names and temporal proximity never establish parentage.
Replayed, stale, reparenting, or cyclic input cannot silently change the tree.
Unknown and retired parents remain visible, and task dependencies are rendered separately from spawned/delegated links.
Unreported child models remain unreported even when their owning task declares a model and effort.
Dragging a card or using Alt plus an arrow key changes only its saved browser position; connectors retain their reported parent identity.
The tree fits and centers itself in the visible canvas, scaled up to fill it but never past 125% and never below 35%, and refits whenever the panel opens or closes, the window resizes, a goblin appears or leaves, or a dragged card is dropped.
Zooming, or panning a view that actually scrolls, stops the automatic fit until Fit is pressed, which restores it.
A family of more than three leaf goblins wraps into two rows, the second offset by half a card so its connectors drop through gaps in the first instead of behind a sibling.
A goblin waiting on another sits in the row under the one it waits on, half a card over, and the dashed line between them runs straight down what the two cards share; a sibling with nothing under it gives up that place and takes the nearest free one, and a chain or a cycle of waits keeps its family places.
Only a card the Overlord moves is saved, so the canvas keeps arranging every card he has not placed himself.
A card he placed stays where he put it, and an arranged card whose place it covers takes the nearest free place, along its row first and then the rows below, so the canvas never arranges a card onto another.
A connector pulses for a few seconds when its goblin reports a new status line or files a wake record; a report that lands while the board is hidden never plays later.
Arrange resets positions, and storage failures remain visible.
Narrow screens use a collapsible nested list that names the actual parent when indentation is capped.
A child without its own reported native transport explains that limitation without borrowing its owning task's terminal or model.
Bounded accepted-message receipts produce a brief travelling connector pulse and receiving-card glow where exact caller native identity proves the reported parent.
Native-creation receipts instead produce a quiet birth highlight on that proven parent branch plus the child-card glow and entrance; creation is never rendered as a message receipt.
Every pulse, card glow and birth highlight is its own layer over the connector or card, which plays out over the effect's whole life and fades before it is removed: a pulse's dash keeps travelling until the fade ends, so none freezes in place or vanishes at full strength, and the connector underneath never changes.
Each effect runs on the clock of the report it shows, so one drawn late, on a branch expanded mid-pulse, joins its animation where it is and still ends on time.
A newer report on the same connector plays its own pulse while the earlier one finishes and fades.
The caller convention is `CFO_SESSION_ID` plus `CFO_SESSION_HARNESS`, with native `CODEX_THREAD_ID` fallback; same-harness or directory/time resemblance never establishes a sender.
When sender identity is unavailable, only target evidence is shown.
Each effect expires independently; initial load, reconnect, hidden-tab return, instance replacement and replayed receipts never fabricate new activity.
Reduced motion uses a short static outline instead of movement.

Changes is closed when a panel opens and reads nothing until it is opened: then it reads the change set's file list and shows its summary first, with Files on GitHub linking a GitHub pull request's Files view, and each file is a closed disclosure whose diff is read only when it is opened; no diff is read in the background or on a timer.
Changes presents stacked file disclosures with lazy syntax-highlighted unified, split and code previews.
A file over 256 KiB shows its changed lines in unified and split views, with a note that the code preview is off; every Git read behind a preview is capped at 1 MiB.
Select a visible old/new line or contiguous range, including unchanged context, by its line number, which shows a comment icon on hover and focus.
Dragging across diff lines opens the same comment box for the lines it covers, and a double-click or triple-click still just selects text to copy.
A comment box floats beside the selection without moving the diff; Enter sends it, Shift+Enter starts a new line and Escape cancels.
Sending queues a durable review record containing the exact file, side, range, revision, HEAD and diff fingerprint, and the box shrinks to a chip whose check marks show delivery.
The server validates those coordinates and derives bounded selected code; it never sends a review comment to the worker.
Admission first proves the registered primary CFO live with the same process, pane, agent and terminal checks as delivery, then pins its fingerprint; a stale registration is refused and nothing is queued.
An identical retry answers from its durable record without a new probe and keeps its pinned recipient even if the primary changes.
An empty untracked file has no selectable line, while a newline-only file has one empty line.
New annotations use only the verified CFO normal native message channel, and success requires transport acceptance.
They do not append a second actionable wake record; existing historical wake records remain untouched and old unpinned reviews are not adopted or replayed.
Review remains available during gate custody, while native worker terminal input retains the custody checks.
Drafts bind the task session identity at selection and survive view, file and recipient changes without silently moving to a restarted task.
An unchanged submitted payload keeps its request ID after an ambiguous HTTP failure; an SSE outcome is displayed instead of dispatching it again.
An interrupted external delivery becomes uncertain and is not replayed automatically.

A goblin panel's Terminal view shows a task in a native terminal from its host, as described after this Herdr view, and the CFO and a task in Herdr through Herdr.
A panel with no terminal to show says why without opening any view: No CFO is running while no CFO runs (the snapshot's `cfo_runs`), This task has not started yet for a queued task, and This child has no separate terminal, with **Open owning task**, for a child session.
The installed Herdr build `0.9.0-preview.2026-09-08-62431dbd033b` exposes `terminal session observe` and `terminal session control` over NDJSON.
The browser renders its real ANSI screen frames using xterm, loaded the first time a panel shows its Terminal view.
A goblin panel's Terminal view of a Herdr pane is a live view of the pane, and it never resumes or answers an agent.
An open view keeps its pane live and sized to the panel at all times (the Overlord: "it should always be live"): once the view is shown it takes the pane's controller (`terminal session control --takeover`) and sizes the pane to the panel at the chosen text size, 20 px unless Ctrl+Plus, Ctrl+Minus or Ctrl+0 chose another, which native terminals share, and keeps it whether or not the board's window has the focus and while another view is shown; a Herdr window shows the pane at the board's size.
The panel keeps an even inset around the screen, and a panel that changes size asks for the grid that fills it at once, then at most every 40 ms while it keeps changing and once more when it holds still.
Dragging the divider between the board and the panel is the exception: every resize makes the pane's program redraw its whole screen, so while the divider moves the screen keeps its grid, shown scaled into the panel, and the view asks for the panel's grid once, when the drag ends; the board lays itself out at most once an animation frame during the drag.
`tests/acceptance/terminal_drag.mjs` drags the divider 400 px out and back beside a live terminal and fails when the view resized the pane before the drag ended; on 27 September 2026 the view on main asked for 53 to 64 resizes during one drag, and now asks for none during it and at most one on release.
Only another client taking the pane over, such as Open in terminal, makes a view show the pane at the size Herdr lays it out at, until the Overlord comes back to the board or types in it.
A view that sized the pane, however it ends, closing the board included, has the supervisor return the pane to that size, as a Herdr window does when a controller leaves; with no Herdr window open, nothing else would.
A Herdr window that shows the pane takes its size back once the controller leaves.
Taking control sends the program nothing but its size: attaching, resizing, taking over and detaching sent a program that logs every input byte nothing at all, and a test proves the view sends the pane only its size until the Overlord types.
A switch opens beside the view and takes over on its first whole screen, after any input on its way, so the screen never blanks and no key is lost; a size refused, such as a goblin whose review gate owns it, leaves the view showing the pane at its own size, and a review gate that takes a goblin over while the board sizes its pane ends that sizing view on its next check, which does the same.
Shown at its own size, the pane's frames arrive as Herdr lays it out; the view sizes its font to show the whole pane in the panel, bound by whichever of the panel's width or height runs out first and measured from the cells xterm drew, with no floor, centers it and anchors it to the panel's bottom so the input line stays there; the panel never scrolls, nothing is cropped and no scroll bar is drawn.
Rows are drawn at the font's own height, xterm's default line height, and a screen whose rows round up past the panel steps down half a pixel at a time until it fits whole; a 120 by 40 pane is 14 px maximized in a 1600 by 1000 window and 22 px in a 2560 by 1440 one.
A view is refused when Herdr reports no size for the pane; when Herdr lays the pane out at a new size the view ends, and the panel reconnects on its own to show it whole.
Typing needs no separate step: the view's lease takes keys, escape sequences and one bracketed paste at a time, in order, each typed into that exact pane over the Herdr session's socket.
Herdr's socket answers one request on a connection and then closes it, so each input is one pipe round trip of about a millisecond, and no key starts a process; the supervisor reads where each session's socket is from `herdr status` once and keeps it, reading it again only after the socket cannot be reached, and a view is refused when the socket cannot be found.
The supervisor also reads the session's snapshot and a pane's process info, which prove a view's pane, on that socket in milliseconds rather than through a herdr process of about a second each, and reads through the herdr command when the socket cannot be reached or its answer does not decode.
The view proves its pane, terminal, process and gate custody in full when it opens and on every five-second tick; an input starts no process to prove it again, and only rereads the task's record (or the CFO's registration) and checks that the verified process is alive, so a changed generation, pane or registration or an exited process is refused on the next key and anything else on the next tick.
A tick's Herdr commands run beside the screen, never in its way, so a frame, and with it the echo of a key, is never held behind a tick.
A paste reaches the supervisor in pieces of at most 64 KiB, which its lease joins into one paste before anything is typed, so no key or other command lands inside it.
The joined paste, with every escape character inside it removed so pasted text cannot end the paste early, is typed in one request: `pane.send_input` on an observing view, which applies the program's own paste mode, or the control stream on a sizing view.
A paste is limited to Herdr's 1 MiB request, less 1 KiB for the request's envelope, measured after JSON encoding; the view refuses a larger paste before sending any of it, the lease ends the view without typing a paste that grows past the limit or is interrupted by any other command, and an input Herdr refuses ends the view with an unknown outcome.
A refused input, or one whose outcome is unknown, ends the view with the reason in plain words; nothing is resent, and reconnecting starts from a fresh full screen.
The view never scrolls a pane that keeps scrollback: Herdr sends a view only the pane's live screen, never its history, and `pane.scroll` would move the view of every Herdr window on that pane without reaching the board.
So the live screen always follows the pane's bottom, and scrolling up with the mouse wheel, anywhere over the panel and not only over a screen that fills part of it, or Shift+PageUp reads the pane's last 3,000 lines through `POST /api/terminal/history` (Herdr's `pane.read` of its recent output, with colors, on the same socket) and shows them in a terminal of their own over the live screen, at its size and font.
The history scrolls by itself with the wheel and Shift+PageUp and Shift+PageDown; scrolling down at its bottom, Escape, or typing returns to the live screen, and what is typed reaches the pane.
A history read types nothing, so a gate that owns the pane stops typing but not reading history; it reads only the pane the view was verified on, and at most 5,000 lines.
A Claude Code pane whose history holds no more lines than its screen, as Claude Code's fullscreen interface draws in the terminal's alternate screen and keeps no scrollback, scrolls itself instead: every turn of the wheel sends it Herdr's own wheel scroll (`terminal.scroll` with source `wheel`, at most 50 lines, never any text), which the supervisor refuses for any pane whose agent is not `claude`.
Herdr hands the program one wheel notch for each such scroll, whatever number of lines it names (measured on a fixture: scrolls of 3, 4, 12 and 50 lines each arrived as one notch), so turns are never joined into one scroll.
The view judges whether a pane scrolls itself as soon as each connection draws its first whole screen, and again whenever the screen's rows change, from a read of two screens and one line, which tells a pane with no scrollback from one with some exactly as a read of its whole history does.
A judgement holds only for the rows it was read at, since a pane asked for a new grid while being read may answer at that grid.
The wheel never waits for that read, except for turns in the moment before the judgement at the screen's rows lands, which wait and are then scrolled rather than dropped.
The view trusts the judgement for 30 seconds and while its connection lasts; after that, the next turn scrolls at once and the history is read again beside it, and a pane that has since built scrollback returns to the read-only history.
Scrolls go out one at a time, and when the wheel turns faster than they can be sent, at most two wait behind the one on its way and the rest are dropped, so the pane stops soon after the wheel does; a turn the other way replaces the waiting ones.
Scrolling such a pane counts as working in the board, like typing: a view that does not hold the pane takes it first and scrolls it by that turn once it holds it; a take that is refused, such as a goblin whose review gate owns it, is not asked for again until the Overlord comes back to the board or types, and the wheel says why instead: "A review gate owns this goblin's pane now; scroll it in Herdr."
Herdr's frames carry no mouse modes, so a click never reaches the program and Claude Code's "Jump to bottom (ctrl+End)" note cannot be pressed; instead, a plain click (under 4 px of movement, not a drag or a selection) on a pane the board has scrolled up and could scroll now sends Ctrl+End, which jumps it back to its bottom, and Ctrl+End typed in the view does the same.
The jump waits out the double-click interval, 300 ms after the release, and a further press in that time cancels it, so a double-click or triple-click selects the word or line it aimed at; a click on a pane the view does not hold, such as one a review gate owns, does nothing.
A NUL key such as Ctrl+Space is typed like any other key.
Shift+Escape moves keyboard focus out of the terminal to the panel's pill; ordinary Escape stays with the pane.
Releasing a drag selection copies it to the clipboard, the way Herdr does, and Ctrl+Shift+C, or Ctrl+C while text is selected, copies the current selection.
Closing, switching, disconnecting or restarting invalidates the lease; reconnection starts with a full screen frame, never replayed input.
At most eight views are open, frame gaps disconnect, and oversized UTF-8 paste is rejected before sending.
Adjacent printable keystrokes coalesce into bounded ordered inputs; control keys and paste wrappers stay inputs of their own.
Until the first frame is drawn a full-pane state says the terminal is connecting, and a view that has stopped says why in a pill with Reconnect beside it.
The terminal carries no options menu or help text; xterm's default input mode accepts InsertText and IME Unicode, and paste works.
There is no second model session or generated reply.
The native interface exposes rendered screen updates rather than original historical PTY bytes, and omits Kitty keyboard negotiation, graphics and host mouse notifications.

A task whose record names the `native` backend runs in a `cfo host` of its own, and `GET /api/terminal/native?task=ID&generation=GEN&token=TOKEN` relays one view of it over a WebSocket; a goblin panel's Terminal view opens it for every such task, and the snapshot names each task's `backend` so the board knows which view to open.
A CFO registered in a native terminal is relayed the same way with `?cfo=TERMINAL&token=TOKEN`, and the snapshot's `cfo_terminal` names that terminal (native terminal `cfo` while a CFO is starting there unregistered), empty while the CFO runs in Herdr, so the CFO's entry in the panel shows the native terminal instead of a Herdr view that cannot show it.
The view is refused unless the registration names a live CFO in exactly that terminal, or, with no CFO registered, the terminal is native terminal `cfo` and its host answers, and it closes once that stops holding, so typing never reaches a terminal the CFO has left.
The upgrade needs the board's own origin and the board's token in the query, since a browser cannot set a WebSocket header, and anything else is refused with 403.
Every later refusal closes the socket with its reason, which a browser can read: a replaced generation, a task that runs in Herdr, no running host, a host that did not answer, or 32 native views already open, a limit of their own apart from the eight Herdr streams.
The view is bound to its terminal once, by the host's pipe, whose server process must be the host the record names, so a key costs no check and starts no process.
The first message is the text `{"type":"history","bytes":N}`, the number of output bytes that follow as the host's history, even when it is empty.
Output arrives as binary messages, the history first; typing goes back as binary messages, and a resize as the text message `{"type":"resize","cols":C,"rows":R}`.
The pseudo console repaints its whole window on every resize, even to the size it already has, so a view replays the history and then sends its size, and its screen is whole however much of the history the host still keeps; the host starts a replay at the next line or escape sequence past its 4 MiB limit, never inside one.
Every view of the terminal, the one that sent the resize too, is told each size the terminal took as `{"type":"size","cols":C,"rows":R}` at its place in the output: after what the terminal wrote before the resize and before what it wrote after, whichever viewer resized it, `cfo attach` in an Open window included, and the history carries the sizes it was written at the same way.
A view changes its grid only there, once xterm has parsed the output before it, never ahead of the terminal, so output is always drawn on the grid it was written for rather than wrapping and landing at the wrong columns.
A view draws another viewer's size until it is typed into, which sizes the terminal to it, and `cfo attach` takes the terminal back the same way with its next key.
Nothing else in the view takes the size back: not the view becoming whole or coming into sight, its font loading or its panel resizing.
Only a size the terminal takes after it took the view's own counts as another viewer's; until then the terminal is still on its way to the view's size.
xterm answers a program's terminal queries, such as a cursor-position or device-attributes request, through the same input path as typing, so a view that answers one takes the terminal's size back just as a key would.
A size under 20 columns or 5 rows, measured while a panel was hidden, or over 1000 columns or 500 rows is ignored and announced to nobody.
A host tells a viewer sizes only when its handshake asks for them; for a host started before hosts told sizes, the relay tells every view the size each of its own resizes gave the terminal, at the end of the output it had read by then.
A view acknowledges the output it has drawn with `{"type":"ack","bytes":N}`, N counting every output byte so far: the relay sends a view at most 1 MiB beyond what it acknowledged and keeps reading the host, and a view that falls 8 MiB behind is closed with code 1013 and "The view fell behind the terminal's output.", so the host never waits on a slow window and a view that stays open never loses a byte.
Gate custody and the task's generation are checked when the view opens and on every five-second tick; a key sent under custody closes the view with the gate's reason and is not typed, and a resize sent under custody is ignored, so the terminal keeps its size until a resize arrives after custody ends.
The terminal's end closes the view with its exit code in the reason.

Native hosts explicitly disable Windows execution-speed throttling for themselves, their new system console server, and the suspended terminal process before it runs.
A hidden console has no foreground window to earn interactive scheduling automatically; on a loaded hybrid processor, automatic scheduling can leave it competing on saturated efficiency cores while performance cores are parked.
The policy changes neither process priority, processor affinity nor the machine's power plan, and preserves other process power controls.
The console server is selected from direct children created within a serialized `CreatePseudoConsole` call, with its creation time and system image checked through the open handle used to set the policy; existing consoles are left alone.
The terminal's existing job applies interactive scheduling to automatically managed descendants, including a harness launched through cmd or node, while preserving a descendant's explicit EcoQoS choice.
This intentionally gives the terminal's ordinary process tree an interactive default, including background tools that have not chosen their own power policy; it can use more processor power than Windows' automatic background policy.
Processes that break away from the job are excluded, and every descendant is checked against the exact job through its open handle before any change.
Job notifications handle new processes, with job-local reconciliation at most once per second when input arrives because Windows does not guarantee notification delivery.
Descendant scheduling errors are logged and cannot discard terminal input; required host, console-server and initial-process policy failures refuse startup.
The latency regression uses one native console event per read, as libuv does, and checks key p95 below 50 ms, the second slowest of 40 keys within 250 ms, no key past a second, and an ordered 2,000-character burst within two seconds during idle and continuous output.
One slow key in a run is a hosted runner's scheduling noise (504 ms seen with the test's job to itself); two slow keys fail, and a failure names the slow keys by number.
Installing a build or restarting `serve` leaves existing hosts running their original code; the persistent policy takes effect in newly launched hosts, so resume each existing session only when its active work permits a host restart.

Every native terminal also runs an input waker: the host's own program again, attached to the terminal's pseudo console and told each time the host writes input or resizes the terminal.
Windows' inbox conhost (10.0.26100, as Windows 11 and GitHub's runners ship it) can queue input without waking a program that waits for it in a blocking read, `ReadConsoleInputW` or `ReadConsoleW`, while another of its threads prints, and the input then sits unread until more arrives; microsoft/terminal#18228 fixed that wakeup in a conhost Windows does not ship yet.
The waker counts the console's unread input 25 ms after the last write and again at doubling intervals up to 1.6 s, and while any is unread it writes a menu event, which wakes the reader and which every reader skips, so input a program was left waiting for arrives within about 25 ms and a program that reads promptly never sees one.
It outlives Ctrl-C and Ctrl-Break, ends with its console, and notes its wakes in the host's log at most once a minute.
`TestTypedKeysReachAProgramThatPrintsWhileItReads` types 3,000 keys into a program that echoes each while it prints and fails on any key left unread for 2 s, and `TestInputLeftUnreadIsFollowedByAWakeEveryReaderSkips` checks the wake itself.
Once a terminal has read one key event written as Windows sends it, as `cfo attach` writes every key, conhost holds an input that ends in Escape, or in Escape and a character that starts a longer sequence (`[`, `O`, `P`, `]`, `X`, `^` or `_`), as a sequence still arriving: an Escape typed alone reached the program only with the next key, as Alt and that key, and Alt+P or Alt+] swallowed every key after it.
The host holds such an ending back for up to 100 ms: the next input takes it on when it comes that soon, as the rest of a key event `cfo attach` split across two writes does, and otherwise it is written as the Windows key events it stands for, Escape or Alt and the character, which conhost delivers at once; `TestAnEscapeTypedAfterAWindowsKeyEventReachesTheProgramAtOnce` and `TestAKeyEventSplitAcrossTwoWritesReachesTheProgramWhole` cover it.

The board draws a native terminal with xterm at the panel's size: the view measures the cell xterm drew and fits the columns and rows the panel holds with an even inset of at least 10 px, and sends them as the resize, so the program and the view agree on the size.
The spare width is split between left and right, the bottom keeps the same space, so the last row, the input line, sits that far from the panel's bottom, and the spare height, under a row, goes above the grid; no space is kept for a scroll bar, since none is drawn.
xterm draws with its WebGL renderer, and with its DOM renderer where WebGL is unavailable or its context is lost.
The font starts at 20 px; Ctrl+Plus and Ctrl+Minus step it between 12 and 28 px and Ctrl+0 restores it, saved in the browser, and a new font size resizes the pseudo console, so the terminal gains or loses columns instead of shrinking its text.
The terminal keeps 5,000 lines of scrollback and the wheel scrolls it; the panel around it never scrolls.
Output is written through xterm's own write queue and never re-renders the page.
A chunk as large as one host read, 32 KiB, is part of a larger redraw, so the view opens a synchronized update (DECSET 2026) before it and ends it when the redraw's short tail arrives or the stream has been quiet for 8 ms, and xterm paints the redraw as one frame; a smaller chunk, such as an echoed key, is written as it is.
The update's markers only go where the stream sits between escape sequences and characters and outside any synchronized update the program opened itself, and xterm ends an update left open after one second.
Each connection draws into its own xterm, kept out of sight until the history, the repaint its size asked for and any open update are drawn, so the panel never shows a blank, cleared or half-drawn screen; until the first one is whole a full-pane state says the terminal is connecting.
A view that fell behind, a restarting board or a dropped connection reconnects on its own up to five times, keeping the last screen in place with a Reconnecting note until the new connection is whole, and does the same while the board's own connection is down; any other close keeps the last screen in view with its reason and Reconnect in a bar across the bottom.
A paste goes as it is typed, in pieces of at most 64 KiB, in order.

Every native terminal the Overlord opens stays live while the board is open, one xterm and one socket each, hidden rather than unmounted, so switching only brings another into sight; the three most recent Herdr views stay live the same way, and each may briefly hold a second stream while it switches to or from sizing its pane, so another window still gets some of Herdr's eight streams.
The shown terminal fills the panel, with no list beside it; the Overlord picks the goblin on the board.
Ctrl+Alt+Up and Ctrl+Alt+Down step through the terminals, the CFO first and then each goblin with a terminal, and Ctrl+Alt+1 to Ctrl+Alt+9 jump to one in that order, matched by key position; the board catches them before a terminal sees them, except while a dialog such as the Command Center is open, and a switch hands the terminal the keyboard, a Herdr terminal included.
A key typed with AltGr, which Windows reports as Ctrl+Alt, stays the terminal's, so a layout that types a brace or bracket with AltGr and a digit keeps it.
A divider between the board and the panel sizes the panel, keeping at least 360 px for the panel and 280 px for the board, and a maximize button gives the panel the whole window; every view opens beside the board until it is maximized, the Board's terminal view and its Task view each keeping the last choice and the Orchestration view following the Task view's so its graph stays beside the panel, and the width and both choices are saved in the browser, a width saved on a wider window is held to the same bounds, and on a narrow window the board and the panel stack and the divider is hidden.
The first open of the board in a browser, which is one that records no first open (`cfo-first-open` in its local storage) and keeps neither a panel width nor a maximize choice, arranges the panel once: with a CFO running it shows the Board with the CFO selected, its panel on the Terminal view beside the board at the default width, not maximized, and the keyboard in that terminal.
With no CFO running the first-run page comes first, and Start lands on the same view.
On a window too narrow for two columns (40rem or less) the board and the panel stack, so the first open chooses the same view but leaves the keyboard and the scroll where they are and the board stays in view; between that and 1410 px the two sit side by side with the board stacked, since its three columns need 961 px beside the panel.
The record is written as the board first shows, so nothing arranged later is undone, and a browser that kept a layout before the record existed counts as having had its first open.
The panel of anything but the CFO (a queued, running, paused or completed task, or a session on Orchestration) has Back in its corner where the CFO's own panel has Close: a button with a back arrow and its name, which returns the panel to the CFO's panel on the view that panel last showed, Task or Terminal (Terminal when it has shown neither), keeps a maximized panel maximized, and hands the keyboard back to the card or node the panel was opened from, or to the panel when that is hidden behind it.
Escape outside a terminal does what the corner button does: back from a task's panel, close on the CFO's.
The panel's top row is one line at every width the panel can take, 360 px at its narrowest: the Task and Terminal switch, when the panel has both views, then its controls (Open in terminal and Maximize or Restore today), then the corner button, Close or Back.
Nothing in the row overlaps or is cut, and nothing in it is drawn smaller to fit: an icon button stays 44 px square.
The switch and the corner button always show; a control the row cannot hold at full size goes into More, a menu at the corner that lists each with its icon and its name, opens on its first item, moves with Up and Down and closes on a choice or on Escape, which hand the keyboard back to More, or on a press anywhere else; a choice that gives the row room for every control again, so that More goes, hands the keyboard to the panel, and Escape still closes it or goes back.
The switch gives up its words, Task and Terminal, only when the row cannot hold them even with every control in More, and then shows its two icons, each named by its tip and its accessible name; with its icons the row holds as many controls as fit again.
The row is `PanelRow` in `frontend/src/panel-row.tsx`, used by every panel, and what fits is `rowFit` in `frontend/src/panel-fit.ts`.
A control is a `PanelControl` (an id, a name for its tip and its menu item, an accessible name, an icon, an importance and what a press does); its importance is written beside the others in `PANEL_IMPORTANCE`, the highest staying in the row longest, and `App.tsx` builds the list.
Until 2 October 2026 the row gave the controls half of what the switch left, so in a panel dragged narrow they were drawn over the switch.

Every terminal pane, native or Herdr, shows a voice bubble in its bottom-right corner, in a strip of its own under the terminal, so it never covers the terminal's text.
It is drawn like the board's goblin alerts: a microphone in a stepped pixel frame, outlined in Bone while idle and in Moss while it records, and its first-visit hint and recent messages open in the same leather dialogue frame.
That frame is at most 400 px wide and never wider than its terminal less 28 px, so in a panel dragged narrow it is whole; until 2 October 2026 it was sized by the window, and a narrow panel cut off its left side.
Holding Ctrl+Shift+Space in a terminal, native or Herdr, dictates into it through the speech model the supervisor runs on this PC, in a browser tab and in the desktop window alike.
At each press the board opens the microphone once and records that track with the browser's own recorder, and while the keys are held the bubble's bars are recent samples of that same capture's level; nothing runs while the bubble is idle.
Releasing any of the three keys decodes the recording to one channel at 16 kHz, posts it as a WAV to `POST /api/dictation` on the board's own address, and types the words the supervisor answers as one line through the terminal's paste, so nothing is sent until Enter; the page asks nothing else of anyone while it dictates.
A dictation typed right after another, with no key pressed in the terminal between them, starts with a space, so the two do not run together; the recent dictations keep the words alone.
The microphone closes and the bubble goes idle at the release, without waiting for the words, and the next dictation can start while the words of the last are still on their way; the words of each are typed when recognition finishes.
A native terminal's paste follows the program's own bracketed paste mode, and the Herdr view, whose screen is redrawn from frames, always sends a bracketed paste, as its clipboard paste does.
Releasing the keys anywhere on the page, the window losing focus or the page being hidden also stops listening, so the microphone never stays open once the terminal loses the keys.
A blocked or missing microphone or silence is explained in a note for six seconds, and so is whatever the supervisor refuses a dictation with, in the supervisor's own words: a machine with no memory to spare, a board reached from another machine.
While the first dictation sets up the speech model, the note stays and follows it, asking the supervisor each second: how many of its 125 MB have arrived, then that it is being checked and unpacked; it ends with "Dictation is ready: hold Ctrl+Shift+Space and speak.", or with why the set-up failed, which stays until the next dictation.
The words of a dictation the set-up refused are not kept, and the note says to dictate again when it is ready.
While it listens the bubble's tip names the model, and the foot of its list says what heard the recent dictations.
A browser with a speech recognition of its own offers **Use this browser's speech recognition instead** there, off until it is ticked and kept in that browser: Edge and Chrome recognise speech in their vendors' online services, so with it the audio leaves the machine while the keys are held, and the tip and the list say that this is what listens. The desktop window has no such recognition and offers no choice.
Clicking the bubble lists the pane's five most recent dictations, newest first, for that pane's goblin or the CFO, kept in this browser only, ten each for the twenty used last, so a relaunched goblin keeps its own; the board reads no other dictation app.
Each has Copy and Paste into this terminal, which pastes as dictation does and hands the terminal the keyboard back, and Escape closes the list.
The bubble's tip names the shortcut, and a first visit shows a hint about it once, until it is dismissed.

The supervisor's speech engine is what makes that dictation free and local.
`config/voice.json` is its one setting: the engine and the model, each a download pinned by its address and SHA-256, the files of it that are kept, and the engine's library and how it loads the model.
The build carries that setting, so an install of a newer build brings a newer pin; a home that keeps a `config/voice.json` of its own uses that one, and one that cannot be read leaves the supervisor without dictation rather than falling back.
The pin today is NVIDIA's Parakeet TDT 110M (CC BY 4.0) run by sherpa-onnx 1.13.8 (Apache 2.0) on ONNX Runtime (MIT), both from the sherpa-onnx project's releases, 125 MB to download.
`POST /api/dictation` takes a WAV sound of at most 8 MB and answers its words, and refuses any other body as a bad request before reading it, only from the board's own page on this PC, at 127.0.0.1 and through no proxy, so a board shared over a tailnet dictates nothing.
The first dictation downloads what is missing into `caches/voice/` under the home and answers that it is being downloaded, with how many of its megabytes have arrived; `GET /api/dictation` says which model listens and whether it is ready, missing with why the last download failed, or being fetched with how far it is.
Each download's size is pinned with its checksum in `config/voice.json`, so the first answer already says how big it is.
A download is kept only when it matches its SHA-256, holds every file named, and no program or library in it is linked against a Windows networking library; otherwise nothing of it is kept and the answer says why.
Offline, the answer names the address to download and the file to save it as in `caches/voice/`, where it is held to the same checksum.
The first dictation starts the engine in a process of its own, a hidden `cfo voice-worker` that loads sherpa-onnx's library and the model once, and the next dictations go to it while it is loaded, one at a time, through a pipe: the sound is never written to a file, and the worker gets none of the fleet's environment beyond what Windows needs to start it.
On this PC the first dictation takes about 1.4 s, most of it loading the model, and each after it about 75 ms for a short line; the loaded engine holds about 250 MB.
It is ended after 2 minutes without a dictation, when it breaks or a dictation is abandoned, when there is no room for it, and when the supervisor stops, and the next dictation loads it again; a fault in the engine ends that process, never the board.
It needs 1 GB of free memory and of free commit and says so when the machine has less, which is far under the fleet's 4 GB floor on purpose: dictation is how the Overlord types, and it must not refuse because the fleet is busy.
`cfo doctor` names the model, its version, the engine and whether they are fetched, and how many megabytes the first dictation downloads when they are not.

Key-to-echo latency, measured with `tests/acceptance/terminal_latency.mjs` against the example fixture on 25 September 2026: the Herdr view on main e6f7ea97 took p50 74 ms and p95 592 ms with 3 of 100 keys unechoed after 5 seconds and 4.6 s to a live screen, and the native view p50 24 ms and p95 34 to 36 ms with none missed and 0.4 s to a live screen.
With synchronized redraws and the 20 px font, measured with the DOM renderer in headless Edge, the native view took p50 28 ms and p95 41 ms with none missed.
`tests/acceptance/terminal_checklist.mjs --url <board> --title <task> --fill <command>` opens a task's terminal from its card in headless Edge, runs the command in it once for history, and checks the terminal with real wheel and key input: the inset on each side at every size Ctrl+Plus and Ctrl+Minus reach, any scroll bar in sight, history on the wheel and the way back down, and typing in the history returning to the bottom.
On 27 September, against a PowerShell terminal on a scratch home, the view that reserved a scroll bar's width kept 10 px left, 16 to 22 px right, 6 px above and 2.8 to 28.8 px under the input line; the even fit keeps 10 to 16 px left, right and below at every size from 19 to 22 px, with the rest above, and the other checks pass as before (`docs/evidence/cg-native-desktop/terminal-checklist`).
Switching, measured with `tests/acceptance/terminal_switch.mjs` over 30 round trips between two native terminals: when each switch remounted the view it took p50 44 ms and p95 55 ms from the click to the other terminal drawn whole, with 47 blank frames; with every opened terminal kept live it takes p50 16 ms and p95 21 ms with no blank and no half-drawn frame, and a first attach shows no half-drawn frame.
An idle board draws nothing: a working goblin's status dot wears a still ring rather than an endless pulse, because a pulse restyles and repaints the page on every frame for as long as the board is open.
`tests/acceptance/board_idle.mjs` measures an open board while nothing happens on it and fails when the page runs an endless animation or restyles itself more than 30 times a second.
On 27 September 2026 the pulse kept the live board (five working goblins, 2560 by 1440, headless Edge) at 423 to 431 ms of GPU-process CPU and 201 to 226 ms of renderer CPU every second, with 113 to 141 style recalculations a second; without it the same page took 15 to 28 ms, 27 to 35 ms and 7 to 17.
`docs/evidence/cg-board-ux/pr1b/side-by-side.mp4` records one Claude Code session shown at once in the board and in Windows Terminal through `cfo attach`, while the board switches to another native terminal and back and then to the CFO and back: the Claude Code answer arrives while the board shows the CFO, and is already drawn when the board switches back.
The CFO in that recording still runs in Herdr, so its first open shows Herdr's connecting state over an empty pane, which is not the native view.

CFO transport reads the `state/primary.json` registration and binds each queued message to its fingerprint.
The primary CFO writes that registration itself: Claude's SessionStart hook does it after the digest settles custody, and a Codex or pi CFO runs `cfo register`, which the first prompt goblins starts it with tells it to do.
`cfo register` refreshes it by hand.
Registering the same process in the same terminal again leaves the file byte-identical, so a compact, clear or resume keeps the fingerprint pending questions, reviews and answers are bound to.
A CFO registers from a native terminal, a `cfo host` that tells the program it starts which terminal it is through `CFO_HOST_ID`.
Registration trusts no variable alone: the terminal's program, named by its host's record, must be one of the caller's own ancestors, or the caller must carry the terminal's proof value (see [Goblin questions](#goblin-questions)), the host must answer on its pipe, since a host that was killed leaves its record behind, and that harness must hold the home's session lock, taking it only when no live session does.
The registration names that terminal, and it stays valid while the host's record names the registered process as the terminal's program.
Plain `cfo register` names the harness from that program: `claude.exe` is Claude Code, and the `cmd /c codex` or `cmd /c pi` a native terminal runs an npm script shim through is named by the name cmd runs, read from cmd's own command line; any other program is refused.
A session in no native terminal, a Herdr pane included, registers nothing, and a registration an older build wrote for a CFO in a Herdr pane reads as not reachable: the board delivers nothing to it and takes no proof from it until the CFO is started again in a native terminal.
A message for the CFO is typed into its terminal once, then Enter submits it, over a delivery connection of its own: the host acknowledges each part once it has written it into the terminal's input, and it is never typed again.
The board shows it delivered only once the CFO's own prompt hook, which names the native terminal its harness runs in, reports taking it.
A screen turning to work is no proof, since Enter may have chosen a dialog's option instead.
A CFO inside a turn, or one slow to start its next, reports only later, so a message its hook has not reported within five seconds is sent, not failed: its action stays on its way (`status: running`, with `awaiting` naming the terminal and when it was submitted) and reads "Sent. The CFO reads it when its current turn ends." with one check.
The supervisor then settles it: the hook's report makes it delivered, with two checks, however late it comes.
It warns only when the message can no longer arrive or plainly has not: the terminal closed, or its screen has shown no turn for three minutes with still no report, counted from the last look that showed a turn, so a message behind a long turn never warns.
The warning is the action's `advice`, written for the Overlord (open the terminal and press Enter if the answer is waiting in its box, or type it there), and the action reads `uncertain`; a report that comes after the warning still delivers it.
A second answer is refused while the first is sent, so nothing is typed twice, and a supervisor restart leaves a sent message sent.
A goblin's answer that waits behind its turn is settled the same way, by its task and spawn generation, and its review item reads delivered only then.
A host started by an older cfo cannot acknowledge, so the board refuses anything it sends that CFO with nothing typed until the CFO is started again.
The board shows a native CFO's terminal in its panel, from the CFO bar and from Orchestration.
`goblins` shows a CFO registered in a native terminal in its own terminal, and with no CFO running starts a new one in native terminal `cfo`, in the CFO home, running the remembered harness itself (`claude.exe` for Claude Code) so the terminal ends with it.
Its environment starts, as a native goblin's does, from the one Windows gives a new process of the user, never the launcher's, so a CFO started from inside another Claude Code session is not that session's child (which would save no transcript): the session markers and harness billing keys are dropped, the CFO is placed in the supervisor's home and projects root, and it keeps the launcher's Herdr session and configuration without the pane variables `serve` drops.
Each registration records the CFO's conversation in `state/cfo-conversation.json`, the latest one included when the same process registers again after a compact, clear or resume, which leaves `primary.json` as it was.
Plain `cfo register` names no conversation, so a Codex CFO that registers with it records the thread Codex gives every command it runs (`CODEX_THREAD_ID`); no other harness takes that variable for its own.
A home whose CFO last ran in a native terminal brings a closed CFO back there, on that conversation when its harness is the one chosen: `claude --resume <session>` or `codex resume <session>`; a terminal that ends within three seconds, as a harness that cannot resume the conversation does, or during startup settling, is started again on a new one, and the output names the conversation it left; so is a Claude Code conversation past 20 MB and any pi conversation.
A fresh terminal that also ends during startup is reported as failed before the quick start offers to attach.
`goblins resume` restarts a CFO that runs in native terminal `cfo`, holding `state/.cfo-launch.lock` so two restarts never start two CFOs: when the conversation in `state/cfo-conversation.json` was recorded by the process that terminal runs and can be resumed, it closes the terminal, which ends the harness and everything it started, and starts the harness again there with the arguments a closed CFO comes back with.
Otherwise it stops nothing and says why, and with no CFO running there it goes on as `goblins` does.
Run inside terminal `cfo` itself, where `CFO_HOST_ID` is `cfo`, it closes nothing, since closing that terminal would end the restart with it, and says to run it in another terminal or from the board.
The restarted CFO's startup dialogs are answered as a closed CFO's are, and a terminal that ends within three seconds is started again on a new conversation, with the conversation it could not resume named, and one that ends while those dialogs are answered is reported as ended, with nothing offered to attach to; `cfo resume` with no task named is the same command.
Whenever a CFO coming back, closed or restarted, starts on a new conversation because its harness ended at once, or a closed one's ended while its startup dialogs were answered, `state/cfo-conversation-left.json` records the conversation and its resume arguments, and the snapshot's `cfo_conversation_left` says so on the board until the CFO next comes back on its conversation, which removes the file.
The board's CFO bar offers the same restart for a CFO registered in its native terminal: its Restart button, beside the terminal's, asks first, since the restart interrupts what the CFO is doing, then sends `POST /api/cfo/restart`, guarded by the Host, Origin and token checks like every other change, and a CFO that could not be restarted says why on the bar.
The snapshot's `cfo_terminal_since` says when the host of the CFO's terminal started, and the board's view of the CFO names it, so a restart, which replaces that host under the same terminal id, opens the view again on the new host instead of leaving the old one's ended screen.
`goblins --harness claude|codex|pi` chooses the harness the CFO starts as, and the home remembers it in `state/cfo-harness` for every later goblins start; without it the quick start asks, starting on the remembered harness, or else on Claude Code, the one it recommends.
The board's first-run page reads the same remembered harness, shows it as chosen, and remembers the one it starts, so the terminal and the board hold one answer.
In a native terminal Claude Code runs as `claude.exe` and codex and pi as their npm script shims through `cmd /c`, as a native goblin's do; goblins starts no CFO in Herdr.
A harness that is not installed or not signed in goes to the quick start's install or sign-in step, and nothing is remembered or started until it is ready.
Its command line ends with one first prompt, which has it run `cfo register` in that terminal and then do what AGENTS.md says a CFO does at the start of a session; Claude Code needs none, since its SessionStart hook registers it and prints the digest.
What a CFO in each harness gets is one table, `supervisor.CFOCapabilities`: how it is woken, how it registers, whether a closed one comes back on its conversation, and what it goes without.
The quick start's notes, the first-run page and `cfo doctor`, whose `cfo harness:` line names the remembered harness with a `goes without:` line for each thing it lacks, all read that table, so none says more than another.
A real Codex CFO and a real pi CFO started this way were proved in scratch homes on 2026-10-02: each registered itself within 15 seconds of its terminal opening, ran `cfo session-start` and the other start-of-session checks, and acknowledged a queued wake within 12 seconds of the supervisor typing its line; the Codex CFO's terminal was then closed, and started again with `codex resume` and the same first prompt it registered again on the conversation it had (`TestACFOStartedAsGoblinsStartsItRegistersIsWokenAndComesBack`, run only when asked).
A harness starts on the model its own configuration names: a Codex whose configured model the signed-in account cannot use shows Codex's error in the terminal, runs no first turn and so never registers, until the model is changed and it is told to run `cfo register`.
A CFO already running keeps its harness, and goblins says the choice applies to the next start.
A Claude Code CFO is woken by its own Stop hook, which waits on the wake queue.
A Codex or pi CFO in a native terminal is woken by the supervisor, once it has registered by running `cfo register` in that terminal, as its first prompt tells it to: while wakes it has not been told about are queued, the supervisor types one line into its terminal, `cfo watcher wake: N queued wakes (...); run cfo drain, ...`, only when two readings of its screen a second apart show it idle at an empty composer, so never mid-turn and never over text left unsent, at most one line every 30 seconds, and each queued record is covered by one line only.
The line is proved taken as `cfo send` proves a send, and one that nothing proves taken is reported and not typed again.
A Codex or pi CFO someone starts in Herdr by hand has no wake path: it sees reports only when it checks the board or runs `cfo drain`.
`cfo attach` shows a native terminal in any console: the registered CFO's, or the one named; `--state <dir>` names the fleet's state folder for a console that does not inherit the supervisor's environment.

The panel's Open in terminal button, shown while it shows a terminal, opens that terminal in a new Windows Terminal window beside the board, through `POST /api/terminal/open`, which takes only what the view shows and runs the supervisor's own programs.
For a goblin or CFO in Herdr the pane is proved as its view proves it, Herdr brings its workspace and tab to the front, and the window runs `herdr --session <session>`, so it opens on that pane; for a native terminal the window runs `cfo attach --state <dir> <terminal>`, since a Windows Terminal window does not inherit the supervisor's environment.
A goblin whose no-mistakes gate owns its task is refused with the gate's reason, with nothing brought to the front and no window, as typing into it is; the CFO's own terminal has no gate.
A board without Windows Terminal, or a terminal it cannot prove, says why under the button.
With no CFO registered, `goblins` and `cfo attach` show native terminal `cfo` while its host answers, since the CFO started there may not have registered yet; a CFO registered in Herdr always comes first.
The board follows the same rule for the CFO's panel, so a CFO the first-run page started can be answered there, for example Claude Code asking whether to trust its folder, before it registers.
A starting CFO has not registered yet: Claude Code registers through its SessionStart hook after its onboarding and sign-in, and a Codex or pi CFO when its first prompt runs `cfo register`.
So while native terminal `cfo` is up with no CFO registered the snapshot says `cfo_starting` and carries no registration problem, the board opens that terminal by itself once per page (a panel the Overlord closed is not reopened), and the CFO bar says "Starting: answer anything it asks in its terminal."
The board's root shows the first-run page whenever no CFO runs in a home that has had none (the snapshot's `cfo_runs`: a CFO registered and running, or native terminal `cfo` up for one that is starting) and the normal board once one does, with no path of its own, so an installer or shortcut that opens the board's root lands on it.
A home whose CFO registered and has since ended, with no terminal up for a new one, has a closed CFO (the snapshot's `cfo_closed`), however it ended: he closed it, it crashed, or the machine restarted.
Its board shows no first-run page and no registration problem: the CFO's bar says `The CFO is closed. Goblins keep running.` with one action, Reopen the CFO, and the CFO's terminal slot says it is closed.
Reopen sends `POST /api/cfo/reopen`, guarded by the Host, Origin and token checks like every other change, and the supervisor brings the CFO back as `goblins` does: as the remembered agent, in native terminal `cfo`, on the conversation it last registered with where its harness resumes one, and on a new one when there is none or the resumed terminal ends within three seconds.
It is refused while a CFO runs or is starting, a CFO that cannot come back says why on the bar, and the terminal of the reopened CFO opens by itself, as after Start.
Only a sound registration whose process ended is a closed CFO: one that cannot be read keeps its problem, since it is wrong and stays wrong, and a home no CFO ever registered in shows the first-run page.
The page shows as done what the terminal quick start already knows, and asks for neither again: the Code Goblins home the CFO starts in, and the agent remembered in `state/cfo-harness` when there is one.
It offers Claude Code, Codex and pi as one row of icon tabs, moved with Left and Right, opening on the remembered agent or else on the recommended one, Claude Code, whose tab is marked Recommended; under the row is whether the marked agent is on PATH and has a saved sign-in (`~/.claude/.credentials.json`, `~/.codex/auth.json`, `~/.pi/agent/auth.json`), and the few words the table says on what a CFO in it gets.
It asks for no project: Start starts the agent as the CFO in native terminal `cfo` in the home, as `goblins` does, and remembers the agent as goblins would.
The projects folder, where goblins find a project by its name, is optional: the field opens on the recorded `CFO_PROJECTS_ROOT`, Look lists the git checkouts directly in a folder, and Start records a folder he looked at as the machine's projects root, as `cfo install --projects-root` does.
Start starts any of them this machine has, since the fleet wakes all three: an agent that is not installed says so and holds Start back until another is picked, and so does a Claude Code the terminal cannot start itself (anything but `claude.exe`).
Start is refused, with the reason, while a CFO runs or is starting, for an agent it cannot start, and for a folder he looked at that is not a full path, cannot be read or holds no checkout; with no folder looked at it starts all the same. An example board (`cfo serve --example`) records the folder for itself alone, never as the machine's setting.
After Start the board shows at once with the CFO's terminal open. A quiet link, Open the board without a CFO, shows the board while none runs, so goblins at work stay in view, and the CFO bar then offers Start the CFO in place of its terminal icon, which leads back to the first-run page.
Keys pass through raw, the terminal follows the console's size, taken back with the next key after another viewer resized it, and Ctrl-] leaves it running, whether the console sends that key as a byte or as a Windows key event.
A host refuses to start for a terminal that already runs, so a second start never takes over the first one's record.
`cfo peek` of a native terminal reads its screen from its console, exactly as the terminal's program would read it, rather than rendering the terminal's output: the rows written, without trailing blanks.
`cfo peek gb-<id>`, the form fleet-view suggests, reads a native task's terminal as `cfo peek <id>` does.
`cfo fleet-view` reads a native task's current state from its own terminal, as the monitor does, never from Herdr: a turn on its screen is working, and an idle terminal whose host recorded itself under the task's id is the task's own, so the row shows the task's latest report; with no running host the row is unknown.
A Herdr task's idle pane still shows unknown, since no Herdr answer proves the pane is the task's.
For each read the host starts a process of its own that attaches to the terminal's console, reads its window and ends, so a Ctrl-C typed to the terminal, or its console closing, during a read can end only that read, never the host.
A read that fails is an error naming the terminal, never an empty screen.
`cfo spawn`, and the board's Start with it, starts every goblin in a native terminal of its own, named by its task id. A pi goblin starts with `--approve` where its pi advertises it, so it never asks to trust the folder's project files and saves no trust.
The harness starts as its own program: claude.exe itself, and codex and pi through `cmd /c`, since their npm shims are scripts, and an argument cmd would read as more than text is refused.
The terminal's environment starts from the one Windows gives a new process of the user, built from the user's and the machine's configured variables, never from the spawning process's own, so nothing the spawning session set reaches the goblin.
The harness billing keys and every known session marker, such as `CLAUDECODE`, `CLAUDE_CODE_CHILD_SESSION` and the Herdr pane's variables, are dropped from it all the same, then the project's credentials and the launch's variables, `CFO_ROLE=goblin` among them, are added and win: a native task has no credentials script.
A Claude Code setting such as `CLAUDE_CODE_GIT_BASH_PATH` therefore reaches a native goblin only when it is configured for the user or the machine, not when only the spawning session sets it.
The spawn reads the terminal's screen throughout and types only where it recognizes what it reads.
A startup dialog it knows is answered only once it has shown for a moment, by moving the focus down and checking each move on the screen before confirming: Claude's trust dialog, which focuses "No, exit" first, Codex's update prompt (Skip), trust prompt (Yes) and hook review (Continue without trusting, never Trust all), and the project trust prompt of a pi without `--approve` (Trust (this session only), never a trust pi saves).
Trusting a hook is the Overlord's decision, so a Codex goblin runs without the hooks its review listed, and the spawn reports them to the CFO as `cfo notify` does, with a working status and a wake naming the command hooks of `hooks.json` in `CODEX_HOME` and in the project's `.codex`; a hooks file it cannot read is named in that report, never a reason to stop, and `config.toml` `[hooks]` tables are not named.
A prompt a spawn may not answer, or a screen it does not recognize within the startup budget, stops the spawn with the terminal named and its screen quoted.
The instruction is typed into the composer once the composer has stayed ready for two seconds with no dialog drawn over it, submitted once the composer shows it, and the spawn succeeds only once the harness shows it working.
An idle Codex can hold typed text undrawn and take it in slowly, so while the text does not show the terminal is widened by one column and set back to make it redraw, and the wait allows time for each character and keeps extending while its screen keeps changing, up to ten minutes, since a resumed Codex has taken text in at about a character a second; an instruction over 400 characters is written whole to the task's `instruction.md` and a one-line pointer to it is typed instead, on spawn, switch and resume.
Codex can also take the Enter as part of a paste, so Enter is pressed again one, two and three seconds apart while the text still shows and no turn has started; an Enter on an empty composer submits nothing, so nothing is delivered twice.
Codex's screens are the ones captured live on Codex 0.154, whose empty composer shows its placeholder rather than the context left.
A spawn that fails closes the terminal it started, which ends the harness and everything it started, and retires the task; a terminal that already ran under the task's id refuses the spawn's host and is left running.
If the terminal's host still runs but does not answer the close, the spawn's error says so, and the worktree and task record stay, so the task can still be reached.
`cfo send` reaches a task by its id or `gb-<id>` through its own native terminal, and nothing else: text is typed into the composer and submitted once the composer shows it, as the spawn delivers its instruction, and `--key` writes the key straight to the terminal.
A task an older build recorded in Herdr and a name that is no task are refused by name with nothing typed, and `cfo peek` of either fails the same way, since it reads native terminals only.
Once delivered, a send leaves the board's message receipt.
The text is delivered once the harness's own native hooks report it took a prompt after the submit (Claude Code's and Codex's `UserPromptSubmit`, Pi's `agent_start`, marked `prompt` in their events), or, where no hook has reported, once its screen shows it working when it was not working before.
Text sent while the harness is already in a turn waits in its composer until that turn ends, so without a hook report within a few seconds the send says it waits behind the turn rather than calling it delivered; a board answer in that case reads submitted while the goblin was working.
A credential stored for the project after a goblin started reaches it while its terminal's host runs: its task's credential script is rewritten and the goblin is told, through its terminal, to re-source it.
`cfo switch` changes a goblin's harness, model or effort in place: the harness exits on its own command, its terminal is closed if it will not, which ends it and everything it started, and the new harness starts in a new terminal under the same id with the task's credentials in its environment.
A reboot or sign-out ends every native terminal; the monitor's wake for a native task whose terminal has ended says so, and `goblins resume` brings every such task back, as `cfo switch <id>` to what it already ran (`--harness` naming its own harness) does for one, starting it again under the same id on the task's own recorded conversation (`claude --resume <session>` or `codex resume <session>`, never another task's latest) and tells it to continue where it left off.
A resumed conversation that comes back in a turn, its status row showing it working, has started: nothing is typed into that turn.
`goblins resume` reads every task record: it leaves a task whose lifecycle record of its generation says paused or stopped, or whose operation still holds its lock, and a task whose terminal still runs; it reports a task that ran in Herdr, one whose pause, resume or stop was cut short, and one whose operation failed, for a hand; and it admits each launch as a spawn does (`supervisor.CheckLaunch` on the memory left after the launch before), so a goblin there is no room for waits with the reason and none after it starts either.
Every restarted goblin, resumed or handed off, is also told that a question it asked the CFO before the restart was cancelled with it, and to ask it again with `cfo notify --blocked` if it was waiting on an answer.
That resume in place skips the dirty-worktree refusal, since uncommitted edits are the goblin's own work in progress and nothing is stopped or handed off; a switch that changes the harness, model or effort is still refused on a dirty worktree unless `--force-dirty`.
A task an older build recorded in Herdr is refused by `cfo switch` by name, before anything is stopped or written.
A `/` or `$` command gets the completion popup's longer wait before Enter and is reported submitted once but unconfirmed rather than awaited, because `/exit` ends the harness and `/model` opens a picker; check it with `cfo peek` rather than sending it again.
The monitor supervises a native goblin as it does a Herdr one: its host's record says whether its terminal runs, and the harness's own screen, read the way the spawn reads it, says whether a turn is in progress, a dialog waits on a person or the composer waits for input.
Its progress evidence is the processes its terminal's program started; its transcript is not located yet, since no Herdr session names it, so only those processes count as progress before a stale wake.
The board's runtime line for a native task names its terminal instead of Herdr.
`cfo cleanup` returns a native task whose terminal has ended, or whose harness waits at its ready composer with no working marker, in which case it closes the terminal, which ends the harness (decision 2339); any other screen, a screen or host record that cannot be read, and a harness whose screens cfo cannot read are refused with or without `--force-archive`.
It asks nothing of Herdr for a native task, so a machine without Herdr retires its native goblins; only a Herdr task needs Herdr.
The orphan sweep (`cfo reap` and the watcher's) refuses when a running Herdr server for its session cannot be read, since every goblin in it would read as an orphan, and sweeps on the native hosts and the process table alone when no Herdr server runs for its session, so no pane can exist; a test fixture's server for another session or a Herdr CLI call does not stop it.
A missing or stale registration shows on the board as one banner, and in the CFO terminal as its own state, naming what went stale and the fix, `cfo register` in the CFO session.
Each snapshot takes the banner from the same read of `state/primary.json` as `cfo_runs`, `cfo_starting` and `cfo_terminal`, so a CFO that was closed and opened again never shows running beside the problem of the registration it replaced; what the once-a-minute recovery cycle finds is shown only for the registration it examined.
On Windows normal message delivery holds that registration against replacement and validates the live process and start time and that the registered terminal's host still names that process as its program before it types anything.
Missing or changed identity is refused with nothing typed.
The CFO's own prompt hook establishes accepted delivery; the current native contract cannot prove a model response or provide an atomic process-identity compare-and-send operation.
Terminal input pins the exact terminal ID and task generation, checks process ownership and pipeline custody on the schedule above, and consumes ordered input identities once.
Writing native stdin does not acknowledge application acceptance.
Herdr cannot atomically compare the foreground process while writing: if an agent exits after the check, bytes may reach the same PowerShell terminal.
Known exited/replaced sessions are refused, but the board does not claim to eliminate that native check-then-write race.

Workspace details show the working folder separately from the Connections dropdown's asynchronous health checks; both are sections closed until opened, with the same header as Changes.
The task's Connections start with Harness, Model and Effort selectors, with the real harness mark and efforts beside the model; the CFO and child sessions retain their reported read-only engine rows.
`GET /api/engines` reads installed, signed-in harnesses and their local model catalogs: Codex's `models_cache.json`, Claude Code's `additionalModelOptionsCache`, pi's `models-store.json` and configured model in `settings.json`, plus fleet routing and the adapter's default model.
The model catalog is not a model-name list embedded in the board; each model's reasoning levels are limited to those the installed adapter accepts, and an unavailable current value remains visible with its reason.
`POST /api/tasks/engine` binds live changes to the task's spawn generation and queued changes to the backlog revision.
A queued choice changes the settings Start reads under the backlog lock; a paused choice is saved in `state/engine/<id>.json` and consumed only after Resume succeeds.
A running task's confirmation defaults to Switch when its turn ends: the pending choice survives a supervisor restart, is shown on its card, and applies after two idle empty-composer readings at least one second apart and no running, fixing or fix-review gate step.
A task paused while that choice is still pending uses it on its next Resume, which removes it only after the launch succeeds.
The catalog is read again as the choice applies: a selection that is no longer available is removed, with its reason on the task's card.
An idle or gate reading that fails shows on that card until a later reading succeeds or the choice is cancelled; neither kind of failure becomes the board-wide error.
Switch now explicitly interrupts the turn and a running gate step, using the existing `cfo switch` path with `--force-dirty` and `--generation`, which refuses the switch when the task's session has changed; its confirmation says uncommitted work stays.
The old native terminal closes, ending the processes it started, and there is no additional leftover-process refusal.
The target launch is built before the old harness stops, so an invalid launch leaves the session untouched; a failure after the stop shows the CLI's recovery reason and preserves the worktree.
The panel keeps its live values while the switch runs, then shows the resulting session and any failure reason.
The CFO receives one digest line in the notify wake queue, never a prompt, for each saved queued or Resume choice, each switch that completed or failed, and each pending choice removed because its session changed or its selection became unavailable.
Completed outcomes record the last harness, model and effort, while older outcomes without those fields say Engine not recorded.
Connections groups MCP servers, repository services and credentials present in the goblin's launch environment, with 16px or larger text and check times.
Each connection puts its name left and its status right on one line when the Connections region is at least 520px wide, then stacks them below that width, independently of the window's width.
Every name carries the board's full-text tip, so a long name the wide row truncates with an ellipsis still reads in full, and narrower regions wrap it in full instead; the rows have 24px of padding above and below their content while checks load and after they finish.
Claude checks use its MCP health report and the goblin's strict/config-file arguments; Codex inventory preserves the goblin's disabled-server overrides, and enabled servers earn Connected only from a fresh app-server runtime report, never from stored auth or cached tools.
Repository services reuse the auth manifest's probes and status words; a resolved token with no probe is Unverified, and a token present in the goblin's environment is Provided rather than Connected.
Checks are cached for one minute, limited to two concurrent workers and 45 seconds per check, and return Checking immediately instead of blocking the board.
Opening the dropdown starts a check only when the cached result is older than a minute; the refresh icon, a finished repair card and the first return to the board after each sign-in click always start one.
The dropdown polls only while a check runs, so an open dropdown never rechecks on its own.
A refresh or finished repair that arrives while a check runs queues one more check after it, and repair icons act on the shown result without starting a check.
Native environment reads verify the host, process ancestry, working folder and spawn generation; missing runtime evidence remains Unverified.
Herdr environment reads first verify the pane's registered harness, working folder, foreground process and shell ancestry, then the spawn generation in the process itself.
Project MCP servers omitted from the goblin's configuration appear as Withheld, with a token action when the project names a token variable.
CLI sign-in is offered only when the service's declared credentials resolve, and launch-disabled MCP servers offer no sign-in.
Sign-in icons open the manifest's HTTPS login page or a server-generated repair card using the existing Command Center run machinery; key icons create a store-from-clipboard card without reading the clipboard in the browser.
Fix requests accept connection and action identities only, require the board token and origin, and reject replaced tasks; repair cards verify the task again before running.
Finishing a repair refreshes the cached status, and returning from a browser sign-in rechecks it.
A stored token does not change an already running native process's environment, so its row remains Missing until the goblin receives it.
Matching-generation native model evidence takes precedence; otherwise the model is explicitly labeled configured, including a configured default.
Environment values, full process environments, dotenv, auth scripts, MCP commands and headers are never exposed.

### The tab and the installed app

The board's tab shows the goblin mark, from `/favicon.svg` with 16 and 32 px PNGs beside it, and its title counts what waits on the Overlord, such as (2) Code Goblins, dropping the count once nothing does.
Its theme color is the #03050a base, so a browser that tints its bar, such as Chrome on Android, blends into the board.
The supervisor serves a web app manifest at `/manifest.webmanifest` as `application/manifest+json`: Code Goblins, standalone, in the base color, with 192 and 512 px icons and a maskable 512 px icon, so Chrome and Edge offer Install Code Goblins and open the board in a window of its own with no tabs or address bar.
The PNG icons under `/assets/icons/` are rendered from `/favicon.svg`; render them again from it whenever the mark changes.

### The desktop window

A desktop window for the board, `goblins-window.exe`, is the program in `cmd/goblins-window`: a Wails v3 window on Microsoft's WebView2 around the board root the supervisor serves, never linked into `cfo.exe`, which every hook runs.
`go build -trimpath -tags production -ldflags "-H windowsgui" -o goblins-window.exe ./cmd/goblins-window` builds it as a release and `-Dev` do, and it is started with `--board <the board's address> --state <the home's state folder>`.
Without `-tags production` the window has WebView2's developer tools and the browser's menu on a right click, which is how to look into the board's page while working on the window.
`goblins --window` starts it that way where it sits beside `goblins.exe`, after finding or starting the supervisor as `goblins --board` does, with its output appended to `state\window.log`; a window already running takes the start as its second instance and comes to the front.
`goblins --window --background` starts it in its tray, and where no window sits beside `goblins.exe` either exits 1 and says so.
Started with neither `--board` nor `--state`, as the Start menu's Code Goblins and **Start at login** start it, the program is the app's launcher: it runs the `goblins.exe` beside it with `--window`, and `--background` when it was given that, in a console that is never shown, and exits once that has started the window on the board.
So a supervisor is started in one way only, by `goblins`, whoever opens the app.
A click on a notification while no window runs opens the app the same way: Windows then starts the program alone, with `-Embedding`.
When `goblins` fails, the launcher shows the last sixteen lines it wrote to stderr in a message box titled Code Goblins and exits 1; with no `goblins.exe` beside it, the box says that Code Goblins is not installed there.
Sixteen lines hold all that `goblins` says about a supervisor that did not start: what to do, where `serve.log` is, and the end of that log.
`.\install.cmd -Dev` builds it into the clone, the one-line install puts it in the home from a release whose `SHA256SUMS` lists it, as releases from v0.4.0 on do, and `cfo update` carries one that sits beside the candidate into the home once the candidate serves.
It needs only what the supervisor already provides: the board's address in `state\board.json`, the board's page, `/api/snapshot`, which it reads every 3 seconds for what newly waits on the Overlord, and `/api/announce`, where it claims each of those before it notifies it.
It holds no fleet state, and closing it leaves the supervisor, the CFO and every goblin running: it runs as a single instance, closing hides it to the tray, and only **Quit the window** in the tray menu ends it.
It follows the supervisor to a new address, loads the board again when a supervisor that was down answers, and opens the board's new-tab links in the default browser.
It raises the board's alerts as Windows notifications only while it is minimized or hidden to the tray; on the screen the board's own alert is the signal, and nothing is claimed.
What its own look finds it claims under the key the board's page uses, `alert:` and the item's key, after the 1.5 seconds a hidden tab waits, and notifies only what the supervisor hands it, so nothing in AFK mode; a supervisor that cannot be asked lets it notify, as it lets the page.
Each notification carries the goblin from `goblins-window.png`, which every start writes beside the program and names as `IconUri` under `HKCU\Software\Classes\AppUserModelId\Code Goblins`.
A picture that went missing while the window ran is put back before the next notification, and a notification goes out without it when it cannot be kept.
A window started with `--profile`, as its tests start it, registers for no notification, raises none and claims nothing from its board.
**Start at login** in the tray menu writes the `CodeGoblins` value under `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, and clearing it removes the value.
For a window that `goblins` started, the value runs the window alone with `--background`, which runs that `goblins` as above, so the supervisor starts before the window and no terminal shows at sign-in; `goblins uninstall` removes the value where it starts a program in that home, and an install rewrites it to this home's where it started a copy of the window installed on its own.
The install writes the window alone only where it put the window in the home itself, from a release that ships it or, with `-Dev`, from the build it just made; where the home only kept a window it already held, the install writes that home's `goblins.exe --window --background`, which opens any window.
An install that retained the home's window keeps the earlier standalone window, its picture, folder and Start-menu entry unchanged.
A value an earlier window wrote, which ran the `goblins.exe` beside it with `--window --background` in a terminal at every sign-in, is rewritten to this one the next time `goblins` starts the window, so the tray shows **Start at login** as on and clearing it once removes the value.
The board's dictation works in it as in a browser tab, because the page records with the browser engine's own recorder and the supervisor recognises the sound; WebView2's speech recognition, which has no service behind it, is not used.

### Interface rules

These rules hold for every board surface, and new work follows them.
Recurring tool actions (open in VS Code, open folder, open pull request, refresh, zoom, fit, arrange, close, reconnect) are icon buttons, each naming itself with an accessible label and a tooltip on hover and keyboard focus.
Decisions and one-off commands keep a short word, for example Send decision, Retry or Show the next 300 lines.
Every connector, MCP server, credential, harness and model provider shows a mark beside its name: the brand's mark from Simple Icons where one exists, a plain glyph where the owner withholds its mark, the Model Context Protocol mark for an unknown MCP server and a key for an unknown credential.
Delivery reads as a mark: one check once the supervisor accepted it or while it is sent and waits for its reader's turn to end, two checks once delivered; only a delivery that failed or never arrived is spelled out, with what to do.
An answer to a question or on a review item, to a goblin or to a CFO, and a `cfo answer` to a goblin, that arrives while it is working, such as inside a long tool call, waits in its input until that turn ends, and nothing can show it taken before then (a harness's prompt hook fires only when it takes it), so it is sent until that hook reports; a `cfo send` steer to a working agent still reads unconfirmed, while a run result or a review request sent to a working CFO reads sent until its hook reports.
A review answer's own action keeps one check, because it succeeds whether the answer reached the goblin or went to the CFO; only its review item says which.
Status words say what is happening in plain words, such as Working, In review gate, Waiting on you, Waiting on the CFO or Merged, verifying, never the evidence the supervisor holds.
Text is never smaller than 15 px.
Every surface wears SIQstack's glass from SIQshift's brand stylesheet: a 135-degree green-to-blue tint over the #03050a base, a green hairline border and a deep shadow with a green top highlight; a hovered card brightens its border and glows faintly, and what floats, such as a dialog or a menu, is the same glass laid on the solid #04060a surface so nothing behind it shows through; a tip has its own solid surface, described with the cards' tips.
The tokens live once, in `frontend/src/styles.css` `:root`, under SIQshift's and SIQstack's own names.
A selected card has a solid green edge and glow, and keyboard focus is SIQstack's 3 px blue ring, so a focused control never reads as selected.
The active tab of a pill switch carries SIQstack's green, blue and purple ring; primary buttons are SIQshift's solid green pill.
The goblins' dialogue boxes keep their lantern-and-leather look and add a green glass hairline that follows their stepped frame, with a faint green glow around it.
The board has one dark theme, and status colors keep their meaning in it: working blue, waiting and next amber, failed red, merged purple, done mint.

## Deliberate CFO questions

Worker alerts and natural-language questions stay with the CFO, including a goblin's blocked notify that offers choices.
The registered CFO must deliberately publish a decision from its own process ancestry:

```powershell
cfo question --id layout-choice-001 --text "Which layout should I use?" --option "Compact" --option "Spacious" --option "Keep current" --recommend "Spacious"
```

Omit `--option` when the question needs a written answer.
`--recommend` must exactly match a supplied option, which the modal shows first with its real recommendation; omit the flag when no option is recommended.
The Supreme Overlord Command Center labels supplied choices A/B/C and always offers Other for a written answer.
A question, the CFO's or a goblin's, is shown as body text at a readable line length rather than as a heading: a blank line starts a paragraph, a line that starts with `- ` is a bullet, text between `**two asterisks**` is bold, and everything else, markup included, is shown exactly as written; no HTML is ever interpreted.
So a question leads with one short sentence that is the actual question, puts its details on `- ` lines and bolds only the verdict or the blocking item.
The inbox and history list each question on at most two lines, with the marks dropped.
No choice is preselected and written text is sent only when Other is selected.
Use a new stable ID for a new question, and keep the same ID/content for an uncertain publication retry.
The publisher walks up to 32 process ancestors and verifies the registered CFO PID, creation time and live native identity; a worker cannot escalate on the CFO's behalf.
For a CFO in a native terminal, a publisher whose chain of parents stops short of the CFO is proven instead by the terminal's proof value, as for a goblin's question.
The board's stream, `GET /api/events`, sends two kinds of event.
A `snapshot` is the whole board, read from the fleet's files, so the supervisor builds each away from the stream.
One build serves everyone who asks while it runs: every open board, `/api/snapshot` and the ticket keeper share it, so one change costs one build however many readers there are, and one refresh every 15 seconds serves them all.
A build opens a fleet file only when its size or its modification time changed since the build before, and otherwise keeps what the file gave then; it asks the disk about each path once, and learns of the task records, lifecycle records and monitor records from one listing of each folder.
On 2026-10-02 a build opened 447 files at the live fleet's size and took 3 to 13 seconds on the fleet's machine, where opening a file cost 20 to 80 ms in bursts; an unchanged fleet now opens 4, and a goblin's report one more.
A goblin's `cfo notify` tells the supervisor over its pipe as it returns, without waiting for an answer: the supervisor runs a cycle, which takes in a blocked question or a wait on the Overlord, and tells every board, so a report reaches an open board within a second rather than at the next refresh, and leaves the board's last error as it was.
With no supervisor running, or one too busy to take it, the report shows at the next refresh.
An `items` event is the Command Center's part alone (`questions`, `reviews`, `runs`, `credentials` and `actions`, with the supervisor's `instance` and `revision`), read from the store's memory and sent the moment it differs from what that board last received, while a snapshot may still be building; the board lays it over its last snapshot.
A snapshot carries the items as they are when it is sent, not as they were when its build began, so it never undoes an items event sent before it.
The Command Center shows one item at a time as a stack, a question, a review item, a run item or a credential request, the CFO's own items first, then goblins in the In progress order, each goblin's by longest wait, then goblins not placed yet by longest wait (the snapshot's `attention` names the placed ones), and a horizontal swipe on touch screens moves between them; the card stands alone, with no edge of the next one behind it, and its text is sized to read at a glance (19 px body, 22 px titles).
A card's own action row holds everything: Back, its place such as 2 of 4, and Next on the left while more than one item waits, and its answer on the right; closing keeps every item for later.
Each card sends only its own answer.
The moment the Overlord sends from a card, an answer, a review answer or a Clear, its check draws with Sent (or Opened, Downloaded or Cleared) and three quarters of a second later the next open item follows, passing over any sent in this sitting, while the action is delivered in the background; CFO received or Delivered to <goblin> joins the check if delivery lands while it shows, and with nothing left it shows You're all done and the Command Center closes.
A request the board refuses keeps its card on screen with what went wrong, and can be sent again with Retry under the same request identity; a run card stays to show the command's result.
A request refused, or failed before the board took it, after its card moved on or the Command Center closed opens nothing: its item keeps waiting under the badge, its row under Waiting on you reads Not sent with what went wrong, and opening it from there shows the same and Retry.
An item the Overlord acted on never comes back by itself: a delivery that fails or never arrives reads as the item's line in History, with its warning mark and the supervisor's `advice` on what to do while the action is still recorded, and it neither reopens the Command Center nor returns the item to Waiting on you.
An item leaves the whole board in the frame he acts on it, not when the next snapshot arrives: its alert and Windows notification, its count on the badge and in the tab's title, its row under Waiting on you, the CFO bar's line and its goblin's Answer button all go with the click, on that board and on every other board open in the same browser, which hear of it on a `BroadcastChannel`.
On 2026-10-02 the live board took 3 to 13 seconds to build a snapshot, and all of those kept asking a question for that long after its card had closed with its check.
A send the board refuses puts the item back, with why, as before.
The supervisor closes the item as it takes the request: an answer, a review answer and a Clear or Dismiss all close it under the store's lock before the request is answered, and the queued action only delivers the answer or tells the CFO what was dismissed, so a dismiss no longer waits behind a delivery for its turn.
An item the board has once seen closed stays closed: a snapshot built before an answer and arriving after it, a reconnect or a second window brings nothing back, and the item shows as the board last saw it closed.
Closed here means the same publication, the item with that `created_at`: an ID the supervisor takes again after it dropped the earlier record is a new item, whose count and CFO bar line show.
An item keeps its publication when the CFO registers again and its identity moves.
The Command Center never opens itself while the Overlord types on the board, with the focus in a text field, a comment box or a terminal (decision 3596, after his 2026-09-28 words that it must not "prevent input when the editor has text annotation input"): a new question then waits under the badge with its alert, and his typing keeps its focus; a question its goblin asked about an open review page never opens it, since it is that page's card.
A click on the dimmed board outside the card closes the Command Center, and a click anywhere outside the open inbox closes the inbox.
Every time the Command Center opens, from an alert, the counter or a card, it starts at the top of its item, however far down it was left, and moving to another item shows that item from its top; within one item the scroll stays where the Overlord puts it.
A card whose item closes while it is on screen and that sent nothing itself, answered with `cfo answer`, on another board or on its page, or taken back by its goblin or the CFO, finishes as one he sent from does: its check draws with Answered, or Closed for an item nobody answered, over the line its History row carries, and three quarters of a second later the next open item follows.
A card whose answer did not arrive keeps its place and says what to do, and a run card stays to show the command's result.
Drafts survive closing, reconnecting and moving between cards, and the header button, whose badge counts what waits on the Overlord, opens an inbox of those items under Waiting on you, then a history of what he answered, cleared or ran, newest first by when each closed.
Waiting on you lists everything that needs him, and only that: every open question, review item and run item, and each live presentation its goblin asked him to watch with `cfo present --watch`, as its line with a Watch button (an eye) that opens it, counted in the badge.
A presentation without `--watch`, such as a goblin's own test walkthrough, stays off the Command Center; its task card still shows the browser it has open.
A goblin panel whose goblin is waiting on the Overlord offers Answer, which opens the stack at that goblin's question or review item.
Once submitted, every tab displays the durable answer rather than an unsent local draft.
Answers retain their question and CFO identity and enter the durable native CFO message queue, never a worker send or gate approval.
Claude Code, Codex and Pi primary-context guidance routes explicit user decisions through this command.
Publish from the same registered primary shell, continue independent work or finish the turn awaiting the reply, and do not also open a native prompt tool: a normal message cannot answer a correlated native Codex/Pi prompt.
Duplicate HTTP/SSE outcomes cannot cause a second delivery; interrupted delivery becomes uncertain.
The CFO's work on the board follows the home's CFO, not one CFO process, so closing the CFO and opening it again strands nothing.
Once another CFO process registers in the home, its open questions, its review items whose answer has not reached it, its run items not yet ended and the deliveries queued to any of them move to that CFO, and nothing of it is superseded.
While no CFO runs, an answer or review on its way to the CFO waits queued instead of failing, and how a run item ended is kept on the item and told to the CFO once one runs again.
The store bounds question history at 128 records, retires cleared and answered history first, and defers overflow when all questions remain pending.
The bounded publication inbox is admitted in timestamp order, not hash-filename order, and rollover keeps its cutoff below the incoming timestamp so deferred and same-time questions are not discarded.
Conflicting, corrupt or oversized inbox records leave bounded diagnostics and cannot stop unrelated native events.

## Goblin questions

A goblin's `cfo notify <id> --blocked "<question> options: <answer> (Recommended) | <answer>"` asks the CFO.
It never appears in the Overlord's waiting count, shows him a toast or opens the Command Center; after it is answered, History shows who answered it.
Once the CFO answers or acknowledges it, its notify still holds the task, blocked or failed with its words, until the goblin reports again, and the snapshot marks that task `report_handled`, so the board never tells him of it as the goblin's news; a block or failure the gate holds is never marked and is news.
The CFO publishes a decision that needs the Overlord with `cfo question`.
A question's first choice that ends with `(Recommended)` is shown first and marked, and the mark is stripped from every choice.
The choices follow the last `options:` marker, so a question that names the marker in its own words, such as in a detail line, keeps its choices.
The card shows the choices as a plain list of radio buttons, each the answer's own text with no A, B or C, because the Overlord picks an answer, not a letter.
`cfo notify` and `cfo question` refuse, recording nothing, a choice that is only a letter or number, such as `a`, `B)`, `(c)` or `2`, and say to write the answer itself as the choice.
When every choice starts with the goblin's own letter in order, such as `A) `, `b. `, `(c) ` or `D: `, the card drops those letters from what it shows; the answer is still the goblin's choice word for word.
A goblin can attach one review image to each choice with `--image <path>`, repeated in the order of the choices, so the Overlord picks by picture: the card shows a thumbnail for each choice, captioned with its answer, and any thumbnail opens a full-size gallery with its position, side buttons, arrow keys, swipe, click-to-zoom and a Choose this answer button for that image's choice.
While the gallery is open it replaces the card, so a strip of the question's thumbnails under the image jumps straight to any other image.
When the asking goblin has a review page live, the card and its gallery offer it with Open review; [Review items](#review-items) says which page.
An image must be a PNG, JPEG, GIF or WebP of at most 10 MiB inside the task's worktree, task scratch or data directory, reached without a symlink or junction, and `cfo notify` refuses a wrong count or a bad image before anything is recorded.
The board never sees an image's path: it serves image n of a question at `/api/questions/<id>/images/<n>`, checks the file again on every request, and stops serving once the task restarts or ends.
A blocked notify without an `options:` marker, and every other worker alert, stays in the CFO wake queue only.
Only a process running under the task's own terminal can surface its notify, by the same proof `cfo register` uses: under the program of its native terminal, whose host must answer.
In a native terminal a process whose chain of parents stops short of the program, as Git Bash leaves `timeout 60 cfo notify ...` when it runs timeout, an MSYS program, by replacing its own Windows process, is proven instead by the proof value the terminal's host put in the terminal's environment as `CFO_HOST_PROOF`, beside `CFO_HOST_ID`; the CFO's own registration, its pipe proof and the sender of its sends take the same proof in a native terminal.
The host records only the value's SHA-256 and when the terminal's program started, so the value proves nothing once Windows gives the program's pid to a later process.
Every process the terminal starts inherits the value, so it proves nothing in a Herdr pane, to which a Herdr server started from the terminal would hand it, and `cfo serve` forgets the id and the value of the terminal it was started from before it starts anything.
A notify that fails that proof or offers more than eight choices still wakes the CFO, and `cfo notify` prints why the board could not show it.
Such a question never reaches the board, so once it has had 30 seconds to arrive `cfo answer` sends the CFO's choice straight to the goblin that asked, marks the notify answered and records nothing on the board; it refuses, sending nothing, when the goblin's current generation started after the notify, since that goblin never asked.
The question is bound to the task generation and terminal that asked.
The Overlord's answer goes to that goblin exactly once, never through the CFO and never to a respawned successor: typed into its native terminal the way `cfo send` types, submitted once its composer shows it and delivered once the harness works on it.
The notify then reads answered: `cfo drain` prints the board's answer and acks the record without `--ack-blocking`, and the monitor stops re-asking it.
The CFO still acks it in the ordinary way.
The CFO acks a notify once it answered the goblin itself, such as with `cfo send`, so the board then closes its copy as answered by the CFO (`answered_by: cfo`, no choice recorded), which reads "The CFO answered it" with the check of any answer, and an answer queued before that ack is refused with nothing sent.
`cfo answer --record-only` can still record which choice it was.
One window stays open: if the CFO answers with `cfo send` and the Overlord answers on the board before the CFO acks, the goblin receives both, each labelled with its sender.
The CFO answers a goblin's question in a structured way with `cfo answer <question-id|wake-seq> --option <choice> [--note "<text>"]`, from the registered primary CFO only.
The choice is named in full or by its first word (the `a`, `b` labels goblins give their options), and a label that names two choices, a choice the question does not offer, a notify without choices, one already answered or handled, and a restarted goblin are refused before anything is sent.
The goblin receives `CFO: decision <seq>: <choice>. <note>` the way `cfo send` types, the notify reads answered so `cfo drain` acks it without `--ack-blocking`, and the board records the choice, that the CFO gave it and when, even when the CFO drains the notify first.
Every answer, on the board or through `cfo answer`, is recorded on its question as `answered_option` (the choice; empty for a written answer), `answered_by` (`cfo` or `overlord`) and `answered_at`, so a closed question shows which option was chosen.
History marks who answered each question: a crown for the Overlord, the CFO's face for the CFO, and the CFO's face with a moon for an answer the CFO gave while AFK mode was on (`answered_away`, which the supervisor sets from AFK mode's state when it receives the CFO's answer or closes an acked question, whatever a sender says).
Each row gives when it closed and, on a line of its own, the reason the CFO gave with `--note` or the CFO's answer his change replaced; an answer that failed or is unconfirmed keeps its warning mark.
His word outranks the CFO's: on a goblin's question the CFO answered, while the goblin that asked is still the one running and has written no report since (`reported_at` on its task, the time of the report `last_report` shows), History's pencil (tip: "Change the CFO's answer") opens the question with the CFO's choice marked, and Change to my answer queues an `answer_change`.
The supervisor checks the same when it queues the change and again under the question's answer lock, then tells the goblin "The Overlord changed the answer to your question on the board" with the question and his answer, saying it replaces the CFO's; the question then reads as his answer, with `replaced_answer` naming the CFO's choice, and a change that fails leaves the CFO's answer standing.
An answer the Overlord gave somewhere else, such as in chat, closes its card once the registered CFO records it with `cfo answer <question-id> --option <choice> --record-only --in <where>`: the question reads `answered_by: overlord` and `answered_in` naming where, an open card finishes with the Answered check and "You answered in chat · recorded by the CFO", and nothing is sent to anyone.
The CFO's own question takes only this form, since only the Overlord answers it, and `--in` is a few plain words.
A question that closed without an answer, because its goblin restarted or ended, stays listed with its reason until the Overlord clears it (`question_clear`), or until 128 questions are held and it is the oldest superseded one, which makes room for a new question; a pending question is never dropped, so an unanswered decision is never hidden.
The Overlord dismisses a pending question he answered elsewhere or no longer needs with the close icon beside Send decision (tip: "Dismiss: answered elsewhere or no longer needed"), the same `question_clear`: it closes as dismissed, the card finishes with Dismissed, and the CFO is told, since it asked the question or holds the goblin's notify that did, which still waits for it; a question whose answer is on its way cannot be dismissed.

## Working and waiting reports

A goblin that resumes, or waits on something, says so without asking anything:

```powershell
cfo notify <id> --working "wiring the store"
cfo notify <id> --waiting-on <task-id|overlord|ci|deploy|memory> "<why>"
```

Both write a status line only, so they wake nobody, and a newer one of them replaces an older blocked or failed reading on the board while the question itself stays in the CFO's queue.
The task reads `working` with the reason, or `waiting` with the reason and `waiting_on` naming the target, unless a newer question, the gate's own decision, or a merge says otherwise.
A wait on another task clears itself once that task reports done, and a wait on CI, a deploy or memory lasts until the goblin reports again; the CFO releases any wait with a `--working` line of its own.
Waiting on the Overlord is the one wait that wakes the CFO: it also opens a review item for him, named `waiting-<task>-<wake sequence>`, which he can answer or clear, and which is withdrawn once the goblin reports anything newer than a question.
A question the goblin asks while it waits (`--blocked`) replaces no wait: the goblin waits on the Overlord and on the question at once, the item stays, the task reads blocked while the question is open, and once it is answered the task reads the wait it still stands on.
So `--waiting-on overlord` is only for a wait on the Overlord personally: his sign-in, his click, his page.
A choice the CFO can make, such as whether to start something now or after a reset, is an actual question and uses `--blocked` with options.

Scrawl is the Overlord's name for the review page he annotates and answers on.
The `lavish-axi` command serves it, and the command, the `--lavish` flags and the `lavish` state fields keep their names.
A wait whose answer the Overlord gives on a Scrawl page names the page:

```powershell
lavish-axi .lavish/plan.html --no-open
cfo notify task-id --waiting-on overlord "pick a plan" --lavish .lavish/plan.html
```

The notify opens the page without a browser, and refuses, recording nothing, when the file is not an HTML page or `lavish-axi` cannot show it.
The page's link goes into the wait's line, so the CFO's wake carries it, and onto the wait's review item.
`cfo serve` then polls the page, one bounded `lavish-axi poll` at a time, for as long as the item is open, and is the only one that does: a poll hands the Overlord's feedback to whoever runs it, so nobody, goblin or CFO, polls a page themselves.
Whatever becomes of the page reaches the CFO as a `review` wake, retried until the queue takes it, and then the item closes: his answer, sent with Send & End (an empty Send & End approves as shown), saved whole under `state/reviews/feedback/`; the review ended; or a page that cannot be polled three times running.
His answer reaches a goblin by itself: the supervisor types it into the goblin's own terminal, as a board answer reaches it, and the CFO's wake says the goblin has it; only when the goblin cannot take it, such as one that restarted, does the wake ask the CFO to relay it, and the CFO's own page wakes the CFO to act on it.
What he sends on the page without ending the review (Send) is a revision, unless the page carries the goblin's pending question, which it answers: the goblin gets the revision in its own terminal with how to send its next version, the CFO is told, the item waits on the next version (`revising_since`) and leaves Waiting on you, and the next poll answers him in the page's conversation panel: Revision received, the goblin (or the CFO, for its own page) makes the next version, which replaces this page.
The review goes on and the page is still polled, so a further revision or his answer reaches the goblin the same way.
The goblin's next version is a `cfo notify --waiting-on overlord ... --lavish <the same file>` again: it replaces the page's item in place (same item, its new words, waiting on him again) instead of opening a second one, and the page itself reloads to the new version.
A wait whose revision its goblin is making stands while the goblin reports work on it, and retires once the goblin reports done or failed.
A closed or disconnected review window is not one of them: lavish-axi keeps the session and queues every answer he gives on the page, so the poll goes on, ten seconds after a disconnect, and when he reopens the page the same review resumes and the next poll takes everything he sent, however many answers.
How it closes says who closed it: his answer closes it as answered, by the Overlord (`answered_by: overlord`), on the page (`answered_in: page`), which also ends the goblin's wait because the goblin has it; his own end of the review on the page closes it as his clear (You ended the review on its page.); and it reads withdrawn only when it closed without his word, such as a page that cannot be polled or an agent that ended the review itself (lavish-axi reports `ended_by: agent`).
Only the item gets the page polled, so when it cannot be published the notify says nothing watches the page and fails, telling the goblin to ask in text with `--blocked`; the wait's line is already recorded and still reaches the CFO.

The monitor watches for the mistake this rule prevents.
A `lavish-axi poll` that runs in a goblin's worktree, or under a process that does, such as its harness, and that `cfo` did not start wakes the CFO once, as a `review` wake, for as long as that poll runs.
A live goblin's wake says to have it stop the poll and wait with `cfo notify --waiting-on overlord --lavish`.
A retired goblin's, known by the status log `cfo cleanup` keeps, says to read the page's open questions, put them to the Overlord and stop the poll.
A poll in a worktree whose goblin the home never had is somebody else's and is left alone.
The check reads the command lines of `node` and `lavish-axi` processes, and the folders of a poll and of the processes above it, once per monitor scan.

## Review items

A review item is something that needs the Overlord's attention without blocking anyone, such as a page of mockups, a report or before and after screenshots:

```powershell
cfo review --id mockups-review-1 --task task-id --title "Pick a task list layout" --image grid.png --image list.png --lavish http://127.0.0.1:4387/session/<id>
cfo review --id mockups-review-1 --task task-id --withdraw "Replaced by mockups-review-2"
cfo review --clear mockups-review-1 --reason "Decided: the grid layout ships"
cfo review --id dispatch-review-1 --title "Pick the dispatch order" --lavish .lavish/dispatch-options.html
cfo deliver --id setbacks-1204-oak --title "Setbacks and envelope, 1204 Oak St" --file "$env:USERPROFILE\Desktop\setbacks-1204-oak-st.pdf"
```

A goblin runs it from its own native terminal, proven the way its questions are; the registered primary CFO omits `--task`, and only a goblin's item takes images.
The ID is 8 to 128 letters, digits, dots, dashes or underscores; republishing the same ID with the same content changes nothing, and other content under that ID is refused.
Up to twelve images, each a PNG, JPEG, GIF or WebP of at most 10 MiB inside the task's worktree, task scratch or data directory, are checked like a question's and copied under `state/reviews`, so the item outlives the worktree and the goblin; `data/` is never used, because it is pushed.
A `--lavish` link follows the presentation URL rule below, and a refusal names the rule it broke.
`--lavish` also takes the page's HTML file, from a goblin or from the CFO: the command opens it without a browser, refusing a file that is not an HTML page or that `lavish-axi` cannot show, and the item carries the page's link.
`cfo serve` then polls that page exactly as it polls a page wait, and whatever becomes of it reaches the CFO as a `review` wake, keyed by the goblin or, on the CFO's own item, by the item's ID, and the item closes.
For another round, publish the page again under a new ID.
An item stays open until the Overlord clears it (`review_clear`), its reporter withdraws it with a reason, or the supervisor retires it; nothing expires it, and a `cfo serve` restart keeps it.
The supervisor retires a goblin's item nobody waits on any more, on every reconcile: a wait on the Overlord once its task reports anything newer than a question, any other item once its task reports done after publishing it, and every item of a task that is gone, such as one cleaned up; a goblin's page or images stay while it keeps working, asks or waits, because it still wants the Overlord's look, and the item reads Withdrawn: <task> finished: <report> or <task> is gone.
A delivered document is never retired, because its copy outlives the goblin: it stays until the Overlord opens, downloads or clears it.
When the CFO answers a goblin's question with `cfo answer`, the goblin's waits on the Overlord raised up to that question close at once, since it has what it was waiting for, and each reads The CFO answered <task>'s question.; a wait it raised after the question stays open.
The registered primary CFO can clear any open item with `cfo review --clear <id> --reason "<why>"`, such as one a retired goblin left; the item reads Cleared by the CFO: <why>, and `state/reviews.audit` records every clear the CFO makes, with its time, item, goblin and reason, one per line.
The Overlord can instead answer it (`review_answer`): his text goes once to the reporter, the goblin's own terminal while it is the same task generation or the CFO that reported it, and the item closes as answered; an answer for a goblin that restarted or ended goes to the current CFO instead, and the item reads `delivered: false`.
An answer the goblin received also reaches the CFO as a `review` wake that asks nothing, so the CFO sees every answer the Overlord gives.
The board sees each item in `snapshot.reviews` with an image count, never a path or a digest, and fetches image n at `/api/reviews/<id>/images/<n>`, checked again on every request.
A new review item waits in the Command Center inbox under the badge instead of opening the stack, and the browser tab's title counts everything waiting, so a board in a background tab shows it too.
An item addressed to the Overlord has one signal on a visible board: Open Command Center and its count.
Only the CFO's own questions (`task` empty) wait on him; a goblin's blocked or failed question belongs to the CFO and enters History after it is answered.
A goblin's explicit wait on the Overlord, review page, delivered file, run item and credential request still belong in his queue.
No item shows an in-page toast or opens the Command Center by itself, including while the CFO's terminal is open.
A goblin's finished or failed news still shows a dialogue box with Open for that goblin; routine progress shows none.
The first snapshot establishes the baseline for news and items.
When unanswered blocked or failed wake records, or `goblin_asks` records of a question a goblin asked in prose, have waited on the CFO for ten minutes, `cfo_quiet` gives one notice with their count and the oldest age in seconds.
Its `since` is saved once for that stretch and remains the same through answers, reloads, reconnects and supervisor restarts until no question has waited ten minutes.
The board can announce that notice on its first snapshot, with Open the CFO's terminal; it never offers the goblins' questions to the Overlord.
Before an announcement the board claims the event through `POST /api/announce` (`keys` for items and the quiet-CFO stretch, `news` for a goblin's news) and announces only what the answer's `claimed` names.
The supervisor hands each key to the first request and keeps it for thirty days, at most 2048 keys, so a second tab or a restart never announces that item or stretch again.
While [AFK mode](#afk-mode) is on it hands out nothing and records what was asked about, so nothing announces while the Overlord is away or repeats when he returns.
A hidden tab waits a second and a half before claiming, giving a visible tab the first chance; with no tab in view the hidden tab can send its Windows notification.
An announcement is claimed as `alert:<key>` cut to 160 characters, within the endpoint's 512 byte bound, and an unreachable supervisor falls back on what the browser remembers.
An item's identity prevents repeats, and identical words from the same goblin within five minutes are one event.
A goblin's next pull request alerts at once; the same news more than five minutes later can alert again.
The browser remembers the last 100 announcements it showed.
A toast steps up once as it arrives, or appears under reduced motion, leaves after eight seconds unless the pointer or keyboard rests on it, and can be dismissed; at most four show, newest at the bottom.
Windows notifications require `document.hidden`: a hidden tab or minimized window, not an unfocused window that remains visible.
The browser asks for notification permission once, with the first announcement, and a notification click opens its item or terminal.
An announcement and its notification leave when their item closes or the quiet-CFO stretch ends.
A click on an item already closed opens the Command Center's list, never another item's card.
Its card shows the title, its images as thumbnails that open the same full-size gallery as a question's, and its own page, when it has one, as a preview named Scrawl page that opens the page with Open review and never repeats the title.
An item whose page the supervisor watches (an HTML page given with `--lavish`) is answered on that page, so its card has no text box: it finishes when the Overlord sends or ends the review there, or he closes it with Clear.
A goblin's pending question asked in the same generation as one of its open page items is that page's item, so the Command Center shows one card for the two: the snapshot names the item in the question's `page` and the newest such question in the item's `question`, the stack and the inbox list the page's card alone, and an alert or link to the question opens it.
That card shows the question as its body, one Open review button and a status line instead of choices, since he answers on the page: Waiting for your answer, or, once the page's window disconnected (`window_closed_at`), when it closed and that nothing he sends there is lost, or, after a revision, Revision received: the next version replaces this page.
Clearing the page's card dismisses the question it carries in the same step (`You cleared its page's card.`), and the CFO, which holds the goblin's notify, is told as for any dismissed question.
The Overlord's board answer to a goblin's question also closes that goblin's waits on him up to the question (`waiting-<task>-<n>` with n at most the question's sequence, `answered_in: question`), as the CFO's `cfo answer` does, and a goblin's newer question supersedes its older pending one, so one card shows.
An answer in either place closes both: his answer on the page closes the question as answered by him on the page (`answered_in: page`, with what he wrote) and marks its notify answered, so `cfo drain` retires it, and the CFO's `cfo answer` or his own board answer to the question closes the page's item as answered through its question (`answered_in: question`), as soon as his answer is sent, so nothing he writes on the page after it reaches the goblin as a second answer.
A card open on screen when he answers on the page finishes as one he answered from it does: a check with Answered and You answered on its page, then the next open item, and History lists it with a double check; a Clear or Dismiss the board refused because the answer had already closed it shows no error, and no card shows a refusal once its item has closed.
Any other item takes a written answer with Send answer or closes with Clear.
`cfo deliver` hands the Overlord a document the same way: the registered primary CFO delivers any file it can read, a goblin only one from its worktree, task scratch or data directory, at most 64 MiB, and the file is copied beside the item so it outlives the original.
`--url` names where Open goes instead of the copy, such as a hosted page, under the same rules as a presentation link (https or plain http on this machine or the tailnet, no query, no credential in the path).
The card shows the file's type, name, size and sender with Open and Download, Open only when the browser can open the copy or there is a `--url`; the board fetches the copy at `/api/reviews/<id>/document`, in the browser only when it is a PDF or a PNG, JPEG, GIF or WebP image by its content, and as a download for anything else, HTML and SVG included, never sniffed.
Opening or downloading it clears the item as Opened or Downloaded, which History keeps, so a document leaves the queue once he has it.
A question's card offers Open review only for its asker's most recent live review page, from the same goblin session or the same CFO registration, so it never opens another task's page or one a replaced asker left behind; page links read Open review, never Lavish.
A closed item moves to the inbox history as You wrote: <answer>, Cleared, Opened or Downloaded for a document, the CFO's reason when the CFO cleared it, or Withdrawn: <reason>; an answer still on its way reads not yet delivered, and one whose `review_answer` action failed or became uncertain carries the same warning marks as a question.
Only `delivered` earns two checks: an answer the CFO took over because its goblin was replaced reads not yet delivered to the CFO until the CFO's hook reports taking it, then Sent to the CFO: <answer>, and an undelivered answer whose action has aged out of the snapshot reads delivery no longer recorded instead of being assumed delivered.
Closed items and their copies are pruned a week after they close; open items, answered items whose answer is still on its way, and a closed wait on the Overlord while its goblin's latest report is still that wait are never dropped, and a new item waits in the inbox while all 128 held items are one of these.
The API contract for the board is `data/board-ui/api-contract.md`.

## Run items

A run item is a command the CFO, or a goblin, needs the Overlord to run, such as a PowerShell or Git Bash script, a sign-in or a step that needs administrator rights; he runs it with one click from the Command Center instead of copying and pasting it:

```powershell
cfo run-request --id install-tool-1 --title "Install the tool the build needs" --shell powershell --command-file C:\temp\install.ps1
cfo run-request --id fix-acl-1 --title "Grant the service account access" --shell powershell --admin --command-file C:\temp\acl.ps1
cfo run-request --id gh-sign-in-1 --title "Sign in to GitHub" --shell powershell --interactive --command-file C:\temp\sign-in.ps1
cfo notify <task-id> --waiting-on overlord "Sign in to GitHub so I can push" --run C:\work\sign-in.ps1
```

`cfo run-request` hands the item to the supervisor over its named pipe, and the supervisor itself proves the sending process runs under the registered primary CFO, the proof `cfo question` uses: it walks up from that process to the CFO, each ancestor created before its child, since Windows reuses PIDs.
For a CFO in a native terminal, a process whose chain stops short of the CFO is proven instead by the terminal's proof value, which the supervisor reads from that process's own environment.
The sending process must also have started before it connected, so a process that later took its PID proves nothing.
The supervisor drops a client that sends nothing within 10 seconds and gives each request 20 seconds for its proof, and `cfo run-request` waits 30 seconds for the answer.
Every reply on this pipe stays readable after the supervisor has let go of the client, so a client that reads late still gets its whole answer; only a reply larger than the pipe's 64 KiB buffer waits for its reader, for at most 30 seconds or until shutdown, and a client that does not read holds up no other request.
The pipe is the supervisor's own: it creates the first instance of its name, waiting up to two seconds for a stopping supervisor to let go, grants the current Windows user alone, and rejects remote clients.
A supervisor that finds the name still taken serves nothing and lists that among the board's issues, and every command that uses the pipe sends only to the process holding this home's watch lock, so a squatter never receives a request.
The supervisor thus proves the sending process runs under the process `state/primary.json` names: a request from a process outside the CFO's tree or terminal is refused before anything is written, and an item planted in the state directory never reaches the board; a request needs the supervisor (`cfo serve`) running.
Processes of one Windows user are peers, though, and a same-user process that rewrites `state/primary.json`, starts a process with a spoofed parent, or copies the proof value out of a process in the CFO's terminal can still pass the check, so it is not a boundary between processes of the same user.
The Overlord reading the exact command before Run, and Windows UAC for an admin item, remain the final check.
The command file is read once: the supervisor stores its text as `state/runs/<digest>/command.ps1` or `command.sh`, which is what runs, so quoting cannot change it, and the item runs in the CFO home unless `--cwd` names a folder.
The ID follows the review item rule: republishing it with the same text changes nothing while the item waits, and republishing it with other text, or once the item has run or expired, is refused.
The board sees each item in `snapshot.runs` with its exact command, shell, folder and whether it needs administrator rights, and Run sends only the item's ID and identity through the board's action checks (exact Host and Origin plus the per-session token), never command text.
On the board a run item is a card in the Command Center stack, counted in the header badge while it is ready or running.
The card shows why the CFO needs it, the shell with its mark (the GNU Bash mark for Git Bash, a terminal glyph for either PowerShell, since Simple Icons carries no PowerShell mark), an Admin badge with a shield when it runs elevated, the exact command in a monospace block that wraps and has a copy button, and the folder it runs in.
One button runs it, enabled only while the item is ready and the board is connected, and it says where the command runs: **Run in PowerShell**, **Run in PowerShell 7** or **Run in Git Bash**, or **Run as administrator** with a note that Windows will ask to confirm.
The card then says Running, and Finished or Failed with the exit code, or Expired or Withdrawn by the CFO; its output shows as a terminal shows it, read every second from `GET /api/runs/<id>/output` while the command runs and kept with its exit code after, newest at the bottom; a finished item moves to the inbox's history with the same words.
A goblin's wait on the Overlord himself (`cfo notify --waiting-on overlord`, a review item whose id is `waiting-<task>-<n>`) shows as a status card: who waits and what for, in the goblin's own words, and no answer box; it closes by itself once the goblin reports again, other than with a question, or the CFO answers it.
Its words show once: a wait with a page opens it from the card's one Open review button and shows no page preview, which would repeat them and open the page a second time.
The goblin's words keep their lines and read formatted, never as raw markup: the first line heads the card, `**bold**` is bold, `- ` lines are bullets, and a value in backticks is code with a copy button beside it.
Values the Overlord must enter somewhere, such as DNS records, go in a Markdown pipe table on the lines after the lead (a header row, a separator row of dashes, one row each), which the card shows as a table, each backticked value with its own copy button; `cfo notify`'s help shows this one way to send a table.
The Command Center's list rows, the pinned CFO bar, alerts and the Answer button read a wait on one line, without its table.
The card opens what the wait points at, as its main button, with Dismiss beside it: the page the goblin named with `--lavish` (Open the page), else that goblin's newest question still waiting in the Command Center (Open its question), else a file it delivered with `cfo deliver` (Open the file), else the link it gave with `--link` (Open the link); a wait that names none of these offers Dismiss alone.
A web address the goblin only names in its words is never offered as the link, and `--link` must be https (or plain http on this machine or the tailnet) with no credentials, query or fragment, checked before anything is recorded.
The board never opens a path from a wait's text, only the link the goblin gave or an item it already holds.
An interactive item (`cfo run-request --interactive`, and every goblin's command) runs its script in the window itself, with nothing between it and the console, so a sign-in, `cfo attach` or anything else that needs a real terminal works there; its exit code is recorded when the script ends, the shell stays open for him to use until he closes the window, and nothing of its output is kept, so its card says it runs in its own window and shows no output.
A goblin hands him a command with `cfo notify <task-id> --waiting-on overlord "<why>" --run <file>` (or on a `--blocked` question): a `.ps1` file runs in Windows PowerShell and a `.sh` file in Git Bash, in the goblin's worktree, never elevated, and the file is read before anything is recorded.
A wait that carries a command is that run card alone, named for the goblin (`<goblin> asks you to run this`), with the goblin's words as its title; a question keeps its own card beside it.
The goblin's notify leaves the command in `state/goblin-runs-inbox`, and the supervisor takes from there only what can be a live goblin's own item: it names a task whose running goblin has that identity, its ID is `run-<task>-<notify>`, and it never asks for administrator rights; its folder is set to the goblin's worktree and it is always interactive, whatever the record says, and anything else is refused, said once among the board's issues, and removed.
The CFO's own run items still reach the board only over the supervisor's pipe, and the folder they once came through stays unread.
When the command ends the goblin, not the CFO, is told that he ran it and its exit code; a goblin's command nobody ran is withdrawn once its goblin reports anything newer, is replaced or is gone, as its wait would be, and reads Withdrawn.
An item runs once and expires 24 hours after it was created; running it again needs a new item.
The registered CFO withdraws an item nobody ran with `cfo run-request --withdraw <id> --reason "<why>"`, over the same pipe and proof: the item reads Withdrawn by the CFO with the reason, leaves the Command Center for the inbox's history, and `state/runs.audit` records the withdrawal on its own line (when, the item, its script digest, `withdrawn` and the reason); Run on it is refused with that reason from then on.
Replacing an item is withdrawing it and publishing the new command under a new ID, and an item that already ran or expired cannot be withdrawn.
A connection repair the Overlord asked for from a goblin's connections panel is the board's own item, not the CFO's, and cannot be withdrawn.
Nor can the terminal the Overlord opened for a credential request, which is the board's own item too.
Run opens a visible console window of exactly the shell the item names: Windows PowerShell 5.1, PowerShell 7 (`pwsh` on `PATH`) or Git Bash (the `bash.exe` beside Git for Windows' `git.exe`, never the WSL `bash.exe`); a shell that is not installed fails the item with the reason.
An admin item launches through `Start-Process -Verb RunAs`, so Windows itself asks the Overlord to confirm, and a declined prompt ends the item failed with that reason.
The window stays open after the command finishes, showing its exit code, until he closes it; a window closed before the command finishes ends the item failed.
When the command finishes, its exit code and the last 64 KiB of its output are on the item, and the CFO receives the result as the Overlord's answer, with the end of the output.
Every run appends a line to `state/runs.audit`: the time, the item's ID, the SHA-256 of exactly the script file that ran, and its exit code, or none when it did not finish.
Finished, expired and withdrawn items are pruned a week after they end; a waiting or running item is never dropped, and a new request is refused while all 64 held items are one or the other.
Output is stored on the board, so the CFO never puts a secret in a run command or requests one that prints a secret.
Items that speak for the CFO (its questions, its own items with their withdrawals, every clear, documents from `cfo deliver` without `--task`, and `cfo answer`) reach the board only over the supervisor's pipe, which proves the sender descends from the registered CFO process, like run items; the question, review and answer inboxes refuse any file that claims to be the CFO's and name it on the board.
Known limit: every goblin runs as the same Windows user as the CFO, and a process of that user can still spoof another goblin's items, including withdrawing them, since a goblin's identity is a hash of its task record, which any of them can read.
It can also spoof the CFO's live presentation notices (`cfo present` without `--task`), text typed into a goblin's terminal, including a line that starts with `CFO:`, and the wake queue and status files the CFO reads.
It can rewrite the supervisor's own database file (`state/.supervisor.json`) while `cfo serve` is stopped, and anything it runs as a descendant of the CFO's harness process is the CFO by this proof.
It can also debug or inject into the CFO process itself: the pipe closes the file inbox path and the pipe squat, not the same-user boundary.

## Credential requests

When the CFO or a goblin needs a secret the project's scope does not hold, such as `STRIPE_SECRET_KEY`, it asks for it by name and the Overlord pastes the value on the board; nobody asks for a value in chat or puts one in a brief:

```powershell
cfo auth request --project acme-shop --why "Charge test cards in the checkout tests" --link https://dashboard.stripe.com/apikeys STRIPE_SECRET_KEY
cfo auth request --project acme-shop --task add-billing --why "Check webhook signatures" STRIPE_WEBHOOK_SECRET
```

A request takes names only, at most ten, each an environment variable name, with one line saying what they are for and optionally the https page they come from, with no credentials, query or fragment in its link.
Anything shaped like a value is refused before anything is filed: a known key prefix (such as `sk_`, `rk_`, `ghp_`, `xox`, `AKIA` or `eyJ`), an equals sign, or long random text, in a name, in the reason or in the link, and no refusal repeats what it refused.
The registered CFO files over the supervisor's pipe, proven the way run items prove it; a goblin files for its own task with `--task <its id>`, from its own terminal, proven the way its questions are, through `state/credential-requests-inbox`, and a request in that inbox that claims to be the CFO's is refused and named among the board's issues.
The board holds each request in `snapshot.credentials`: its ID, a generation minted when the board took it, who asked and for which task, the project, the names, the reason and link, the names the scope already holds, each name's format hint, and its state, open, saved or expired.
It also says where each value goes: the repository, which is the checkout the scope is named for when it is on this machine, the credential scope, and every service of the project's `auth.json` that reads the name, beside the project's goblins, whose `auth.ps1` carries the whole scope.
It never holds a value.
A project's `auth.json` may declare format hints, which the request carries for the card to warn with and never to refuse a value:

```json
"formats": [
  {"names": ["STRIPE_*"], "prefixes": ["sk_", "rk_"], "warn": [{"prefix": "sk_live_", "say": "A live secret key can do anything on the account; use a restricted rk_live_ key."}]}
]
```

A name in `names` is exact or ends in `*` to match every name starting with what comes before it, and the first matching format applies.

A request may also name a local env file with `--env-file <file>`, such as `.env.docker.local` for a project's docker compose setup: each saved value then also sets its `NAME=` line in that file, beside the credential scope.
The file must be an env file name (`.env`, `.env.<name>` or `<name>.env`) at the root of the project's main checkout, never a goblin's worktree; a plain file or one not made yet; ignored by git (`git check-ignore`) and not tracked (`git ls-files`), so a value written there can never be committed.
`cfo auth request` checks it before filing, the board checks it when it takes the request and again before each write, and a goblin's request may name an env file only in its own task's checkout.
The board rewrites the file in place and never replaces it, because goblin worktrees share a project's env files as hardlinks to the same file: every line that sets the name changes, keeping an `export` prefix, and nothing else does; it never writes through a link, and a file swapped for one between the check and the write is refused.
A value is written as it is when it is plain and in single quotes otherwise, which docker compose and the board's own env reader both read literally; a value with a single quote, a line break or another control character stays in the scope only, and the card and the CFO say why.
A value typed in the card's terminal reaches the file the same way, read back from the scope for each name the terminal provably stored.

The save is `POST /api/credentials/save` with the request's ID and generation, a value for each name the Overlord filled, and the names he confirmed replacing.
It is the one board endpoint that takes a secret value.
Every other endpoint that changes anything still refuses one, as the connection repairs always have: each reads a fixed request shape and refuses a body that carries a value, secret, password, token or credential field, as text, an object or a list, before it reads anything else, and `internal/supervisor/value_guard_test.go` reads the board's route table and fails the moment any other endpoint starts reading such a field.
The save is taken only from the board's own page on this machine: its Host must name 127.0.0.1, localhost or ::1 with the board's port, its peer must be this machine, and any forwarding or Tailscale identity header (`X-Forwarded-*`, `Forwarded`, `Via`, `X-Real-Ip` or any `Tailscale-*` header) refuses it, so a board shared through `tailscale serve`, which reaches the same port on this machine, never takes a value.
It also passes every check other actions pass: POST only, the exact Host and Origin, and the per-session `X-CFO-Token`.
It must name an open request and its current generation, and only names that request asks for, each with a non-empty value of one line and at most 16 KiB.
A request takes one save: the same save sent again, or another value for it, is refused, and a request nobody saves expires after 24 hours, when the CFO is told the names still unsaved so it can ask again.
While the request's terminal is open, its save is refused and it does not expire; it expires on the first check after the terminal ends.
A name the project's scope already holds is replaced only when the save confirms it; otherwise the save is refused naming the names to confirm, and nothing is stored.
Every refusal stores nothing and leaves the request open.
The board holds at most 64 requests, never drops an open one to make room, and prunes a closed one a week after it closed.

A taken save goes straight into the project's scope of the credential store through the same store `cfo auth store` writes, Windows Credential Manager or the file store.
The request closes before the board answers, and then the board runs `cfo auth store`'s own refresh: each live goblin of the project gets its `auth.ps1` regenerated from the store and the one-line re-source notice, and the CFO gets a `review` wake naming the names stored, the project and the goblins told.
A saved value reaches goblins only through their `auth.ps1` environment, never their context: that script is an owner-only file under `state/tasktmp/<task>/`, which `cfo cleanup` deletes before it archives the task (refusing to archive one it cannot delete), and the notice names the script, never the value.
Beside that, and the env file a request names, the value is nowhere else: not in `state/` otherwise, the inboxes, the board's snapshot or event stream, its logs, telemetry, a wake, an error message or the save's answer, which names names and states only.
A save's body is never logged, and a panic while one is handled answers with a fixed message and logs nothing of it.

The terminal fallback is `cfo auth store --project <project> <NAME>` on this machine: with no value argument it reads the value from stdin, and a value typed at a console is read without being shown, and its stored line then shows no part of it, not even the redacted shape.
`POST /api/credentials/terminal` with the request's ID and generation opens that fallback for the Overlord: a visible PowerShell window on this PC, a run item the board makes itself, that runs `cfo auth store` for each name the request does not have yet that the scope does not hold, and for each name the scope holds that the request confirms replacing, so he types or pastes each value there and it never passes through the board.
It takes the save's checks (this machine only, the open request and its generation) and opens one window at a time per request; with nothing left to type but names the scope holds, it is refused naming them to confirm.
When the window finishes, the board checks each row it provably stored, a name the scope did not hold before and holds now, or every name it ran for when it finished cleanly, and tells the CFO the names; `cfo auth store` refreshed the project's goblins itself.
A clean finish closes the request as saved, like a save; a window that failed or was closed leaves it open with what it stored recorded, unless every name is stored.

On the board each open request is a card in the Command Center, counted under Open Command Center and eligible for a Windows notification only while the tab is hidden or the window is minimized.
The card says who asks and for which project, and has a row per name: where its value goes (the repository, the credential scope, the env file the request names, shown as `File .env.docker.local · gitignored, checked · local dev`, and the goblins' `auth.ps1` and each `auth.json` service that reads it), what it is for, the page to get it from, and a hidden field to paste it into.
A field never holds its value: what is pasted or typed is taken from the edit before it reaches the field and kept by the card in the page's memory, and the field is given one dot for each character, so copying out of it copies dots.
Pasting those dots back into a field is refused: the field stays as it was and a note under it says to copy the value again from where it came from.
It is a plain text field with no name or id and with autocomplete and spell check off, not a password field, because a browser keeps, or offers to keep, what a field held.
Edge and Chrome offer to save what a password field held, whatever its autocomplete says: with one, Edge showed **Save your password?** after a save on the card and named the Microsoft account it would go to.
Edge also keeps what any other text field held, even one drawn as dots with autocomplete off, in its profile's `Web Data`.
With only dots in the field, neither browser offers anything or stores anything.
Nothing blocks pasting into a field, and its value is in neither the field nor the page's markup; **Save** sends it once from the card's memory.
Every field empties once a save is answered, taken or refused, except while the card asks **Replace a stored credential?** about a name the scope holds: **Cancel** sends nothing and keeps the pasted value, and **Replace and save** sends the same values with that name confirmed.
A value that does not start the way its format hint expects, or starts the way a hint warns about, shows the hint's warning under its field, and **Save** still takes it.
Below the table the exact `cfo auth store` lines have **Copy** and **Run**: Run opens the terminal for the names the scope does not hold, and asks the same question before a terminal that would type a stored one; while that terminal is open the card says so, and Save and Run wait for it to end.
A board opened from anywhere but 127.0.0.1, localhost or ::1, such as through `tailscale serve`, shows the card read-only: no field, no Save and no Run, only the commands to copy, which a page without the clipboard selects for Ctrl+C.
A request that closes while its card is open stays on screen with a check on each saved row, how it was stored (pasted on the card or typed in the terminal, saved or replaced), where it went and who was told, and History lists it as saved or expired.

`tests/acceptance/credential_canary_windows.ps1 -Binary <cfo.exe>` proves the path end to end on a scratch home, with a random canary under a throwaway credential scope it deletes at its end.
A stand-in goblin files the request from its own terminal, a browser on a throwaway profile enters the canary on the real card, and the proof counts the files that hold it: none in the browser's profile, `state/`, `data/`, the logs, the snapshot, the event stream or the agent transcripts it is given, and one, the goblin's owner-only `auth.ps1`, which `cfo cleanup` then removes.
A fresh browser profile is not isolation: Edge signs a new profile in to the Windows account by itself and syncs it, which pulls that account's saved entries into the profile and sends up what is typed.
So the proof starts the browser with sync and automatic sign-in switched off on a profile that disallows sign-in, reads the browser's own sign-in state before it opens the board, and fails with nothing typed if an account shows or the state cannot be read; at the end it checks that the profile stayed signed out and holds no saved form entry it did not type.
It also fills a plain sign-in form with values it makes up, which a browser does keep and does offer to save, so a clean result cannot come from a search or a probe that sees nothing.
`-Visible` runs it in a window, where a browser shows its offer to save a password, and checks that none follows the card's save; `-Browser` names another browser, such as Chrome.

## AFK mode

AFK mode is the Overlord's switch for running the fleet while he is away: the CFO decides what his authority covers and logs each decision, what stays his alone is held for him, and nothing on the board prompts him.
`AGENTS.md` holds its terms; this is how the supervisor keeps them.

The supervisor is the only writer of AFK mode's three files in `state/`:

| File | What it holds |
| --- | --- |
| `afk.json` | The switch: whether it is on, the stretch it names (`session`), since when and from where, the allowance read then, and after it turned off, when and from where. A switch the CFO made at his ask names the CFO there and keeps his words (`asked`, `ended_asked`). |
| `afk.audit` | One JSON line for each switch, each decision the CFO logged with its evidence, and each item held for him. Every line carries its stretch, and a switch the CFO made at his ask carries his words as its evidence. |
| `afk-report.json` | The report of the last stretch that ended. |

`cfo afk on` and `cfo afk off` send `afk-on` or `afk-off` over the supervisor's pipe, the one a run item travels over, and need `cfo serve` running.
The supervisor makes the switch only for a process it proves runs in a terminal of the Overlord's own, read from the process at the other end of the pipe: its ancestry, each parent created no later than its child, and its environment.
It refuses, in this order, a process whose environment carries `CFO_ROLE=goblin` or `NO_MISTAKES_GATE`, one whose ancestry reaches the registered CFO, one whose environment names a native terminal (`CFO_HOST_ID`) or a Herdr pane (`HERDR_PANE_ID`), where the fleet runs an agent, and one under an agent harness: one among its ancestors (`claude`, `codex`, `pi`, `kimi` or `node`, by executable name), or a harness's own variable in its environment (`CLAUDECODE`, `AI_AGENT`), which still marks a command whose parents were cut off, as Git Bash's `timeout` leaves one.
A process it cannot read, or one that started after the request, is refused too, never taken for his; a terminal run as administrator is one it cannot read, and the refusal says so.
So is a process whose ancestry reaches neither the Windows desktop (`explorer`) nor Windows Terminal (`WindowsTerminal`): its parents were cut off, as Git Bash's `env` leaves a command with those variables removed too, and as Git Bash leaves the Overlord's own command, so the refusal tells him to run it in PowerShell or cmd.
What it accepts is recorded with the shell the command was typed in, such as `his own terminal (powershell.exe pid 4242)`.

A request that carries `asked`, the words `cfo afk on --asked` or `cfo afk off --asked` passes, is the registered CFO making the switch at the Overlord's ask, and takes another proof.
The supervisor refuses a process whose environment carries `CFO_ROLE=goblin` or `NO_MISTAKES_GATE`, then proves the process runs under the registered primary CFO, the proof a question takes, and refuses one it cannot prove.
The words are kept on one line, and words that are blank or longer than 500 characters are refused.
What it accepts is recorded as `the CFO at his ask (claude pid 4242)`, with his words as `asked` in `afk.json`, as the evidence of the switch's line in `afk.audit`, in the notice in the CFO's wake queue and in the report.
It does not reset a switch that cannot be read, which stays the Overlord's.
This proof says who asks and nothing of whose words those are: the CFO quotes them, and the supervisor cannot tell an ask of his from one the CFO misread or was handed by a goblin, a page or a tool's output.
`AGENTS.md` holds that part, and the record shows him what it was switched for.
His own switch, from the board or a terminal of his own, turns it either way whoever made the last one.

Turning it on reads the allowance from `quota-axi`, writes the log line before the switch, and puts a `review` notice keyed `afk` in the CFO's wake queue; turning it off keeps the report first, so a stretch never ends without one, and the notice tells the CFO to write it into its terminal.
The adversary this proof names is an agent that follows its contract and tries the command, the pipe or a wrapper around either.
A process of the same Windows user that writes `state/afk.json` itself is the boundary the board's other items already have.

The board's own switch, `POST /api/afk` with `{"on": true}` or `{"on": false}`, is under the same proof, made of the program that shows the board.
The endpoint first refuses a body it cannot read, then a request that is not the board's own page on this PC: one whose peer is not this machine, or one a proxy handled, as a board shared through Tailscale serve is.
It then asks Windows which process owns the other end of the connection (`GetExtendedTcpTable`), which for a browser is the browser's own network process, and reads that process as it reads a pipe's caller: its environment and its ancestry, with the same refusals in the same order.
The desktop window started from the Start menu passes, as does a browser he starts from the desktop or from a terminal of his own.
A browser an agent started does not: one a test runner started under `node`, one opened from a goblin's or the CFO's terminal, or one whose harness left its variable in its environment.
A browser whose opener has since exited has parents that stop short of the desktop, so it is refused with the way out: the desktop window, a browser started from the desktop, or `cfo afk on` in PowerShell or cmd.
What it accepts is recorded with the program he started, such as `his own board (goblins-window.exe pid 4242)`, and the switch is then made as the command's is.
Off while it is already off answers as off, and a switch that cannot be read answers on with 409 and off by resetting it.
The adversary is the same one, at the board: an agent that follows its contract and tries the switch from a browser it started, through a proxy or from another machine.
An agent that drives the Overlord's own running browser is his browser to the supervisor, as a process that writes `state/afk.json` is his user; `AGENTS.md` forbids both, and nothing here stops either.

Every snapshot carries `afk`, which is what the board shows of the switch.
Its `state` is `off`, `on`, or `unreadable` for a switch that cannot be read, which is never taken for on.
While it is on it carries `since` and `from`, `asked` (his words, for a switch the CFO made at his ask), `decided` (how many decisions the log holds for the stretch) and `held`: each item the log holds for him in the stretch, with whether it still waits on him, what became of it, its goblin's latest report since and `recommendation`, the choice a held question's asker recommended, empty for none.
While it is off it carries `report`, the last stretch that ended, whose report is kept.
The log's file and each held goblin's status log are read for a snapshot only when they have changed since the last one, and a log that cannot be read is said as the snapshot's error.
`GET /api/afk/report` is that report as the board's page reads it: `{"found": false}` while no stretch has ended, and otherwise who turned it on and off (`from` and `ended_from`, with his words in `asked` and `ended_asked` for a switch the CFO made), its decisions under the headings `cfo afk report` prints, what each goblin finished, what was held with what became of each item as it stands now, how long it lasted and what was spent in the same words, with every list present.
The page and the text list what is held for him first, then what the CFO decided.

`cfo afk log`, `cfo pr merge` and `cfo answer` send a decision as `afk-log`, which the supervisor writes only for a process it proves runs under the registered CFO, the proof a question takes, and only while AFK mode is on.
A decision names its kind (`merge`, `deploy`, `migration`, `install`, `answer` or `other`), what was decided and the evidence it stands on; one without evidence is refused.
A merge word is logged before the merge and its outcome after it, as a second line for the same pull request, so the report never shows a merge word as a merge.
Its evidence names the base tip `cfo pr merge` read a moment before merging; `gh pr merge --match-head-commit` holds only the head, so a commit that lands on the base between that reading and the merge is not seen.
Where the base requires a merge queue the word adds the pull request to the queue instead, and its evidence says the queue tests the merge on the base's tip; its outcome is that the pull request joined the queue, not that it merged.

While AFK mode is on, `POST /api/announce` records every key it is asked about and claims none, so the board shows no alert, sends no Windows notification and never opens the Command Center by itself.
It claims none even when the record cannot be saved, as on a full disk: the board reads a refusal as leave to announce, so while he is away the endpoint answers with nothing claimed rather than with the failure.
What a page asked about then stays recorded, so it is not announced once he is back either.
The page holds the same line by itself: while its snapshot says AFK mode is on, a claim it could not ask for hands its alerts and its Command Center nothing, where with AFK mode off it falls back on what the browser remembers.
A page that was not looking while he was away, such as a tab the browser put to sleep, asks about what it missed when it wakes, and gets it if AFK mode is off by then: he is back, and those items wait on him.
[The desktop window](#the-desktop-window) is silenced the same way: what its own reading of `/api/snapshot` finds it claims here before it notifies, as the page does, and it is handed nothing.
Each cycle the supervisor records every item that waits on the Overlord as held, once in a stretch: a pending question the CFO asked him, with the choice it recommended, an open review item or wait, a run item nobody ran, and an open credential request.
A goblin's question waits on the CFO, which answers it, as the Command Center has it (`forOverlord`), so it is never held for him.
What only he can decide is held for him rather than asked: the CFO publishes it as a `cfo question` with `--recommend`, which the command refuses without one while AFK mode is on and the question offers choices, or as a run item with the exact command.
A run item the board made itself, for a credential card's terminal or a connection's repair, is not held: it is ready only for the moment after his own click.
While it is on, a memory reading under the floor counts toward the memory floor: after two in a row with free memory or commit under 4 GB, and with no start or other change under way, the supervisor pauses the newest live native goblin, by when its terminal host started, that is not pausing, paused or failed at the memory floor before, and has no git push, merge, pull, rebase, cherry-pick or am, or `gh pr merge`, running under its host.
It sends the `memory` pause through the same `cfo pause` the allowance floor sends, so the lifecycle keeps its handoff and the scheduler resumes it after two readings at 5 GB; a change under way, the pause included, starts the count again, so one goblin is paused at a time.
Each pause at either floor is logged as a `pause` line with the readings it stood on, then its outcome, and the report lists them under Paused at a floor; they are not decisions, so the bar's count leaves them out.
An answer recorded as the Overlord's (`cfo answer --record-only --in <where>`) is refused while it is on, by the command and by the supervisor for the same request sent straight over the pipe.

A switch that cannot be read is never taken for on or off.
The board announces as usual and the recovery cycle reports the unreadable switch, the digest and `cfo drain` say `AFK MODE: UNREADABLE`, and `cfo pr merge` refuses to merge.
The Overlord's `cfo afk off` is the way back, under the same proof as any switch: the supervisor logs the reset with the reason the switch could not be read, writes it back to off and tells the CFO.
What the switch held is not known then, so no stretch ends and no report is built; the log keeps what was decided.
His `cfo afk on` is refused until the switch reads again, since it would guess at what the switch held.

The report is built from the stretch's lines of the log, the `done: PR <url>` lines every status log and archived status log holds from that stretch, each held item with what became of it on the board and its goblin's latest report since, and the allowance read when it turned on beside the one read when it turned off.
A held question the log holds an answer decision for is a decision, so the report and `cfo afk status` list it there and not as held.
Only the log says so: a held question the board closed as the CFO's with no decision logged stays in the report as held, not waiting, and says no decision was logged for it.
A held question the CFO answered after the stretch ended, under the standing rules where nothing is logged, reads `answered by the CFO after AFK mode ended` when the report is read again; an answer with no time recorded, or one at or before the end, keeps the word that no decision was logged.
What could not be read, a log line or an allowance, is named in the report rather than left out.

## Nonblocking presentation notices

After a presentation tool succeeds, the goblin that ran it reports the returned safe URL explicitly, from its own terminal:

```powershell
cfo present --id browser-walkthrough-001 --task task-id --kind browser --url http://127.0.0.1:5173/ --ttl 5m
```

A goblin proves itself the way it does for a question: the command must run under the task's own native terminal, so no native hook is needed and a goblin spawned while serve runs can present at once.
Its report names no native session, and the board treats it as live while the task's own runtime evidence is fresh.
`--generation` is optional and refused when it is no longer the task's current generation.
Use `--kind review` for a review surface, and omit the task only from the verified primary CFO's own process ancestry, which a CFO proves in its native terminal as it does for a question.
A URL must be https, or plain http where it never crosses an untrusted network: this machine (127.0.0.1, localhost, ::1) or the tailnet (`*.ts.net` names and 100.64.0.0/10 addresses), whose traffic Tailscale encrypts.
The tailnet URL `lavish-axi` returns is therefore kept exactly as returned and opens on the Overlord's phone through the tailnet as well as on this machine, and every refusal names the rule the URL broke.
For a Scrawl page, use `lavish-axi <file> --no-open`, then report the actual successful session URL; do not republish a user-ended session.
Refresh the same ID only while the activity remains live, and report `--state ended` with the same identity/URL on completion.
A presentation reaches the Overlord's Command Center only when it asks him to watch it, with one line he reads under Waiting on you, at most 300 characters: `--watch "Watch the checkout walkthrough I am running for you"`; a goblin's own test run omits it and stays off his Command Center.
IDs cannot change recipient or URL, and an ended pending record cannot be reopened by a later refresh.
The store and inbox retain at most 128 receipts, URLs exclude credentials/query/fragment and known sensitive paths, and expiry is limited to thirty minutes.
Only declared safe presentation links belong here, not raw tool arguments or arbitrary browser history.
Task indicators require current generation and fresh runtime evidence; primary notices use their exact verified registration, checked on the existing supervisor cycle at most once per minute.
Command Center offers Open page/Open review and Keep in background without redirecting, opening a modal, controlling the native browser, or pausing work.
This is an explicit reporting integration, not interception of every browser tool or a live browser-mirroring stream.

## Local endpoint boundaries

The listener requires a numeric loopback address.
Host and Origin checks, cross-site rejection, a per-instance mutation token, strict request schemas, and bounded request bodies protect the local control API.
These are local browser boundaries, not authentication against another process already running as the same Windows user.
Git previews refuse unsafe revisions, traversal, a symlink, junction or other reparse point anywhere on the file's path, and common credential-bearing paths.
The file itself is opened beneath the task root through `os.Root`, so a link swapped in after those checks still cannot resolve outside it.
Git output, timeouts, cache entries, concurrent previews, and event streams are bounded.
Native terminal frames preserve the actual screen, including anything printed there; avoid displaying secrets in the terminal.
Input bytes and native frames are not logged or persisted by this bridge.
Per-page CSP nonces permit the terminal's generated stylesheets while inline scripts remain blocked.
The separate `style-src-attr 'unsafe-inline'` directive permits CSS style attributes across the page for xterm 6's ANSI truecolor rendering.
This is a page-wide CSS-attribute compatibility allowance; style elements still require the page nonce or a same-origin source, and script, connection and framing restrictions remain unchanged.

## Build and verification

Use Node 24.13.0 and the committed npm lockfile when editing the board:

```powershell
cd frontend
npm ci
npm run typecheck
npm run lint
npm test
npx playwright install chromium --only-shell
npm run test:browser
npm run build
cd ..
go build -o cfo.exe ./cmd/cfo
```

Vite generates `internal/boardweb/dist/board`, which git ignores; do not edit or commit it.
`cfo.exe` embeds it, and a checkout that has not built it embeds instead the one committed file under `internal/boardweb/dist`, a placeholder page that says the board was not built and names the commands that build it, so `go build` and `go test` need Go alone.
`npm run test:browser` first builds the board and every page under `frontend/tests/fixtures`, bundled as the shipped board is, into a folder of that run's own under the system's temporary folder, and the browser tests read those files, answering each request themselves.
No web server runs behind them: the pages are given the origin `http://127.0.0.1:1`, this machine as the board is really served, on a port the browser refuses to connect to, so no test waits on a port or opens a connection the machine can refuse, and a request a test does not answer itself fails at once.
Outside CI every browser test waits, before it starts, while less than 5 GB of memory is free, and fails saying so after a minute: Windows takes about half a minute to release a test's browser process after the test ends, each still holding about 150 MB, so a fast run holds gigabytes on a machine the fleet shares; on a machine short of memory, run one spec at a time.
One unit test, `src/dev-server.test.ts`, starts the dev server on a port the machine picks and loads the board's page and entry module from it, so development mode stays tested.
The board's HTML template and the placeholder page are pinned to LF in `.gitattributes`: Vite preserves the template's newlines, so a build on Windows embeds the same bytes as one anywhere else.
Both Windows CI and the release workflow install from the lockfile, check the frontend, build the board, and fail when the build is not where `cfo.exe` embeds it from: the release before it compiles Go, and CI in a job of its own beside the Go tests, which then runs `internal/boardweb`'s tests over the build.
The runtime executable embeds those assets and requires no Node process.
The board names the build it serves, a hash of its `index.html`, which names every hashed file of the bundle, in the page (`<meta name="cfo-build">`) and in every snapshot, on `/api/snapshot` and on the event stream.
A tab whose loaded build differs from the one served, such as one left open across an install, reloads itself while it is hidden, the event stream is live and the Overlord is not mid-answer (the Command Center closed, no card in it and no comment on a diff keeping an answer not yet sent, no terminal listening to dictation, and no text field holding text), and otherwise shows one line, The board was updated, with Reload; a page loaded without a build, such as from the dev server, never acts.
For frontend development, `npm run dev` listens at `127.0.0.1:5173` and proxies API calls to a supervisor on `127.0.0.1:4310`, translating only that explicit local development origin.
`BOARD_DEV_PORT` sets the port the dev server listens on; it defaults to `5173`, and the translated origin follows it.
`BOARD_SUPERVISOR` sets the supervisor the dev server proxies API calls to, such as an example fixture; it defaults to `http://127.0.0.1:4310`.
Setting neither keeps the defaults, so nothing changes for a developer who does not need them.
Both are permanent developer tooling: on machines where Docker, WSL or another proxy already owns port 5173, a dev server pinned to 5173 can silently serve or test the wrong build.
On such a machine, set `BOARD_DEV_PORT` to a free port and `BOARD_SUPERVISOR` to the running board you want to develop against.

Before any test step invokes `cfo` or `go test`, clear `CFO_HOME` and `CFO_STATE_OVERRIDE` or point both to an isolated temporary home.
For bounded local checks, use `GOMAXPROCS=2`, `go vet -p 1 ./...`, and `go test -p 1 ./...`.
The opt-in `tests/acceptance/native_board_windows.ps1 -Binary <absolute-built-executable> -HarnessBinary <absolute-harness-executable>` requires `CFO_BOARD_REAL=1` and creates an isolated home, git project, named Herdr session, native CFO and worker processes, hooks, and board.
Build the harness from `./tests/fixtures/native-board-harness` with the basename `codex.exe`.
Validate/scan both supplied executables before running them when required by machine security policy; copying or rebuilding a flagged binary is not clearance.
The fixture's disposable `codex.exe` is a deterministic test process, not Codex and not a model invocation; its basename exercises Herdr's real foreground-process contract.
Only the recorded fixture PIDs/session may be stopped during crash checks.
The fixture uses `serve --example`, allowed only with a temporary CFO home and its own state directory.
That mode labels Example workspace and omits the production machine-wide orphan inventory, which otherwise sees unrelated host processes beside the isolated Herdr session.
It retains task monitoring and custody checks and never clears shared decisions or stops shared processes.
See [the implementation evidence](evidence/cfo-native-board.md) for measured overhead, browser results, and the unresolved earlier Defender detections.

## Source provenance

Native contracts were checked against installed versions and the primary [Codex hooks](https://learn.chatgpt.com/docs/hooks), [Claude Code hooks](https://code.claude.com/docs/en/hooks), and [Pi extensions](https://pi.dev/docs/latest/extensions) documentation.
The pinned [Cline Kanban source](https://github.com/cline/kanban/tree/abd4912c27ce6b7f18b5a8106c145fd838e90cc4) supplied adapted board, diff, history, and runtime-stream behavior.
Its Apache-2.0 license, copyright, file links, and modification notes are retained in `frontend/public/assets/NOTICE.txt` and the bundled license files; no upstream NOTICE file was present at that revision.
SIQshift's shared brand stylesheet, desktop/web controls, and shared ShiftGroups supplied inspected card/ghost/focus, native-select and disclosure primitives.
The board's palette and glass come from SIQshift's `packages/shared/styles/brand.css` and SIQstack's site (`src/app/brand.css`): the base and hero surfaces, the green, deep green and blue accents, the glass fill, border and shadow, the wordmark gradient, the electric tab ring and the blue focus ring, by their real values.
The user-approved generated mockups supplied the final dark/mint two-view composition and terminal-goblin artwork, and the 2026-09-30 SIQstack restyle, its regenerated workshop background and the goblin mark the tab and app icons are drawn from.
The board self-hosts three OFL faces, so it renders the same offline: Pixelify Sans for headings at weight 400 only, because heavier weights close its C and G into O; Nunito for body text, never below 15 px; and JetBrains Mono for code.
Their licenses sit beside the font files under `/assets/fonts/`.
The supplied code/workflow references informed review and lineage presentation without adding a graph dependency or an automation editor.
