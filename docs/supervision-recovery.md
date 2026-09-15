# Supervision, task context and Windows operation

The task outcome, current worker liveness, native gate state and observation freshness are separate facts.
A restored Herdr tab proves neither a running worker nor completed work.
An absent watcher is `supervisor_down`; a live process without a current heartbeat is `stale_evidence`.
Doctor and fleet JSON report these states explicitly.

## Primary registration and delivery

Register the exact primary Herdr endpoint from the primary's supported session startup path:

```powershell
cfo session-start --primary <session:pane>
cfo supervisor status
cfo drain
```

Registration checks the structural pane/workspace/tab association, supported harness, terminal identity, foreground process, process creation time and host.
No PID, endpoint or restored shell is guessed.
The detached supervisor has its own log and continuous observation loop; stdout is diagnostic output, not wake transport.
Each cycle retries observation and delivers durable queue events through the registered native agent channel.
Spawn, switch, `session-start`, and configured Claude startup/turn-end hooks rearm an existing registration.
After reboot or a new primary process, explicitly register its new identity; the old registration remains a visible notification failure until replaced.
Delivery progress is bound to that primary generation: observer restart with the same primary does not resend, while explicit replacement registration replays still-unacknowledged wakes once to the new primary.
The previous generation's delivery ledger is retained in `delivery.previous.json`.

| Primary state | Delivery behavior |
| --- | --- |
| Verified idle Claude/Codex/Pi/Kimi | Submit once and inspect native acceptance evidence |
| Verified working Codex | Queue once through Herdr; recognized queue text proves submission, not acceptance |
| Occupied Codex composer | Refuse new typing or Enter; preserve the pending text and report the uncertain receipt |
| Working Claude/Pi/Kimi | Report deferred delivery immediately and overdue after one minute; retain the wake and retry after the turn; busy submission is not proven by these adapters |
| Missing, replaced or unreadable primary | Preserve every wake, expose `notification_failed`, require explicit registration |
| Interrupted or unconfirmed submission | Retain an uncertain sequence, avoid blind resend, continue processing later independent wakes |

`cfo supervisor confirm-delivery <sequence>` records an operator-verified submission receipt.
It does not acknowledge the decision queue.
Inspect `cfo peek` before using it; text left in a composer is not a submitted message.
`cfo drain` retains its ordered acknowledgement guard: later decisions cannot acknowledge an earlier unresolved question.
On the next identity-verified delivery cycle, a guarded drain acknowledgement also clears uncertain delivery receipts only through that acknowledged sequence.
This covers a callback that actually reached the primary but whose wrapped/truncated terminal text could not prove submission; no resend or weaker endpoint check is needed.
Queue writes and delivery register/confirm/update operations are serialized across processes and threads.
Persisted producer event IDs prevent replay after a crash from appending the same unresolved episode again.

The supervisor retries observation failures three times per process with bounded delay.
Each task probe has an eight-second deadline, observation uses at most four concurrent tasks, and scans have an overall deadline.
Healthy task observations are persisted independently of a slow task.
These paths never restart a worker, merge code or answer a gate automatically.

## Windows logon recovery recipe

Install a tested candidate explicitly, register a primary, then use Windows Task Scheduler for logon recovery.
Run the supervisor as the same interactive operator account that owns Herdr and its authentication.
Create a private `start-supervisor.ps1` in the selected runtime home:

```powershell
$env:CFO_HOME = $PSScriptRoot
$env:CFO_STATE_OVERRIDE = Join-Path $PSScriptRoot 'state'
& (Join-Path $PSScriptRoot 'cfo.exe') supervisor run
exit $LASTEXITCODE
```

Configure its action as Windows PowerShell with `-NoProfile -NonInteractive -WindowStyle Hidden -File "<absolute runtime home>\start-supervisor.ps1"`.
Use an at-logon trigger, start-when-available, no execution time limit, ignore-new-instance policy, and at most three restarts separated by one minute.
Do not create an unbounded restart loop.
The next primary's `cfo session-start --primary <session:pane>` verifies and replaces stale endpoint registration.
The supervisor lock prevents a logon action and a harness rearm from creating duplicate observers.
`cfo supervisor stop` requests termination of only the recorded process generation.
On restart, the last one MiB of `supervisor.log` is retained as `supervisor.previous.log` before a new log is opened.
The logon recipe is an operator installation action; tests do not register tasks in the user's live scheduler.

## Runtime and instruction ownership

A fresh runtime defaults to `%LOCALAPPDATA%/cfo` on Windows, outside the source checkout.
Explicit `CFO_HOME` and `CFO_STATE_OVERRIDE` retain existing layouts; changing defaults does not migrate or delete an existing fleet.
`cfo install` runs from the source checkout and seeds the selected runtime with the executable, AGENTS/CLAUDE memory, pipeline policy, bundled `.agents/skills` and referenced documentation.
Existing operator memory, policy and instruction files are preserved.
Reconcile differences explicitly during an upgrade; a previously copied Showcase alias is not silently overwritten.
`cfo install --prepare-only` exercises that portable preparation without user environment or hook changes.
The executable fresh-install regression verifies the installed instruction paths, existing-memory preservation and honest supervisor-down output.
Commands remain installed operator tools; bundling a skill does not install its CLI or give every harness the same MCP configuration.

One task entry point is `cfo context <id>`.
It links the saved brief, unresolved decisions, project/worktree/branch/SHA, gate policy and budget, browser identity, test evidence, recap and timestamps.
Spawn and harness switch supply this command through the existing brief/handoff instruction.
Context refresh reads the full decision history, preserves unresolved decisions if source evidence disappears, and labels stale/missing pointers.
The saved brief, policy, decisions and recap survive worktree return and metadata retirement.
Context contains pointers and summaries, not complete transcripts or credentials.
Archived task IDs remain reserved while task context/browser custody exists; choose a new task ID for new work.

## Tested Windows tool boundary

| Surface | Executable evidence and limits |
| --- | --- |
| Shell/process operations | PowerShell paths with spaces, Go subprocess arguments, Windows creation-time identity, detached observer death/restart and generation-bound stop are exercised locally |
| Chrome AXI 0.1.34 / MCP 1.9.0 | Navigation, snapshot, JavaScript interaction, screenshot paths with spaces and named-session stop are exercised in an isolated owned profile |
| Chrome AXI 0.1.34 / MCP 1.6.0 | Incompatible `pageId` tool schema; file existence or `--version` does not establish readiness |
| Browser persistence | Profile location survives restart, but immediate AXI stop reproduced loss of a new localStorage write; this is unresolved |
| AXI `wait <ms>` | Failed against this installed pair; not part of the supported action set verified here |
| Lavish | Real portable export inlines a relative image with spaces; the saved HTML works after the source image is removed |
| Web research | Primary-source browser/search retrieval was used during the investigation; this does not prove every CLI harness has a web-search connector configured |
| GitHub tools | Use installed gh-axi where supported, with gh for structured evidence needed by existing CFO interfaces |

The Windows MCP entry point is `%APPDATA%/npm/node_modules/chrome-devtools-mcp/build/src/bin/chrome-devtools-mcp.js`.
It is not the POSIX `prefix/lib/node_modules` layout or MCP's non-executable `index.js` module.
Task launch selects the explicit installed script, avoiding `npx latest` resolution.
Fresh bootstrap includes both `chrome-devtools-axi@0.1.34` and `chrome-devtools-mcp@1.9.0`.
Doctor probes the exact configured MCP script and reports an absent, non-executable or untested backend as unavailable; it labels a passing version probe as an executable prerequisite, not browser action proof.
`CFO_CHROME_MCP_PATH` is an operator override; any alternate pair needs an action smoke before use.
Task session names are stable hashes of the runtime state root and task ID, within AXI's 64-character limit.
A named session isolates bridge/port state; an explicit task-owned profile supplies separate browser storage.
Auto-connect and externally supplied browser endpoints are cleared on launch.
Headless visibility, session existence, executable version, successful navigation and persistent storage are different assertions.

AXI 0.1.34 `stop` waits for the bridge PID, not the full Chrome/MCP shutdown chain.
Its process-signal path cannot be treated as a graceful Windows storage-flush contract.
MCP 1.9.0 closes its browser on stdin EOF, but AXI exposes no supported graceful browser-close command in the inspected interface.
Verify all profile-bound Chrome/MCP process exits after stop, with a deadline, before reuse or cleanup.
That exit proof still does not guarantee recent localStorage was flushed; a fixed sleep is not a durability fix.
Keep valuable work in exported task artifacts and preserve the immediate-stop reproduction for an upstream source fix.
Do not patch installed generated JavaScript or delete unrelated profiles to hide this limitation.

### Harness configuration and context access

| Harness | Task context handoff | Project MCP configuration | Wake proof |
| --- | --- | --- | --- |
| Claude | Brief/handoff plus saved context; same-harness resume supported | Adapter consumes the filtered supplied MCPConfig | Registered idle acceptance; synthetic adapter tests |
| Codex | Brief/handoff plus saved context; resume supported | Uses operator configuration; supplied MCPConfig is not consumed by this adapter | Registered idle acceptance and visible busy queue submission |
| Pi | Brief/handoff plus saved context; cold handoff where resume is unavailable | No uniform supplied MCPConfig guarantee | Registered idle acceptance; synthetic adapter tests |
| Kimi | Brief/handoff plus saved context; same-harness resume supported | No uniform supplied MCPConfig guarantee | Registered idle acceptance; synthetic adapter tests |

Native tool availability is not inferred from a harness version probe.
The adapters do not copy operator credentials into context or promise OAuth-only connectors to workers.

## Delivery and retention

Every finished branch requires a saved Lavish recap describing changes, reasons, tested behavior, UI evidence where relevant, limitations, branch/PR link and separate ready-for-merge, merged and deployed states.
`cfo recap <id> --file <html>` invokes Lavish's portable export into the task's durable directory, retaining relative assets in the single HTML file.
`cfo notify <id> --done --pr <url>` refuses a missing or empty saved recap.
Browser feedback is optional; honor user-ended pages and never stop the shared Lavish server for task cleanup.

```powershell
cfo retention <id> --dry-run
cfo retention <id> --apply
```

Retention reports paths, bytes, periods and holds before deletion.
Only owned browser profiles older than 30 days and raw browser evidence older than 90 days are eligible.
The clock uses the latest file/directory activity and retirement time.
Context, saved brief/policy, unresolved decisions, gate budget, test summary, retirement proof and portable recap are retained.
Active workers, unreadable process evidence, active native gates, unresolved decisions, dirty/unmerged work, linked directories and ownership mismatches hold cleanup.
There is no force override or arbitrary target-path deletion.
The normal cleanup path proves merge by ancestry or exact content equality to origin's recorded default branch, snapshots context, returns the worktree and persists a retirement receipt before removing live metadata.
For explicit local-only tasks it verifies the primary's checked-out local main/master branch instead, without requiring or inventing an origin remote.
Before archiving task scratch, it removes both generated `auth.ps1` and `mcp.json`, since MCP headers and stdio environment fields may contain credentials.
Later retention uses that receipt plus fresh worker/browser process checks; it does not require the deleted worktree.
An archived task without this proof stays held for an explicit migration decision.
Worktrees with unpublished or unmerged work remain preserved even if their processes stopped.

Public source and release contents exclude operational task reports, client deliverables, screenshots, credentials and browser profiles.
Previously tracked operational artifacts were removed only after verified private owner copies existed.
Current-tree removal does not erase historical exposure; do not rewrite public history or rotate credentials silently.
The legacy TEMP profile cleanup requested during this incident was blocked by automatic execution-policy review and was not performed.
These generic retention commands do not retry deletion of those rejected targets.

## Research and architecture choices

[First Mate's architecture](https://github.com/kunchenguid/firstmate/blob/main/docs/architecture.md) and [watcher continuity](https://github.com/kunchenguid/firstmate/blob/main/docs/watcher-continuity.md) support durable actionable wakes and continuity beyond a conversational turn.
This repair reuses CFO's existing queue and structural monitor instead of adding another orchestrator.
[Agent Orchestrator](https://github.com/Untrivial-ai/agent-orchestrator) separates lifecycle observation from an agent's conversation; CFO now gives its watcher an explicit process lifecycle and delivery health.
[Gas Town's daemon](https://github.com/gastownhall/gastown/blob/main/internal/daemon/daemon.go) illustrates persistent supervision, but adopting its broader coordination framework would exceed this repair.
[Native no-mistakes](https://github.com/kunchenguid/no-mistakes) already owns fetch, effective config, review, repair, tests and PR creation.
Its v1.75.2/v1.75.3 prereleases did not add the speculative trusted-launch assertion that had made CFO refuse every gate before reviewer launch.
The corrected [pipeline boundary](pipeline.md) uses the supported strict native run identity and a task-wide budget, without importing the preserved global bridge wrapper.
