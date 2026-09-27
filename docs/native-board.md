# Native supervisor and board

`cfo serve` runs one native Go supervisor and serves the embedded React/TypeScript board on `http://127.0.0.1:4310`.
Use `--listen 127.0.0.1:0` for an ephemeral port; the command prints its actual URL.
Node, Vite, Docker, and a browser are not runtime dependencies of the supervisor.
Docker remains an optional project environment managed by the existing worktree profiles.

The service takes the existing `.watch.lock` before opening recovery state.
An existing watcher must finish before `serve` can acquire that singleton; starting the board never kills a watcher or worker.
While `serve` holds it, the Claude CFO's `stop-autoarm` hook still rewakes the idle CFO: it waits on the wake queue, rewakes once for each record no earlier rewake covered, and hosts the watcher itself again if `serve` stops.
Closing the browser disconnects a view, while Ctrl-C in the supervisor terminal, or `goblins stop` from any terminal, stops that process.
Once it holds the singleton and listens, `serve` records its pid and the board's address in `state/board.json`, and removes the record when it exits.
`goblins` with no command reads that record: when the address answers at all it prints the board's link and a status line from the snapshot, or says the board could not read the fleet's state when the snapshot fails, and starts and opens nothing.
Otherwise it starts `serve` detached from its terminal, in a hidden console of its own that the programs `serve` runs share, so no console window opens, with its output appended to `state/serve.log`, waits up to 30 seconds for the board to answer, and opens it in the browser once.
The detached `serve` listens on `127.0.0.1:4310`, or on `127.0.0.1:0` when another program already listens there, so the OS picks a free loopback port and the record holds the real address.
A record whose address does not answer, left by a supervisor that ended without removing it, is replaced by the next start, and a record naming anything but a plain loopback board address is ignored.
`goblins --board` finds or starts the supervisor the same way, opens the board root in the browser every time, and exits 1 naming the link when the browser cannot be opened; it starts, shows and attaches no CFO.
Then `goblins` brings the Overlord to the CFO.
A CFO whose registration in `state/primary.json` names a live process is reused, never started a second time: `goblins` brings its registered workspace and tab to the front and hands its terminal to `herdr`, attached to the session the CFO registered in.
It decides from the registration alone and asks neither the board nor Herdr, so a supervisor that has not checked the registration yet or a Herdr that cannot answer changes nothing.
Otherwise it picks the project (the git checkout its terminal is in, else the only checkout under the projects root, else the one the Overlord picks by number), makes sure Herdr's server runs, and starts the remembered harness (Claude Code by default) as the CFO in a fresh `cfo` tab it creates in that project, since Herdr starts an agent in its pane's own directory.
An old `cfo` tab with no agent in any of its panes is closed when every pane sits at its shell prompt, and renamed to `shell` when anything else runs in one, `goblins` itself included; a `cfo` tab with an agent in any pane is left as it is and no second CFO is started beside it.
It brings the CFO's tab to the front and hands its terminal to `herdr`, which attaches to the fleet's session.
Run inside a Herdr pane there is nothing to attach, so `goblins` only brings the CFO to the front.
`goblins status` reads the board record and prints the board's link, the status line and the supervisor's pid, and exits 1 when no board answers there.
A supervisor started in the background has no terminal for Ctrl-C to reach, so `goblins stop` writes `state/serve.stop` naming the record's pid and waits up to 30 seconds for the board to stop answering.
The supervisor checks for that request on its notification tick, which comes at least every two seconds between cycles, and stops as it would on Ctrl-C, removing its record; a request naming any other pid is left over from a supervisor that already ended, so it is removed and stops nothing.
A supervisor also removes any request left from before it started, so a reused pid cannot stop it.
`goblins stop --force` ends the recorded pid's process tree with `taskkill /T /F` and removes the record instead, and a record whose address does not answer is removed without stopping anything.
Restarting with the same CFO home recovers durable events, evaluations, actions, and lineage.

## Native hook setup

Run `cfo hooks check claude`, `cfo hooks check codex`, or `cfo hooks check pi` to inspect the installed capability contract.
Run `cfo hooks install <harness>` to add the corresponding native lifecycle integration.
The verified minimum contracts are Claude Code 2.1.278, Codex 0.154.0, and Pi 0.85.1.
The check reads the harness's version through PowerShell with the execution policy bypassed, so the script shim npm installs for Codex and Pi runs even where the policy is Restricted.
Kimi remains supported by existing CFO runtime monitoring; this change does not claim Kimi native lifecycle hooks.

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

The supervisor reuses the existing monitor, watch signal signatures, wake queue, task metadata, Herdr steering, and frozen pipeline reader.
One Windows filesystem watcher consumes the spool, with a two-second recovery interval for missed notifications.
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
Malformed records leave a bounded diagnostic and do not wedge subsequent valid work.
An interrupted evaluation is safe to replay; an interrupted external delivery becomes uncertain and is not resent automatically.
Uncertain actions remain visible for operator inspection and can eventually exhaust action capacity if left unresolved.

## Board and orchestration

The header switches between Board and Orchestration, with one main view visible at a time and one contextual pane on the right.
Board groups actual tasks into Tasks, In progress and Completed.
The three columns sit side by side while each has room for a 260 px card, and stack one above the other below that, so a card never squeezes its title or status; the CFO's bar and a column's heading wrap too, so nothing on the board is clipped or scrolls sideways at any width.
Tasks lists backlog rows and briefs nothing has started: a `data/<id>/brief.md` with no live task record, status log or archive entry.
Tasks and In progress are in priority order, top first, and every list of tasks the board shows follows it; Completed stays newest first.
Tasks, In progress and Completed each show as many of their cards as fit, Completed its newest: with the columns side by side, the visible canvas below the list, and with them stacked, one screen below the column's heading, less room for the pager; a page holds as many rows of the tallest card seen at that width as fit, times the columns its grid lays out.
A pager under a list, when one page does not hold it all, says which cards show, 1–5 of 18, with earlier and next buttons, and a sideways touch swipe of at least 48 px turns the page while a vertical one scrolls; in Tasks and In progress a swipe that starts on a card's rank is a drag, not a page turn.
A drag places a card among the cards of its page, and a keyboard move past the page's edge carries the page with the card, keeping its focus.
Tasks lists the backlog's Queued rows in file order, then briefs without a row; In progress lists the goblins in the attention order kept in `state/attention.json`, then any goblin not placed yet, and `cfo fleet-view` lists its goblins in that order too.
Dragging a card, or Alt+Up and Alt+Down on a focused one, sends the whole list's new order to `POST /api/order` with the board's token, which the Host, Origin and token checks guard like every other change.
A Tasks order rewrites only the order of the rows in `data/backlog.md`'s Queued section, each row moving with its indented detail lines while notes, parked rows, the row of a task with a live task record (which In progress lists) and every other section stay where they are, and a brief without a row gets one, `- **<id>** - <id> (repo: <project>)`, at the place it was dropped.
An order that is not exactly the queue the file holds, because a row was added, removed or renamed after the board showed it, is refused with 409 and changes nothing; an In progress order naming a goblin with no live task record is refused the same way.
The board shows the dropped order until a snapshot from the revision the save answered with arrives, and a refused order goes back with the reason under its column.
The snapshot's `memory` is the machine's available physical memory, the standby list included, read with `GlobalMemoryStatusEx`, beside the fleet's 3 GB floor and the 4 GB mark at which the CFO starts the next queued task; the meter at the head of Tasks shows it, and the top queued task is marked Next.
The board starts nothing on its own: the CFO dispatches queued work in the Tasks order.
Start on a queued card sends `POST /api/tasks/start` with the task's ID, guarded by the Host, Origin and token checks like every other change, and the supervisor dispatches it through `cfo spawn <id> --project <p> --brief <data/<id>/brief.md> --harness <h> [--model <m>] [--effort <e>] [--mode <m>]`, the same binary the CFO runs.
The project is the brief's `## Project` line, or the backlog row's repo; harness, model, effort and mode are what the backlog row names, such as `(harness: codex, model: gpt-6-astra)`, then what the brief names on lines of their own, such as `mode: direct-PR` under its Delivery heading, then the fleet's defaults, `claude` on `claude-opus-5-5` at `xhigh` (the default model and effort apply to Claude Code only).
It is refused with 409 and the reason, before anything runs, when the task already has a live task record, is not queued, has no brief, names no project or a harness, mode, model or effort `cfo spawn` cannot take, when less than 3 GB of memory is free, or while another Start runs; a board started without a dispatcher, such as a test fixture's, refuses every Start.
The answer's `passing` is true for a cause that passes by itself, memory under the floor or another Start running, and the card drops that reason once a newer snapshot shows Start unblocked; any other reason stays on the card until its Start is pressed again.
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
A task no native hook has reported takes its status from the fleet's own records: a question it is still waiting on in the wake queue, then what its gate proved, then what Herdr sees in its pane, and it is evaluated once a minute like any other.
Each card shows its short title, then a muted line with the task's repo and status, the status's dot between them, and its pull request, linked only when the reported value is an https URL; the task's own latest status line is in its panel.
The title, the repo and the status wrap onto further lines instead of ending in an ellipsis, as the repo under the panel's title does; a card whose title is still shortened shows it in full in the board's tip on hover or keyboard focus.
Each card in Tasks and In progress also shows a quiet clock under its status, in whole minutes, hours and days (just started, 47m, 2h 14m, 1d 3h), counted from the snapshot's `since`: when a live task's worktree folder was created, which `cfo spawn` makes fresh for each goblin and a switch keeps, so the clock counts the whole session across switches, or its spawn generation's time when that folder cannot be read; or when a queued task's `data/<id>/brief.md` was created.
A queued row with no brief, or a live task with neither a readable worktree nor a generation that records a time, gets no clock rather than a guessed one, and a completed card shows none.
The title is the backlog row's short title, which `cfo spawn` keeps on the task as `title=` in its metadata so it outlives the row, and the task's id only when it had none.
The status line and the pull request come from the current generation's lines only, so a respawned task id shows neither its earlier generation's activity nor its pull request until it reports again.
Cards state progress in plain words, never engine words: Not started, Working, In review gate (with its step, such as In review gate: tests), Waiting on the CFO, Waiting on a goblin by its id, Waiting on CI, Waiting on deploy, Checks passed, Delivered and No fresh evidence.
A goblin waiting on another goblin links to it: a chip on its card and a button beside its status in the panel open the goblin it waits on, and Orchestration draws a dashed line from the waiting card to that goblin's card, apart from the family tree.
A goblin that waits on the Overlord, or has an open question to him, shows Waiting on the CFO, since the CFO carries every question to him.
A reported wait on the Overlord ends once the Command Center item it raised closes: his answer reached the goblin, the item was cleared, or the answer on its page went to the CFO to relay; the card then shows what the goblin is doing, and an answer still on its way keeps the wait.
It keeps its phase's colour, without the amber emphasis that belongs to the CFO's bar, so a wait on the Overlord, a goblin, CI or a deploy is shown in the same calmer sand colour.
The CFO is pinned above the Board's columns, and its bar is the one place on the board that says Waiting on you: it names the first item the Command Center holds for the Overlord, by a question's lead sentence or a review's or command's title, and how many more wait, and otherwise says how many goblins the CFO supervises; its Open terminal button opens the CFO's terminal and hands it the keyboard.
Selecting a card or node opens the same goblin panel from either view: a header with the goblin, its plain status and icon actions, then a Task view and a Terminal view one tap apart on a pill at its top.
The Task view header also shows the goblin's own latest status line, up to 4,000 characters, cut to three lines with Show more while it runs past them and Show less once opened; the Terminal view header is compact, showing only the goblin, its status and the icon buttons, since the live screen shows the latest output.
The Task view holds the workspace, connections, changes, activity and commit history; the Terminal view is that goblin's live native terminal, edge to edge.
The CFO's Task view holds its workspace and connections and then every queued task, the same list as the Tasks column, in the same order, with the same memory meter, drag, keyboard moves and Start; an order or a start made in either shows in both.
Board opens a goblin on its Task view, or on its Terminal view from the terminal button on its card, and Orchestration opens on the Terminal view, which defaults to the registered CFO; once opened, both views stay mounted, so switching keeps scroll position and selection.
There is still no standalone message composer: typing happens in the terminal itself.
Open in VS Code and Open folder require a deliberate click and resolve the selected goblin's fresh, isolated Git worktree.
The API accepts task identity and an editor enum, never a browser-provided path or command; it starts Code.exe directly with literal arguments and removes Electron Node/development flags from its inherited environment.
Successful launch means the application was requested, not that a window was observed.
Queued tasks show their known project and Not started yet, without querying nonexistent task metadata, and their panel is the Task view alone: the Terminal pill appears once the task has a terminal.
Operational wake records remain intact; only a deliberate CFO question or a goblin's blocked notify that offers choices opens a modal.
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
A connector pulses for a few seconds when its goblin reports a new status line or files a wake record.
Arrange resets positions, and storage failures remain visible.
Narrow screens use a collapsible nested list that names the actual parent when indentation is capped.
A child without its own reported native transport explains that limitation without borrowing its owning task's terminal or model.
Bounded accepted-message receipts produce a brief travelling connector pulse and receiving-card glow where exact caller native identity proves the reported parent.
Native-creation receipts instead produce a quiet birth highlight on that proven parent branch plus the child-card glow and entrance; creation is never rendered as a message receipt.
The caller convention is `CFO_SESSION_ID` plus `CFO_SESSION_HARNESS`, with native `CODEX_THREAD_ID` fallback; same-harness or directory/time resemblance never establishes a sender.
When sender identity is unavailable, only target evidence is shown.
Each effect expires independently; initial load, reconnect, hidden-tab return, instance replacement and replayed receipts never fabricate new activity.
Reduced motion uses a short static outline instead of movement.

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
The installed Herdr build `0.9.0-preview.2026-09-08-62431dbd033b` exposes `terminal session observe` and `terminal session control` over NDJSON.
The browser renders its real ANSI screen frames using xterm, loaded the first time a panel shows its Terminal view.
A goblin panel's Terminal view of a Herdr pane is a live view of the pane, and it never resumes or answers an agent.
An open view keeps its pane live and sized to the panel at all times (the Overlord: "it should always be live"): once the view is shown it takes the pane's controller (`terminal session control --takeover`) and sizes the pane to the panel at the chosen text size, 20 px unless Ctrl+Plus, Ctrl+Minus or Ctrl+0 chose another, which native terminals share, and keeps it whether or not the board's window has the focus and while another view is shown; a Herdr window shows the pane at the board's size.
The panel keeps an even inset around the screen, and a panel that changes size asks for the grid that fills it at once, then at most every 40 ms while it keeps changing and once more when it holds still.
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
A paste is typed whole in one request, up to the 64 KiB input limit; an input Herdr refuses ends the view with an unknown outcome.
A refused input, or one whose outcome is unknown, ends the view with the reason in plain words; nothing is resent, and reconnecting starts from a fresh full screen.
The view never scrolls a pane that keeps scrollback: Herdr sends a view only the pane's live screen, never its history, and `pane.scroll` would move the view of every Herdr window on that pane without reaching the board.
So the live screen always follows the pane's bottom, and scrolling up with the mouse wheel, anywhere over the panel and not only over a screen that fills part of it, or Shift+PageUp reads the pane's last 3,000 lines through `POST /api/terminal/history` (Herdr's `pane.read` of its recent output, with colors, on the same socket) and shows them in a terminal of their own over the live screen, at its size and font.
The history scrolls by itself with the wheel and Shift+PageUp and Shift+PageDown; scrolling down at its bottom, Escape, or typing returns to the live screen, and what is typed reaches the pane.
A history read types nothing, so a gate that owns the pane stops typing but not reading history; it reads only the pane the view was verified on, and at most 5,000 lines.
A Claude Code pane whose history holds no more lines than its screen, as Claude Code's fullscreen interface draws in the terminal's alternate screen and keeps no scrollback, scrolls itself instead: every turn of the wheel sends it Herdr's own wheel scroll (`terminal.scroll` with source `wheel`, at most 50 lines, never any text), which the supervisor refuses for any pane whose agent is not `claude`.
The view trusts that judgement for 30 seconds and while its connection lasts; after that, the next turn of the wheel reads the history once more, and a pane that has since built scrollback returns to the read-only history.
Scrolling such a pane counts as working in the board, like typing: a view that does not hold the pane takes it first and the next turn scrolls; a take that is refused, such as a goblin whose review gate owns it, is not asked for again until the Overlord comes back to the board or types, and the wheel says why instead: "A review gate owns this goblin's pane now; scroll it in Herdr."
A NUL key such as Ctrl+Space is typed like any other key.
Shift+Escape moves keyboard focus out of the terminal to the panel's pill; ordinary Escape stays with the pane.
Releasing a drag selection copies it to the clipboard, the way Herdr does, and Ctrl+Shift+C copies the current selection.
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
Every other view of the terminal is told the size the terminal took as `{"type":"size","cols":C,"rows":R}`, so a second window draws the output at that size until it is typed into, which sizes the terminal to it; a size under 20 columns or 5 rows, measured while a panel was hidden, or over 1000 columns or 500 rows is ignored and announced to nobody.
A view acknowledges the output it has drawn with `{"type":"ack","bytes":N}`, N counting every output byte so far: the relay sends a view at most 1 MiB beyond what it acknowledged and keeps reading the host, and a view that falls 8 MiB behind is closed with code 1013 and "The view fell behind the terminal's output.", so the host never waits on a slow window and a view that stays open never loses a byte.
Gate custody and the task's generation are checked when the view opens and on every five-second tick; a key sent under custody closes the view with the gate's reason and is not typed, and a resize sent under custody is ignored, so the terminal keeps its size until a resize arrives after custody ends.
The terminal's end closes the view with its exit code in the reason.

The board draws a native terminal with xterm at the panel's size: its fit addon measures the columns and rows the panel holds and the view sends them as the resize, so the program and the view agree on the size.
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
A divider between the board and the panel sizes the panel, keeping at least 360 px for the panel and 280 px for the board, and a maximize button gives the panel the whole window; a terminal opened from the Board opens maximized and the Task view beside the board, the Orchestration view follows the Task view's choice so its graph stays beside the panel, each view keeping the last choice, and the width and both choices are saved in the browser, a width saved on a wider window is held to the same bounds, and on a narrow window the board and the panel stack and the divider is hidden.

Holding Ctrl+Shift+Space in a terminal, native or Herdr, dictates into it with the browser's own speech recognition, so nothing is installed.
It listens in the browser's language while the keys are held, a Listening pill says so, and releasing any of the three keys types the phrases it recognised as one line through the terminal's paste, so nothing is sent until Enter.
A native terminal's paste follows the program's own bracketed paste mode, and the Herdr view, whose screen is redrawn from frames, always sends a bracketed paste, as its clipboard paste does.
Releasing the keys anywhere on the page, the window losing focus or the page being hidden also stops listening, so the microphone never stays open once the terminal loses the keys.
A browser without speech recognition, a blocked or missing microphone, a lost network or silence is explained in a note for six seconds.
Edge and Chrome recognise speech in their vendors' online services, so the audio leaves the machine while the keys are held.

Key-to-echo latency, measured with `tests/acceptance/terminal_latency.mjs` against the example fixture on 25 September 2026: the Herdr view on main e6f7ea97 took p50 74 ms and p95 592 ms with 3 of 100 keys unechoed after 5 seconds and 4.6 s to a live screen, and the native view p50 24 ms and p95 34 to 36 ms with none missed and 0.4 s to a live screen.
With synchronized redraws and the 20 px font, measured with the DOM renderer in headless Edge, the native view took p50 28 ms and p95 41 ms with none missed.
Switching, measured with `tests/acceptance/terminal_switch.mjs` over 30 round trips between two native terminals: when each switch remounted the view it took p50 44 ms and p95 55 ms from the click to the other terminal drawn whole, with 47 blank frames; with every opened terminal kept live it takes p50 16 ms and p95 21 ms with no blank and no half-drawn frame, and a first attach shows no half-drawn frame.
`docs/evidence/cg-board-ux/pr1b/side-by-side.mp4` records one Claude Code session shown at once in the board and in Windows Terminal through `cfo attach`, while the board switches to another native terminal and back and then to the CFO and back: the Claude Code answer arrives while the board shows the CFO, and is already drawn when the board switches back.
The CFO in that recording still runs in Herdr, so its first open shows Herdr's connecting state over an empty pane, which is not the native view.

CFO transport reads the `state/primary.json` registration and binds each queued message to its fingerprint.
The primary CFO writes that registration itself: Claude's SessionStart hook does it after the digest settles custody, and the Codex and Pi native SessionStart hooks do it for a session with no task.
`cfo register` refreshes it by hand.
Registering the same process in the same pane again leaves the file byte-identical, so a compact, clear or resume keeps the fingerprint pending questions, reviews and answers are bound to.
Registration trusts no variable alone: the Herdr pane named by `HERDR_PANE_ID` must have one of the caller's own process ancestors in its foreground, and that harness must hold the home's session lock, taking it only when no live session does.
A CFO can also run in a native terminal, a `cfo host` that tells the program it starts which terminal it is through `CFO_HOST_ID`.
Outside a Herdr pane, registration there needs the terminal's program, named by its host's record, to be one of the caller's own ancestors, and the host to answer on its pipe, since a host that was killed leaves its record behind.
The registration then names that terminal instead of a pane, and it stays valid while the host's record names the registered process as the terminal's program.
A message for a native CFO is typed into its terminal once, then Enter submits it, over a delivery connection of its own: the host acknowledges each part once it has written it into the terminal's input, and the board shows the message delivered once both are acknowledged, and never types it again.
A host started by an older cfo cannot acknowledge, so the board refuses anything it sends that CFO with nothing typed until the CFO is started again.
The board shows a native CFO's terminal in its panel, from the CFO bar and from Orchestration.
`goblins` shows a CFO registered in a native terminal in its own terminal, and `goblins --native` starts a new CFO in native terminal `cfo`, running the remembered harness itself (`claude.exe` for Claude Code) so the terminal ends with it, without the launcher's `HERDR_PANE_ID`.
`goblins --harness claude|codex|pi`, alone or with `--native`, chooses the harness the CFO starts as, and the home remembers it in `state/cfo-harness` for every later goblins start; claude is the default.
The board's first run starts the Claude Code CFO its page offers, whatever harness is remembered.
In a native terminal Claude Code runs as `claude.exe` and codex and pi as their npm script shims through `cmd /c`, as a native goblin's do; in Herdr Claude Code starts with `herdr agent start`, and codex and pi, whose npm script shims Herdr's Windows agent start cannot run, are typed into the `cfo` tab's shell, as a Herdr goblin's typed launch is.
A harness whose program is not on PATH is refused, and nothing is remembered or started.
A CFO already running keeps its harness, and goblins says the choice applies to the next start; a Codex or pi CFO is told it has no wake path, because only Claude Code's Stop hook wakes the CFO.
`cfo attach` shows a native terminal in any console: the registered CFO's, or the one named; `--state <dir>` names the fleet's state folder for a console that does not inherit the supervisor's environment.

The panel's Open in terminal button, shown while it shows a terminal, opens that terminal in a new Windows Terminal window beside the board, through `POST /api/terminal/open`, which takes only what the view shows and runs the supervisor's own programs.
For a goblin or CFO in Herdr the pane is proved as its view proves it, Herdr brings its workspace and tab to the front, and the window runs `herdr --session <session>`, so it opens on that pane; for a native terminal the window runs `cfo attach --state <dir> <terminal>`, since a Windows Terminal window does not inherit the supervisor's environment.
A goblin whose no-mistakes gate owns its task is refused with the gate's reason, with nothing brought to the front and no window, as typing into it is; the CFO's own terminal has no gate.
A board without Windows Terminal, or a terminal it cannot prove, says why under the button.
With no CFO registered, `goblins` and `cfo attach` show native terminal `cfo` while its host answers, since the CFO started there may not have registered yet; a CFO registered in Herdr always comes first.
The board follows the same rule for the CFO's panel, so a CFO the first-run page started can be answered there, for example Claude Code asking whether to trust its folder, before it registers.
Claude Code registers the CFO only after its onboarding and sign-in, so while native terminal `cfo` is up with no CFO registered the snapshot says `cfo_starting` and carries no registration problem, the board opens that terminal by itself once per page (a panel the Overlord closed is not reopened), and the CFO bar says "Starting: sign in to Claude Code in its terminal".
The board's root shows the first-run page whenever no CFO runs (the snapshot's `cfo_runs`: a CFO registered and running, or native terminal `cfo` up for one that is starting) and the normal board once one does, with no path of its own, so an installer or shortcut that opens the board's root lands on it.
The page lists the git checkouts directly in a projects folder, opening on the recorded `CFO_PROJECTS_ROOT`, and shows Claude Code, Codex and Pi with whether each is on PATH and has a saved sign-in (`~/.claude/.credentials.json`, `~/.codex/auth.json`, `~/.pi/agent/auth.json`).
Start records a new folder as the machine's projects root, as `cfo install --projects-root` does, and starts Claude Code as the CFO in native terminal `cfo` in the picked project, as `goblins --native` does.
Only Claude Code can start today, since goblins can wake only a Claude Code CFO, and a Claude Code the terminal cannot start itself (anything but `claude.exe`) says so.
Start is refused, with the reason, while a CFO runs or is starting, for a folder that is not a full path, cannot be read or holds no checkout, and for a project that is not one of its checkouts; an example board (`cfo serve --example`) records the folder for itself alone, never as the machine's setting.
After Start the board shows at once with the CFO's terminal open. A quiet link, Open the board without a CFO, shows the board while none runs, so goblins at work stay in view, and the CFO bar then offers Start the CFO in place of Open terminal, which leads back to the first-run page.
Keys pass through raw, the terminal follows the console's size, and Ctrl-] leaves it running, whether the console sends that key as a byte or as a Windows key event.
A host refuses to start for a terminal that already runs, so a second start never takes over the first one's record.
`cfo peek` of a native terminal reads its screen from its console, exactly as the terminal's program would read it, rather than rendering the terminal's output: the rows written, without trailing blanks.
For each read the host starts a process of its own that attaches to the terminal's console, reads its window and ends, so a Ctrl-C typed to the terminal, or its console closing, during a read can end only that read, never the host.
A read that fails is an error naming the terminal, never an empty screen.
`cfo spawn --backend native` starts a goblin in a native terminal of its own, named by its task id, instead of a Herdr tab; it is opt-in until native becomes the default.
The harness starts as its own program: claude.exe itself, and codex and pi through `cmd /c`, since their npm shims are scripts, and an argument cmd would read as more than text is refused.
The terminal's environment starts from the one Windows gives a new process of the user, built from the user's and the machine's configured variables, never from the spawning process's own, so nothing the spawning session set reaches the goblin, as with a Herdr pane.
The harness billing keys and every known session marker, such as `CLAUDECODE`, `CLAUDE_CODE_CHILD_SESSION` and the Herdr pane's variables, are dropped from it all the same, then the project's credentials and the launch's variables, `CFO_ROLE=goblin` among them, are added and win: a native task has no credentials script.
A Claude Code setting such as `CLAUDE_CODE_GIT_BASH_PATH` therefore reaches a native goblin only when it is configured for the user or the machine, not when only the spawning session sets it.
The spawn reads the terminal's screen throughout and types only where it recognizes what it reads.
A startup dialog it knows is answered only once it shows, by moving the focus down and checking each move on the screen before confirming: Claude's trust dialog, which focuses "No, exit" first, and Codex's update prompt (Skip) and trust prompt (Yes).
A prompt a spawn may not answer, such as Codex's hook review, or a screen it does not recognize within the startup budget, stops the spawn with the terminal named and its screen quoted.
The instruction is typed into the composer, submitted once the composer shows it, and the spawn succeeds only once the harness shows it working.
Codex's composer and working texts are its known ones, not yet seen in a capture here, and the first live native Codex spawn checks them.
A native spawn that fails closes the terminal it started, which ends the harness and everything it started, and retires the task as a Herdr spawn does; a terminal that already ran under the task's id refuses the spawn's host and is left running.
If the terminal's host still runs but does not answer the close, the spawn's error says so, and the worktree and task record stay, so the task can still be reached.
`cfo send` reaches a native task by its id or `gb-<id>` through its own terminal, never through Herdr: text is typed into the composer, submitted once the composer shows it, and delivered only once the harness shows it working, as the spawn delivers its instruction, and `--key` writes the key straight to the terminal.
A `/` or `$` command gets the completion popup's longer wait before Enter, as on the Herdr path, and is reported submitted once but unconfirmed rather than awaited, because `/exit` ends the harness and `/model` opens a picker; check it with `cfo peek` rather than sending it again.
`cfo cleanup` returns a native task whose terminal has ended, or whose harness waits at its ready composer with no working marker, in which case it closes the terminal, which ends the harness (decision 2339); any other screen, a screen or host record that cannot be read, and a harness whose screens cfo cannot read are refused with or without `--force-archive`.
A missing or stale registration shows on the board as one banner, and in the CFO terminal as its own state, naming what went stale and the fix, `cfo register` in the CFO session.
On Windows normal message delivery holds that registration against replacement and validates the live process/start time, foreground process group, registered agent, pane, workspace, tab and terminal ID before using a required-agent sender.
Missing or changed identity is refused, never passed to the explicit-pane shell fallback.
Herdr acceptance counters establish accepted delivery; the current native contract cannot prove a model response or provide an atomic process-identity compare-and-send operation.
Terminal input pins the exact terminal ID and task generation, checks process ownership and pipeline custody on the schedule above, and consumes ordered input identities once.
Writing native stdin does not acknowledge application acceptance.
Herdr cannot atomically compare the foreground process while writing: if an agent exits after the check, bytes may reach the same PowerShell terminal.
Known exited/replaced sessions are refused, but the board does not claim to eliminate that native check-then-write race.

Workspace details read only declared project/provisioning metadata and configured MCP names.
Configured is not connected; each configured entry is marked configured, never connected, and one shared note says so.
Matching-generation native model evidence takes precedence; otherwise the model is explicitly labeled configured, including a configured default.
Environment values, full process environments, dotenv, auth scripts, MCP commands and headers are never exposed.

### Interface rules

These rules hold for every board surface, and new work follows them.
Recurring tool actions (open in VS Code, open folder, open pull request, refresh, zoom, fit, arrange, close, reconnect) are icon buttons, each naming itself with an accessible label and a tooltip on hover and keyboard focus.
Decisions and one-off commands keep a short word, for example Send decision, Retry or Show the next 300 lines.
Every connector, MCP server, credential, harness and model provider shows a mark beside its name: the brand's mark from Simple Icons where one exists, a plain glyph where the owner withholds its mark, the Model Context Protocol mark for an unknown MCP server and a key for an unknown credential.
Delivery reads as a mark: one check once the supervisor accepted it, two checks once delivered; only a failed or unconfirmed delivery is spelled out, with what to check before sending again.
An answer to a question or on a review item, to a goblin or to a CFO in Herdr, and a `cfo answer` to a goblin, that arrives while it is working, such as inside a long tool call, waits in its input until that turn ends, and the turn moves none of Herdr's counters, so it counts as delivered once Herdr submitted it; anything else sent to a working agent, such as a `cfo send` steer, a run result or a review request, still reads unconfirmed.
A review answer's own action keeps one check, because it succeeds whether the answer reached the goblin or went to the CFO; only its review item says which.
Status words say what is happening in plain words, such as Working, In review gate, Waiting on you, Waiting on the CFO or Merged, verifying, never the evidence the supervisor holds.
Text is never smaller than 15 px.
Surfaces sit on three elevation levels, each lighter and more shadowed than the one below, so what floats reads as floating.

## Deliberate CFO questions

Worker alerts and natural-language questions do not automatically become user modals; a goblin's blocked notify that offers choices is the one exception, described under Goblin questions.
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
The Command Center shows one item at a time as a stack, a question, a review item or a run item, the CFO's own items first, then goblins in the In progress order, each goblin's by longest wait, then goblins not placed yet by longest wait (the snapshot's `attention` names the placed ones), and a horizontal swipe on touch screens moves between them; the card stands alone, with no edge of the next one behind it, and its text is sized to read at a glance (19 px body, 22 px titles).
A card's own action row holds everything: Back, its place such as 2 of 4, and Next on the left while more than one item waits, and its answer on the right; closing keeps every item for later.
Each card sends only its own answer.
The moment the Overlord sends from a card, an answer, a review answer or a Clear, its check draws with Sent (or Opened, Downloaded or Cleared) and three quarters of a second later the next open item follows, passing over any sent in this sitting, while the action is delivered in the background; CFO received or Delivered to <goblin> joins the check if delivery lands while it shows, and with nothing left it shows You're all done and the Command Center closes.
A request the board refuses, or a delivery that fails or goes unconfirmed, brings its card back, opening the Command Center if it was closed, with what went wrong, and a refused request can be sent again with Retry under the same request identity; a run card stays to show the command's result.
A click on the dimmed board outside the card closes the Command Center, and a click anywhere outside the open inbox closes the inbox.
A card answered elsewhere while on screen, such as with `cfo answer`, keeps its place until the Overlord moves on: it shows its delivery marks, or once closed a check on the chosen option, the other options dimmed and Answered by you or Answered by the CFO with the time.
Drafts survive closing, reconnecting and moving between cards, and the header button, whose badge counts what waits on the Overlord, opens an inbox of those items, the live pages (review pages and browser walkthroughs) and a history of what he answered, cleared or ran, newest first by when each closed.
A goblin panel whose goblin is waiting on the Overlord offers Answer, which opens the stack at that goblin's question or review item.
Once submitted, every tab displays the durable answer rather than an unsent local draft.
Answers retain their question and CFO identity and enter the durable native CFO message queue, never a worker send or gate approval.
Claude Code, Codex and Pi primary-context guidance routes explicit user decisions through this command.
Publish from the same registered primary shell, continue independent work or finish the turn awaiting the reply, and do not also open a native prompt tool: a normal message cannot answer a correlated native Codex/Pi prompt.
Duplicate HTTP/SSE outcomes cannot cause a second delivery; interrupted delivery becomes uncertain.
A replaced CFO's pending questions become superseded instead of reopening unanswerable modals.
The store bounds question history at 128 records, retires cleared and answered history first, and defers overflow when all questions remain pending.
The bounded publication inbox is admitted in timestamp order, not hash-filename order, and rollover keeps its cutoff below the incoming timestamp so deferred and same-time questions are not discarded.
Conflicting, corrupt or oversized inbox records leave bounded diagnostics and cannot stop unrelated native events.

## Goblin questions

A goblin's `cfo notify <id> --blocked "<question> options: a (Recommended) | b"` also opens the modal, labelled with the goblin and its artwork; the first choice that ends with `(Recommended)` is shown first and marked, like a CFO recommendation, and the mark is stripped from every choice.
When every choice starts with the goblin's own letter in order, such as `A) `, `b. `, `(c) ` or `D: `, the card drops those letters from what it shows, so each choice carries one letter; the answer is still the goblin's choice word for word.
A goblin can attach one review image to each choice with `--image <path>`, repeated in the order of the choices, so the Overlord picks by picture: the card shows a thumbnail for each choice, and any thumbnail opens a full-size gallery with its position, side buttons, arrow keys, swipe, click-to-zoom and a Choose button for that image's choice.
While the gallery is open it replaces the card, so a strip of the question's thumbnails under the image jumps straight to any other image.
When the asking goblin has a review page live, the card and its gallery offer it with Open review; [Review items](#review-items) says which page.
An image must be a PNG, JPEG, GIF or WebP of at most 10 MiB inside the task's worktree, task scratch or data directory, reached without a symlink or junction, and `cfo notify` refuses a wrong count or a bad image before anything is recorded.
The board never sees an image's path: it serves image n of a question at `/api/questions/<id>/images/<n>`, checks the file again on every request, and stops serving once the task restarts or ends.
A blocked notify without an `options:` marker, and every other worker alert, stays in the CFO wake queue only.
Only a process running under the task's own Herdr pane can surface its notify, by the same foreground-harness proof `cfo register` uses.
A notify that fails that proof or offers more than eight choices still wakes the CFO, and `cfo notify` prints why the board could not show it.
The question is bound to the task generation and pane that asked.
The Overlord's answer goes to that goblin's pane through Herdr exactly once, never through the CFO and never to a respawned or moved successor.
The notify then reads answered: `cfo drain` prints the board's answer and acks the record without `--ack-blocking`, and the monitor stops re-asking it.
The CFO still acks it in the ordinary way.
Once the CFO acks a notify it handled itself, the board retires its copy, and an answer queued before that ack is refused with nothing sent.
One window stays open: if the CFO answers with `cfo send` and the Overlord answers on the board before the CFO acks, the goblin receives both, each labelled with its sender.
The CFO answers a goblin's question in a structured way with `cfo answer <question-id|wake-seq> --option <choice> [--note "<text>"]`, from the registered primary CFO only.
The choice is named in full or by its first word (the `a`, `b` labels goblins give their options), and a label that names two choices, a choice the question does not offer, a notify without choices, one already answered or handled, and a restarted goblin are refused before anything is sent.
The goblin receives `CFO: decision <seq>: <choice>. <note>` the way `cfo send` types, the notify reads answered so `cfo drain` acks it without `--ack-blocking`, and the board records the choice, that the CFO gave it and when, even when the CFO drains the notify first.
Every answer, on the board or through `cfo answer`, is recorded on its question as `answered_option` (the choice; empty for a written answer), `answered_by` (`cfo` or `overlord`) and `answered_at`, so a closed question shows which option was chosen.
A question that closed without an answer, because its goblin restarted or ended or the CFO handled it, stays listed with its reason until the Overlord clears it (`question_clear`), or until 128 questions are held and it is the oldest superseded one, which makes room for a new question; a pending question cannot be cleared or dropped, so an unanswered decision is never hidden.

## Working and waiting reports

A goblin that resumes, or waits on something, says so without asking anything:

```powershell
cfo notify <id> --working "wiring the store"
cfo notify <id> --waiting-on <task-id|overlord|ci|deploy> "<why>"
```

Both write a status line only, so they wake nobody, and a newer one of them replaces an older blocked or failed reading on the board while the question itself stays in the CFO's queue.
The task reads `working` with the reason, or `waiting` with the reason and `waiting_on` naming the target, unless a newer question, the gate's own decision, or a merge says otherwise.
A wait on another task clears itself once that task reports done, and a wait on CI or a deploy lasts until the goblin reports again; the CFO releases any wait with a `--working` line of its own.
Waiting on the Overlord is the one wait that wakes the CFO: it also opens a review item for him, named `waiting-<task>-<wake sequence>`, which he can answer or clear, and which is withdrawn once the goblin reports anything newer.
So `--waiting-on overlord` is only for a wait on the Overlord personally: his sign-in, his click, his page.
A choice the CFO can make, such as whether to start something now or after a reset, is an actual question and uses `--blocked` with options.

A wait whose answer the Overlord gives on a Lavish page names the page:

```powershell
lavish-axi .lavish/plan.html --no-open
cfo notify task-id --waiting-on overlord "pick a plan" --lavish .lavish/plan.html
```

The notify opens the page without a browser, and refuses, recording nothing, when the file is not an HTML page or `lavish-axi` cannot show it.
The page's link goes into the wait's line, so the CFO's wake carries it, and onto the wait's review item.
`cfo serve` then polls the page, one bounded `lavish-axi poll` at a time, for as long as the item is open, and is the only one that does: a poll hands the Overlord's feedback to whoever runs it, so nobody, goblin or CFO, polls a page themselves.
Whatever becomes of the page reaches the CFO as a `review` wake, retried until the queue takes it, and then the item closes: his feedback, saved whole under `state/reviews/feedback/` for the CFO to read and relay; the review ended; the review window disconnected; or a page that cannot be polled three times running.
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

A goblin runs it from its own pane, proven the way its questions are; the registered primary CFO omits `--task`, and only a goblin's item takes images.
The ID is 8 to 128 letters, digits, dots, dashes or underscores; republishing the same ID with the same content changes nothing, and other content under that ID is refused.
Up to twelve images, each a PNG, JPEG, GIF or WebP of at most 10 MiB inside the task's worktree, task scratch or data directory, are checked like a question's and copied under `state/reviews`, so the item outlives the worktree and the goblin; `data/` is never used, because it is pushed.
A `--lavish` link follows the presentation URL rule below, and a refusal names the rule it broke.
`--lavish` also takes the page's HTML file, from a goblin or from the CFO: the command opens it without a browser, refusing a file that is not an HTML page or that `lavish-axi` cannot show, and the item carries the page's link.
`cfo serve` then polls that page exactly as it polls a page wait, and whatever becomes of it reaches the CFO as a `review` wake, keyed by the goblin or, on the CFO's own item, by the item's ID, and the item closes.
For another round, publish the page again under a new ID.
An item stays open until the Overlord clears it (`review_clear`), its reporter withdraws it with a reason, or the supervisor retires it; nothing expires it, and a `cfo serve` restart keeps it.
The supervisor retires a goblin's item nobody waits on any more, on every reconcile: a wait on the Overlord once its task reports anything newer, any other item once its task reports done after publishing it, and every item of a task that is gone, such as one cleaned up; a goblin's page or images stay while it keeps working, asks or waits, because it still wants the Overlord's look, and the item reads Withdrawn: <task> finished: <report> or <task> is gone.
A delivered document is never retired, because its copy outlives the goblin: it stays until the Overlord opens, downloads or clears it.
When the CFO answers a goblin's question with `cfo answer`, the goblin's waits on the Overlord raised up to that question close at once, since it has what it was waiting for, and each reads The CFO answered <task>'s question.; a wait it raised after the question stays open.
The registered primary CFO can clear any open item with `cfo review --clear <id> --reason "<why>"`, such as one a retired goblin left; the item reads Cleared by the CFO: <why>, and `state/reviews.audit` records every clear the CFO makes, with its time, item, goblin and reason, one per line.
The Overlord can instead answer it (`review_answer`): his text goes once to the reporter, the goblin's own pane while it is the same task generation or the CFO that reported it, and the item closes as answered; an answer for a goblin that restarted or ended goes to the current CFO instead, and the item reads `delivered: false`.
An answer the goblin received also reaches the CFO as a `review` wake that asks nothing, so the CFO sees every answer the Overlord gives.
The board sees each item in `snapshot.reviews` with an image count, never a path or a digest, and fetches image n at `/api/reviews/<id>/images/<n>`, checked again on every request.
A new review item waits in the Command Center inbox under the badge instead of opening the stack, and the browser tab's title counts everything waiting, so a board in a background tab shows it too.
The board alerts on what needs the Overlord or finished, comparing each snapshot with the one before (the first snapshot a page sees alerts nothing): a new question, review or run item, a goblin whose evidence reads blocked or failed or whose own latest report is failed, and a goblin done with its pull request, read from its evidence or its own report; a goblin's own blocked report does not alert, since the question it raises does.
Each alert is a toast at the bottom right that opens its item when clicked, fades after eight seconds unless the pointer or keyboard rests on it, and can be dismissed; at most four show, newest at the bottom.
While the tab is hidden or its window is not in front, each alert is also a Windows notification through the browser's Notification permission, asked for once, with the first alert; clicking one brings the board forward on that item.
Its card shows the title, its images as thumbnails that open the same full-size gallery as a question's, and its own page, when it has one, as a preview that opens the page with Open review.
An item whose page the supervisor watches (an HTML page given with `--lavish`) is answered on that page, so its card has no text box: it finishes when the Overlord sends or ends the review there, or he closes it with Clear.
Any other item takes a written answer with Send answer or closes with Clear.
`cfo deliver` hands the Overlord a document the same way: the registered primary CFO delivers any file it can read, a goblin only one from its worktree, task scratch or data directory, at most 64 MiB, and the file is copied beside the item so it outlives the original.
`--url` names where Open goes instead of the copy, such as a hosted page, under the same rules as a presentation link (https or plain http on this machine or the tailnet, no query, no credential in the path).
The card shows the file's type, name, size and sender with Open and Download, Open only when the browser can open the copy or there is a `--url`; the board fetches the copy at `/api/reviews/<id>/document`, in the browser only when it is a PDF or a PNG, JPEG, GIF or WebP image by its content, and as a download for anything else, HTML and SVG included, never sniffed.
Opening or downloading it clears the item as Opened or Downloaded, which History keeps, so a document leaves the queue once he has it.
A question's card offers Open review only for its asker's most recent live review page, from the same goblin session or the same CFO registration, so it never opens another task's page or one a replaced asker left behind; page links read Open review, never Lavish.
A closed item moves to the inbox history as You wrote: <answer>, Cleared, Opened or Downloaded for a document, the CFO's reason when the CFO cleared it, or Withdrawn: <reason>; an answer still on its way reads not yet delivered, and one whose `review_answer` action failed or became uncertain carries the same warning marks as a question.
Only `delivered` earns two checks: an answer the CFO took over because its goblin was replaced reads Sent to the CFO: <answer>, and an undelivered answer whose action has aged out of the snapshot reads delivery no longer recorded instead of being assumed delivered.
Closed items and their copies are pruned a week after they close; open items, answered items whose answer is still on its way, and a closed wait on the Overlord while its goblin's latest report is still that wait are never dropped, and a new item waits in the inbox while all 128 held items are one of these.
The API contract for the board is `data/board-ui/api-contract.md`.

## Run items

A run item is a command the CFO needs the Overlord to run, such as a PowerShell or Git Bash script or a step that needs administrator rights; he runs it with one click from the Command Center instead of copying and pasting it:

```powershell
cfo run-request --id install-tool-1 --title "Install the tool the build needs" --shell powershell --command-file C:\temp\install.ps1
cfo run-request --id fix-acl-1 --title "Grant the service account access" --shell powershell --admin --command-file C:\temp\acl.ps1
```

`cfo run-request` hands the item to the supervisor over its named pipe, and the supervisor itself proves the sending process runs under the registered primary CFO, the proof `cfo question` uses: it walks up from that process to the CFO, each ancestor created before its child, since Windows reuses PIDs.
The sending process must also have started before it connected, so a process that later took its PID proves nothing.
The supervisor drops a client that sends nothing within 10 seconds and gives each request 20 seconds for its proof, and `cfo run-request` waits 30 seconds for the answer.
The pipe is the supervisor's own: it creates the first instance of its name, waiting up to two seconds for a stopping supervisor to let go, grants the current Windows user alone, and rejects remote clients.
A supervisor that finds the name still taken serves nothing and lists that among the board's issues, and every command that uses the pipe sends only to the process holding this home's watch lock, so a squatter never receives a request.
The supervisor thus proves the sending process descends from the process `state/primary.json` names: a request from a process outside the CFO's tree is refused before anything is written, and an item planted in the state directory never reaches the board; a request needs the supervisor (`cfo serve`) running.
Processes of one Windows user are peers, though, and a same-user process that rewrites `state/primary.json` or starts a process with a spoofed parent can still pass the check, so it is not a boundary between processes of the same user.
The Overlord reading the exact command before Run, and Windows UAC for an admin item, remain the final check.
The command file is read once: the supervisor stores its text as `state/runs/<digest>/command.ps1` or `command.sh`, which is what runs, so quoting cannot change it, and the item runs in the CFO home unless `--cwd` names a folder.
The ID follows the review item rule: republishing it with the same text changes nothing while the item waits, and republishing it with other text, or once the item has run or expired, is refused.
The board sees each item in `snapshot.runs` with its exact command, shell, folder and whether it needs administrator rights, and Run sends only the item's ID and identity through the board's action checks (exact Host and Origin plus the per-session token), never command text.
On the board a run item is a card in the Command Center stack, counted in the header badge while it is ready or running.
The card shows why the CFO needs it, the shell with its mark (the GNU Bash mark for Git Bash, a terminal glyph for either PowerShell, since Simple Icons carries no PowerShell mark), an Admin badge with a shield when it runs elevated, the exact command in a monospace block that wraps and has a copy button, and the folder it runs in.
One button runs it, enabled only while the item is ready and the board is connected: **Run**, or **Run as administrator** with a note that Windows will ask to confirm.
The card then says Running, and Finished or Failed with the exit code, or Expired; its output shows as a terminal shows it, read every second from `GET /api/runs/<id>/output` while the command runs and kept with its exit code after, newest at the bottom; a finished item moves to the inbox's history with the same words.
A goblin's wait on the Overlord himself (`cfo notify --waiting-on overlord`, a review item whose id is `waiting-<task>-<n>`) shows as a status card: who waits and what for, one Dismiss that clears it, and no answer box; it closes by itself once the goblin reports again or the CFO answers it.
An item runs once and expires 24 hours after it was created; running it again needs a new item.
Run opens a visible console window of exactly the shell the item names: Windows PowerShell 5.1, PowerShell 7 (`pwsh` on `PATH`) or Git Bash (the `bash.exe` beside Git for Windows' `git.exe`, never the WSL `bash.exe`); a shell that is not installed fails the item with the reason.
An admin item launches through `Start-Process -Verb RunAs`, so Windows itself asks the Overlord to confirm, and a declined prompt ends the item failed with that reason.
The window stays open after the command finishes, showing its exit code, until he closes it; a window closed before the command finishes ends the item failed.
When the command finishes, its exit code and the last 64 KiB of its output are on the item, and the CFO receives the result as the Overlord's answer, with the end of the output.
Every run appends a line to `state/runs.audit`: the time, the item's ID, the SHA-256 of exactly the script file that ran, and its exit code, or none when it did not finish.
Finished and expired items are pruned a week after they end; a waiting or running item is never dropped, and a new request is refused while all 64 held items are one or the other.
Output is stored on the board, so the CFO never puts a secret in a run command or requests one that prints a secret.
Items that speak for the CFO (its questions, its own items with their withdrawals, every clear, documents from `cfo deliver` without `--task`, and `cfo answer`) reach the board only over the supervisor's pipe, which proves the sender descends from the registered CFO process, like run items; the question, review and answer inboxes refuse any file that claims to be the CFO's and name it on the board.
Known limit: every goblin runs as the same Windows user as the CFO, and a process of that user can still spoof another goblin's items, including withdrawing them, since a goblin's identity is a hash of its task record, which any of them can read.
It can also spoof the CFO's live presentation notices (`cfo present` without `--task`), text typed into a goblin's pane through Herdr, including a line that starts with `CFO:`, and the wake queue and status files the CFO reads.
It can rewrite the supervisor's own database file (`state/.supervisor.json`) while `cfo serve` is stopped, and anything it runs as a descendant of the CFO's harness process is the CFO by this proof.
It can also debug or inject into the CFO process itself: the pipe closes the file inbox path and the pipe squat, not the same-user boundary.

## Nonblocking presentation notices

After a presentation tool succeeds, the goblin that ran it reports the returned safe URL explicitly, from its own pane:

```powershell
cfo present --id browser-walkthrough-001 --task task-id --kind browser --url http://127.0.0.1:5173/ --ttl 5m
```

A goblin proves itself the way it does for a question: the command must run under the task's own Herdr pane, so no native hook is needed and a goblin spawned while serve runs can present at once.
Its report names no native session, and the board treats it as live while the task's own runtime evidence is fresh.
`--generation` is optional and refused when it is no longer the task's current generation.
Use `--kind review` for a review surface, and omit the task only from the verified primary CFO's own process ancestry.
A URL must be https, or plain http where it never crosses an untrusted network: this machine (127.0.0.1, localhost, ::1) or the tailnet (`*.ts.net` names and 100.64.0.0/10 addresses), whose traffic Tailscale encrypts.
The tailnet URL Lavish returns is therefore kept exactly as returned and opens on the Overlord's phone through the tailnet as well as on this machine, and every refusal names the rule the URL broke.
For Lavish, use `lavish-axi <file> --no-open`, then report the actual successful session URL; do not republish a user-ended session.
Refresh the same ID only while the activity remains live, and report `--state ended` with the same identity/URL on completion.
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
npm run build
cd ..
go build -o cfo.exe ./cmd/cfo
```

Vite generates `internal/boardweb/dist`; do not edit that output manually.
Commit the regenerated assets with frontend source changes.
The HTML input and embedded HTML/JavaScript/CSS outputs are pinned to LF in `.gitattributes`, because Vite preserves template newlines and Git checkout conversion otherwise reports rebuilt assets as modified on Windows.
Both Windows CI and release workflows install from the lockfile, check the frontend, regenerate assets, and fail if the committed bundle differs before compiling Go.
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
The user-approved generated mockups supplied the final dark/mint two-view composition and terminal-goblin artwork.
The board self-hosts three OFL faces, so it renders the same offline: Pixelify Sans for headings at weight 400 only, because heavier weights close its C and G into O; Nunito for body text, never below 15 px; and JetBrains Mono for code.
Their licenses sit beside the font files under `/assets/fonts/`.
The supplied code/workflow references informed review and lineage presentation without adding a graph dependency or an automation editor.
