# Herdr operational compatibility design

## Status

This design completes the Windows-native Plan 3 runtime by correcting CFO's Herdr command contract, wiring structural monitoring into the watcher, and adding a guarded cleanup command.
The selected approach uses the installed Herdr CLI and its existing typed socket API surface.
It does not require a Herdr fork, a custom Herdr build, or terminal-text scraping.

## Verified Herdr contract

The installed `herdr 0.8.0-preview.2026-08-04-d78e3d3b5126` client reports protocol 19 and schema version 1.
Installed operational commands such as `workspace list` and `agent list` emit typed JSON response envelopes by default, including structured JSON error envelopes when the server is unavailable.
Those operational commands reject an extra `--json` option because their output is already machine-readable.
The installed help advertises `--json` only for commands that have a distinct human-readable mode, including `status`, `api schema`, and `session list`.
The upstream Herdr CLI routes operational responses through its typed `print_response` path, and the upstream automation documentation tells clients to consume the returned identifiers from those JSON envelopes.
The upstream `session.snapshot` API and `herdr api snapshot` command return version and protocol metadata together with workspace, tab, pane, layout, and agent records in one response.

## Decision

CFO will adapt to Herdr's actual command contract instead of changing Herdr.
CFO will retain explicit `--json` only for `status`, `api schema`, and `session list`, because those commands advertise that option.
CFO will remove `--json` from operational workspace, tab, pane, and agent commands, because those commands already return typed JSON and reject the redundant option.
Every Herdr request will continue to carry an explicit `--session` value.
CFO will keep strict typed envelope decoding for operational responses and will continue accepting a structured error envelope from stderr when Herdr exits nonzero.
Terminal capture remains the one deliberate text response because `pane read` is documented to return terminal text rather than an operational JSON object.

## Compatibility preflight

CFO will run a local schema preflight before relying on Herdr operations.
The preflight will parse `herdr api schema --json --session <session>` and require the supported schema version, protocol, response envelopes, `session.snapshot`, and every workspace, tab, pane, and agent method CFO uses.
After the server is running, CFO will parse `herdr status --json --session <session>` and require compatible client and server protocols rather than inferring compatibility from the executable version string.
CFO will parse `herdr session list --json --session <session>` and require the selected session to be addressable.
The preflight will fail before creating a workspace, tab, pane, agent, or worktree when any required method, protocol fact, session fact, or response shape is absent or ambiguous.
A long-running watcher will cache a successful immutable schema check for its process lifetime while still obtaining fresh runtime evidence on every monitoring cycle.
Diagnostics will name the unsupported protocol, missing method, or malformed response and will recommend a compatible Herdr installation without silently selecting another backend.

## Operational client

The existing `internal/herdr` client remains the only Herdr subprocess boundary.
Its status, schema, and session-discovery methods will use their advertised `--json` option.
Its workspace, tab, pane, agent, create, close, run, send, and get methods will consume the JSON they receive by default without adding `--json`.
Its pane-read method will continue requesting bounded plain terminal text.
All success paths will require the expected typed result fields, and all normal missing-resource paths will require a recognized structured Herdr error code.
Unknown fields may be ignored for forward compatibility, but missing required identity or state fields will remain errors.

## Structural monitor prober

The production watcher will receive a read-only Herdr monitor prober through `watch.ConfigFromEnv` so both manual `cfo watch` and the production hook path use the same construction.
The watcher will resolve the Herdr session from the same source used by spawn, defaulting to `default`, so monitoring cannot drift to a different implicit session.
The prober will call `herdr api snapshot --session <session>` once per monitoring cycle and build in-memory indexes for workspaces, tabs, panes, and agents.
For each task, the prober will require the metadata session, workspace ID, tab ID, pane ID, `gb-<id>` tab label, and agent association to agree with that one snapshot.
A missing pane will produce a typed missing verdict, while malformed, duplicated, cross-linked, or otherwise inconsistent identity will produce an unknown verdict.
Recognized `working` status will map to busy, recognized `idle`, `done`, or `blocked` status will map to idle, and any other status will map to unknown.
After structural validation, the prober will request at most one bounded `pane read --source recent-unwrapped --lines 200` capture for each present task because the session snapshot does not include terminal contents.
Independent pane reads may run with bounded concurrency, but their results will be keyed by task identity so scheduling cannot affect classification order or persisted output.
The batch snapshot replaces repeated workspace, tab, pane, and agent list or get subprocesses for every task, which reduces Windows process startup overhead and prevents topology reads from disagreeing within one cycle.
The prober will expose no send, close, restart, return, or delete capability, and the monitor service will retain its existing inspection-only dependency boundary.
If the compatibility preflight, snapshot, identity proof, activity state, or capture is unreadable, the monitor will persist an unknown observation and a durable wake rather than inventing liveness or progress.

## Guarded cleanup

> **Superseded — cleanup closes the task tab.** Cleanup now closes the task's recorded Herdr tab after proving the endpoint agent-free and before returning the worktree, so a completed task leaves no terminal behind. This replaces the "never close a Herdr pane or tab" and "only successful lifecycle call is `treehouse.Service.Return`" statements below and the cleanup summary in Command flow. The authoritative owner is the `internal/cleanup` package doc comment.

> **Superseded - treehouse removed.** `treehouse.Validate` and `treehouse.Service.Return` below are now `worktree.Validate` and `worktree.Service.Return` in `internal/worktree`: the in-repo worktree at `<project>/.worktrees/gb-<id>` is removed and its git entry pruned, with no external allocator. The authoritative owners are the `internal/cleanup` and `internal/worktree` doc comments.

The new command will be `cfo cleanup <id>` and will accept only a local task ID, not a raw path or explicit Herdr target.
Cleanup will resolve a primary CFO home, validate the task metadata, and hold the task lifecycle lock across all checks and the return operation.
Cleanup will require a Herdr task with complete session, workspace, tab, pane, project, and worktree metadata.
Cleanup will canonicalize the project and worktree paths and call `treehouse.Validate` to prove the worktree is readable, is its own Git root, and is not the primary checkout.
Cleanup will inspect `git status --porcelain=v1 --untracked-files=all` and refuse any tracked, untracked, staged, or unstaged change.
Cleanup will use a fresh structural Herdr snapshot immediately before return and will refuse when an agent is present in any state.
A missing recorded pane or an exact recorded pane with no registered agent is sufficient inactive evidence, while mismatched identity, unreadable state, duplicate identity, or a failed Herdr request is unproven and therefore refused.
After those checks, cleanup will delegate worktree release only to `treehouse.Service.Return`, which remains the single owner of `treehouse return --force` invocation and retry behavior.
Cleanup will never invoke `git worktree remove`, delete a directory, close a Herdr pane or tab, stop an agent, discard changes, or expose a force override.
Cleanup will preserve task metadata when return fails so the operator can diagnose and retry the exact task safely.
Successful cleanup will not broaden its authority to paths or state outside the one validated task.

## Command flow

Spawn will validate the local schema, start or confirm the selected Herdr server, validate live protocol and session compatibility, and only then create the flat CFO workspace and task tab.
Send and peek will resolve the recorded target, use the corrected operational command forms, and retain their existing delivery and output confirmation rules.
Each watch cycle will load task metadata, obtain one structural session snapshot, collect bounded captures for structurally valid tasks, classify every result, and publish at most the existing durable actionable event.
Cleanup will lock the task, validate metadata and isolation, prove a clean worktree, prove an inactive endpoint, call the Treehouse return service, and report the result without performing independent lifecycle actions.

## Error handling and safety

Command-line syntax errors, malformed JSON, missing required fields, protocol mismatch, missing capabilities, inconsistent identities, and unavailable servers are distinct failures and will retain operation and session context in their diagnostics.
No compatibility failure may trigger text parsing, a different backend, a Herdr fork, direct filesystem deletion, or a destructive cleanup fallback.
Monitoring remains read-only even when it classifies a task as stale, unknown, missing, or in need of deep inspection.
Cleanup remains an explicit operator action and cannot be triggered by monitoring or heartbeat processing.
The existing 200-line capture floor, durable observation records, heartbeat records, and wake publication rules remain unchanged.

## Performance rationale

Correcting the CLI arguments is the smallest and fastest route to an operational system because Herdr already exposes the required typed API.
One session snapshot per cycle replaces several subprocesses per task and gives the monitor a coherent topology and activity view.
Bounded concurrent terminal reads reduce wall-clock time without allowing an unbounded process burst.
Schema compatibility is stable within one watcher process and can be cached, while topology, activity, and capture evidence remain fresh.
Calling the CLI instead of implementing the raw socket protocol keeps one tested integration boundary and avoids duplicating Herdr transport, framing, and Windows socket behavior before measurements justify that complexity.

## Acceptance and verification

Unit tests will pin the exact argument split by requiring `--json` for status, schema, and session discovery and forbidding it for operational commands.
Herdr client tests will cover successful default JSON envelopes, structured stderr error envelopes, malformed envelopes, missing fields, explicit sessions, protocol mismatch, and missing required methods.
Monitor tests will prove that one snapshot supplies structural evidence for all tasks in a cycle and that each valid task receives no more than one bounded capture read.
Monitor tests will retain busy protection, first stale wake, repeat stale escalation, deep-inspection threshold, missing endpoint, unknown identity, heartbeat persistence, restart recovery, and absence of lifecycle commands.
Watcher and hook tests will prove that production configuration installs the structural prober and that an unreadable Herdr session becomes durable unknown evidence rather than a nil-prober gap.
Cleanup tests will prove refusal for the primary checkout, a nested or non-isolated path, dirty tracked files, dirty untracked files, an active agent, mismatched endpoint identity, unreadable Herdr evidence, and an ambiguous Treehouse return.
Cleanup tests will prove that the only successful lifecycle call is `treehouse.Service.Return` for the exact canonical project and worktree from the task metadata.
The opt-in Windows acceptance will use one dedicated Herdr session and disposable repository to exercise Claude, Codex, and Pi spawn, send, peek, fleet views, busy protection, stale escalation, unknown endpoint handling, restart persistence, durable wakes, guarded cleanup, and primary-checkout cleanliness.
The acceptance run will first prove cleanup refusal while a worker is active, then stop the worker through the normal agent interaction, wait for structural inactive evidence, and return the worktree only through `cfo cleanup <id>`.
Ordinary Windows CI will continue using deterministic fake binaries and will not require credentials or installed harnesses.

## Alternatives rejected

Forking Herdr is rejected because both the installed build and upstream already expose the typed operational responses CFO needs, so a fork would add release and maintenance work without adding capability.
Adding a new `herdr api v1` facade is rejected because the existing protocol-19 schema, operational commands, and session snapshot already provide the required versioned machine contract.
Adding `--json` to every upstream command is rejected because operational commands are already JSON-only and treat that option as invalid syntax.
Scraping human-readable help, tables, or terminal output is rejected because it would discard typed errors and make identity and cleanup decisions depend on unstable presentation text.
Issuing workspace, tab, pane, and agent list or get commands separately for every task is rejected because it multiplies Windows subprocess overhead and can combine observations from different topology instants.
Implementing a direct Herdr socket client is deferred because the CLI snapshot removes the current performance bottleneck with much less transport and compatibility code.
Adding a cleanup force or discard option is rejected because uncommitted work and unproven worker state are boundaries that must require an explicit separate recovery decision rather than a convenient flag.

## Completion criteria

This continuation is complete when the corrected client passes deterministic tests, `watch.ConfigFromEnv` supplies the structural prober to both watch entry paths, `cfo cleanup <id>` returns only clean and proven-inactive isolated worktrees through Treehouse, and the opt-in Windows acceptance passes against installed Herdr for Claude, Codex, and Pi.
