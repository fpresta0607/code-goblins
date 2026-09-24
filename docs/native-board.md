# Native supervisor and board

`cfo serve` runs one native Go supervisor and serves the embedded React/TypeScript board on `http://127.0.0.1:4310`.
Use `--listen 127.0.0.1:0` for an ephemeral port; the command prints its actual URL.
Node, Vite, Docker, and a browser are not runtime dependencies of the supervisor.
Docker remains an optional project environment managed by the existing worktree profiles.

The service takes the existing `.watch.lock` before opening recovery state.
An existing watcher must finish before `serve` can acquire that singleton; starting the board never kills a watcher or worker.
While `serve` holds it, the Claude CFO's `stop-autoarm` hook still rewakes the idle CFO: it waits on the wake queue, rewakes once for each record no earlier rewake covered, and hosts the watcher itself again if `serve` stops.
Closing the browser disconnects a view, while Ctrl-C in the supervisor terminal stops that process.
Restarting with the same CFO home recovers durable events, evaluations, actions, and lineage.

## Native hook setup

Run `cfo hooks check claude`, `cfo hooks check codex`, or `cfo hooks check pi` to inspect the installed capability contract.
Run `cfo hooks install <harness>` to add the corresponding native lifecycle integration.
The verified minimum contracts are Claude Code 2.1.278, Codex 0.154.0, and Pi 0.85.1.
Kimi remains supported by existing CFO runtime monitoring; this change does not claim Kimi native lifecycle hooks.

Default destinations are `~/.claude/settings.json`, `~/.codex/hooks.json`, and `~/.pi/agent/extensions/cfo-native.ts`.
Use `--config-dir <absolute-directory>` for a custom harness home or an isolated test configuration.
Setup preserves unrelated JSON hooks/settings, takes a first backup before changing existing JSON, and replaces only its owned helper.
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
Tasks lists backlog rows and briefs nothing has started: a `data/<id>/brief.md` with no live task record, status log or archive entry.
Completed lists verified delivery, and within the last week at most 20 entries of history: tasks cleanup finished, from a status log left without its record or one the archive holds, and pull requests merged into a fleet repository, read from merge commits on origin's default branch of this home and each checkout under the projects root, locally and without a forge call.
A merged pull request whose live task already shows the merge, in phase merged or done, appears only on that task's card, which stays In progress as merged-awaiting-verification until landed content is verified.
Any other merged pull request keeps its Completed card, even when a live task reported it: a task whose gate missed the merge, or that is blocked, failed or waiting on a question, keeps its own proven state on its card.
A history card is its pull request link, since it has no live worktree to review.
A task no native hook has reported takes its status from the fleet's own records: a question it is still waiting on in the wake queue, then what its gate proved, then what Herdr sees in its pane, and it is evaluated once a minute like any other.
Each card shows the task's own latest status line and its pull request, linked only when the reported value is an https URL.
Both come from the current generation's lines only, so a respawned task id shows neither its earlier generation's activity nor its pull request until it reports again.
Cards state progress in plain words, never engine words: Not started, Working, In review gate, Waiting on the CFO, Waiting on you, Checks passed, Delivered and No fresh evidence.
Waiting on you shows while the goblin has an open question to the Overlord, ahead of its phase.
Selecting a card or node opens the same goblin panel from either view: a header with the goblin, its plain status and icon actions, then a Task view and a Terminal view one tap apart on a pill at its top.
The Task view header also shows the goblin's own latest status line; the Terminal view header is compact, showing only the goblin, its status and the icon buttons, since the live screen shows the latest output.
The Task view holds the workspace, connections, changes, activity and commit history; the Terminal view is that goblin's live native terminal, edge to edge.
Board opens a goblin on its Task view and Orchestration on its Terminal view, which defaults to the registered CFO; once opened, both views stay mounted, so switching keeps scroll position and selection.
There is still no standalone message composer: typing happens in the terminal itself.
Open in VS Code and Open folder require a deliberate click and resolve the selected goblin's fresh, isolated Git worktree.
The API accepts task identity and an editor enum, never a browser-provided path or command; it starts Code.exe directly with literal arguments and removes Electron Node/development flags from its inherited environment.
Successful launch means the application was requested, not that a window was observed.
Queued tasks show their known project and Not started yet, without querying nonexistent task metadata.
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
Select a visible old/new line or contiguous range, including unchanged context, by its line number, which shows a comment icon on hover and focus.
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

The installed Herdr build `0.9.0-preview.2026-09-08-62431dbd033b` exposes `terminal session observe` and `terminal session control` over NDJSON.
The browser renders its real ANSI screen frames using xterm, loaded the first time a panel shows its Terminal view.
A goblin panel's Terminal view is a live view of the pane: it never claims the native controller, never resizes the pane and never resumes an agent.
Frames arrive at the pane's own size as Herdr lays it out; the view shrinks its font to fit the pane's columns across the panel, never below 12 px, and a wider or taller pane scrolls inside the panel rather than being cropped.
A view is refused when Herdr reports no size for the pane; when Herdr lays the pane out at a new size the view ends, and the panel reconnects on its own to show it whole.
Typing needs no separate step: the view's lease takes keys, escape sequences and one bracketed paste at a time, in order, each typed into that exact pane by Herdr.
The view proves its pane, terminal, process and gate custody in full when it opens and on every five-second tick; an input starts no process to prove it again, and only rereads the task's record (or the CFO's registration) and checks that the verified process is alive, so a changed generation, pane or registration or an exited process is refused on the next key and anything else on the next tick.
A long paste is typed in order, in pieces a Windows command line can carry; a piece Herdr refuses ends the view with an unknown outcome instead of typing the rest.
A refused input, or one whose outcome is unknown, ends the view with the reason in plain words; nothing is resent, and reconnecting starts from a fresh full screen.
The view sends no resize or scroll: the mouse wheel scrolls the panel, and a NUL key such as Ctrl+Space is dropped before sending.
Shift+Escape moves keyboard focus out of the terminal to the panel's pill; ordinary Escape stays with the pane.
Releasing a drag selection copies it to the clipboard, the way Herdr does, and Ctrl+Shift+C copies the current selection.
Closing, switching, disconnecting or restarting invalidates the lease; reconnection starts with a full screen frame, never replayed input.
At most four views are open, frame gaps disconnect, and oversized UTF-8 paste is rejected before sending.
Adjacent printable keystrokes coalesce into bounded ordered inputs; control keys and paste wrappers stay inputs of their own.
Screen reader support is an explicit saved preference in the terminal options menu and changes in place without reconnecting.
The default xterm input mode accepts InsertText/IME Unicode; its optional screen-reader mode has an upstream InsertText limitation, while paste remains supported.
There is no second model session or generated reply.
The native interface exposes rendered screen updates rather than original historical PTY bytes, and omits Kitty keyboard negotiation, graphics and host mouse notifications.

CFO transport reads the `state/primary.json` registration and binds each queued message to its fingerprint.
The primary CFO writes that registration itself: Claude's SessionStart hook does it after the digest settles custody, and the Codex and Pi native SessionStart hooks do it for a session with no task.
`cfo register` refreshes it by hand.
Registering the same process in the same pane again leaves the file byte-identical, so a compact, clear or resume keeps the fingerprint pending questions, reviews and answers are bound to.
Registration trusts no variable alone: the Herdr pane named by `HERDR_PANE_ID` must have one of the caller's own process ancestors in its foreground, and that harness must hold the home's session lock, taking it only when no live session does.
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
Decisions and one-off commands keep a short word, for example Send decision, Later or Show the next 300 lines.
Every connector, MCP server, credential, harness and model provider shows a mark beside its name: the brand's mark from Simple Icons where one exists, a plain glyph where the owner withholds its mark, the Model Context Protocol mark for an unknown MCP server and a key for an unknown credential.
Delivery reads as a mark: one check once the supervisor accepted it, two checks once delivered; only a failed or unconfirmed delivery is spelled out, with what to check before sending again.
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
No choice is preselected and written text is sent only when Other is selected.
Use a new stable ID for a new question, and keep the same ID/content for an uncertain publication retry.
The publisher walks up to 32 process ancestors and verifies the registered CFO PID, creation time and live native identity; a worker cannot escalate on the CFO's behalf.
The UI serializes questions, preserves drafts through reconnects, and supports Escape/Later plus Command Center to revisit.
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
A goblin can attach one review image to each choice with `--image <path>`, repeated in the order of the choices, so the Overlord picks by picture: each choice shows its image, with an icon button that opens it full size.
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
An actual question still uses `--blocked` with options.

## Review items

A review item is something that needs the Overlord's attention without blocking anyone, such as a page of mockups, a report or before and after screenshots:

```powershell
cfo review --id mockups-review-1 --task task-id --title "Pick a task list layout" --image grid.png --image list.png --lavish http://127.0.0.1:4387/session/<id>
cfo review --id mockups-review-1 --task task-id --withdraw "Replaced by mockups-review-2"
```

A goblin runs it from its own pane, proven the way its questions are; the registered primary CFO omits `--task`, and only a goblin's item takes images.
The ID is 8 to 128 letters, digits, dots, dashes or underscores; republishing the same ID with the same content changes nothing, and other content under that ID is refused.
Up to twelve images, each a PNG, JPEG, GIF or WebP of at most 10 MiB inside the task's worktree, task scratch or data directory, are checked like a question's and copied under `state/reviews`, so the item outlives the worktree and the goblin; `data/` is never used, because it is pushed.
A `--lavish` link follows the presentation URL rule below, and a refusal names the rule it broke.
An item stays open until the Overlord clears it (`review_clear`) or its reporter withdraws it with a reason; nothing expires it, a `cfo serve` restart keeps it, and a respawned or retired goblin leaves it listed.
The Overlord can instead answer it (`review_answer`): his text goes once to the reporter, the goblin's own pane while it is the same task generation or the CFO that reported it, and the item closes as answered; an answer for a goblin that restarted or ended goes to the current CFO instead, and the item reads `delivered: false`.
The board sees each item in `snapshot.reviews` with an image count, never a path or a digest, and fetches image n at `/api/reviews/<id>/images/<n>`, checked again on every request.
Closed items and their copies are pruned a week after they close; open items and answered items whose answer is still on its way are never dropped, and a new item waits in the inbox while all 128 held items are one or the other.
The API contract for the board is `data/board-ui/api-contract.md`.

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
