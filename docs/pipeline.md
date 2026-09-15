# Pipeline policy

`config/pipeline.json` is the checked-in CFO policy for no-mistakes v1.75.1.
The CFO remains the driver; no pipeline steps are reimplemented here.
The binary still owns review, fixes, tests, lint, documentation, push, PR creation and CI.

## Task classes

| Spawn class | Review repair cycles | Reviewer |
| --- | --- | --- |
| `ordinary` (default) | 2 | Codex gpt-5.6-sol high |
| `high-risk` | 3 | Codex gpt-5.6-sol high |
| `mechanical` | 2 | Codex gpt-5.6-sol high |

Use `cfo spawn <id> ... --class high-risk` for a high-risk task.
No-mistakes tasks receive a policy snapshot at `state/tasktmp/<id>/pipeline.json`, with its class and SHA-256 recorded in task metadata.
Editing the source policy does not change a running task's snapshot, and `cfo switch` retains it.
Direct-PR and local-only tasks retain their existing delivery paths.
Existing tasks without a snapshot are not silently migrated.

Policy v2 uses the native Codex CLI through the machine's existing ChatGPT OAuth login.
The global primary profile defaults to Codex gpt-5.6-sol at high effort.
Global-only `review_agents.reviewer` and `review_agents.fixer` pin every review and review-fix invocation to that same profile without fallbacks.
No-mistakes v1.75.1 resolves test, document, and lint from the primary agent.
Its effective repository `agent` normally comes from the trusted default branch, but a trusted `allow_repo_commands` setting delegates that field to the submitted branch.
Before a CFO-managed run starts, the driver therefore requires the effective field to be absent, which inherits global Codex, or to select only Codex explicitly.
The driver does not rewrite repository configuration, and non-CFO no-mistakes use retains its native repository policy.

A cycle means one repair followed by another review, after the initial review.
The driver counts completed rounds from the native database, so the budget is durable within a native run and restarting the CLI mid-run does not reset it.
A new run started on the same branch after a terminal failed or cancelled run begins a fresh budget, because it is validating new code.
After two or three repair cycles, actionable review findings remain unresolved.
A clean last review can still pass.
The driver never turns budget exhaustion into approval, skips a step, or uses `--yes`.

## Explicit machine configuration

```powershell
cfo pipeline config-drift
cfo pipeline config-apply
cfo pipeline migrate <id>
```

`config-drift` is read-only and prints owned field names, never their values or unrelated configuration.
`config-apply` requires a scheduled idle window: the daemon must already be stopped and every durable run must be terminal.
It takes the native daemon singleton lock throughout the database check, backup and replacement, preventing a concurrent daemon startup.
It does not stop or restart anything, cancel a run, or repair stale state.
The command currently supports Windows, using the native singleton lock contract retained by v1.75.1.
Use `NM_HOME` to select the same native home as no-mistakes, otherwise both use `~/.no-mistakes`.

Before replacement, the original YAML is backed up beside the configuration with a unique timestamped name.
The backup and staged replacement receive the credential store's owner-only file protection because unrelated settings may contain credentials.
Replacement itself is atomic and preserves the live configuration's existing permissions, so an interrupted apply cannot leave a truncated shared file and every principal that could read it before still can.
The command prints the backup path, preserves unrelated YAML settings and comments, and refuses duplicate keys, aliases, anchors and merges rather than making an ambiguous edit.
It refuses a missing or unreadable database or configuration file.
An operator can restore the printed backup in another idle window; restoration is never automatic over an operator's intervening edit.

Owned machine fields are `agent: [codex]`, `agent_config.codex: {model: gpt-5.6-sol, effort: high}`, both global `review_agents` roles with the same Codex profile, `eval.capture_provenance: true`, `agent_args_override.codex: [--cfo-native-gate, -c, 'service_tier="default"']`, `agent_path_override.codex: cfo`, `auto_fix.review: 0`, and one automatic follow-up each for test, lint, rebase and CI.
The raw Codex argument override forces standard service for every managed role, so a user-level fast or priority preference cannot leak into a gate, while `agent_config` owns model and reasoning effort.
The Codex executable override sends native invocations through the CFO pre-agent guard, which recognizes its private marker only immediately after `exec` or `exec resume` and delegates to the real `codex` command from `PATH` only after a managed run's durable bindings still match.
Before opening SQLite or querying native state, the guard requires a native worktree Git identity and a matching task-local launch contract, then uses the absolute sqlite executable captured in that contract.
An unmatched invocation passes through unchanged without consulting stale machine-global runtime state, while a matched incomplete, revoked, or mismatched managed identity fails closed.
CFO validation generations use the versioned `cfo-v1-` prefix accepted by no-mistakes v1.75.1.
Strict native runs whose validation generation lacks that prefix, and native Codex operations outside a run, pass through unchanged, while a prefixed run without an exact CFO contract fails closed.
Executable overrides for other harnesses remain operator-owned.
The exact legacy CFO-owned Claude model and effort vector is removed during apply; a differing operator-owned Claude vector is preserved.
Document follow-ups and other native settings retain their existing values.
Spawn never rewrites shared YAML.
The YAML parser dependency is needed to preserve unrelated configuration structurally; v3.0.1 avoids the old parser's [known panic vulnerability](https://pkg.go.dev/vuln/GO-2022-0603).

Run `config-apply` before migrating any live task snapshot.
`migrate` requires the applied v2 global configuration, the daemon stopped, every durable native run terminal, and the task's pipeline lock.
It accepts only the reviewed v1-to-v2 transition and preserves the task class and `review_cycles` cap exactly.
Before replacing either owned field, it writes a task-local transaction journal containing the validated old and new snapshots and the expected audit event.
An interrupted command resumes that journal under the same pipeline, cleanup, metadata, and native idle locks before any pipeline command trusts the snapshot hash.
The task-scoped metadata lock also serializes `cfo pr check` and `cfo switch`, so neither command can publish a stale whole-record update over the migrated hash.
Migration removes its journal only after the snapshot replacement, metadata hash update, and audit append have all completed while that lock remains held.
Recovery finishes the migration once or rolls both owned fields back when audit persistence fails, without overwriting unrelated task metadata.
An append reported as successful commits the audit without a second read, while an errored append whose persisted state cannot be reread leaves the forward journal intact for retry.
The task status receives one line containing the class, cap, old hash and new hash.
Migration never changes a worktree, native run row, gate ref, origin ref, or review result.

## Driving a task

```powershell
cfo pipeline run <id> --intent "The user's complete objective and constraints"
cfo pipeline respond <id> --action fix --findings finding-id --instructions "Concrete guidance"
cfo pipeline respond <id> --action approve
cfo pipeline recover <id>
cfo pipeline migrate <id>
```

Commands operate in the recorded task worktree and use its frozen policy.
The pane exports `CFO_HOME` and `CFO_STATE_OVERRIDE` and every gate step inherits both, so the `--intent` text must require any step that runs `cfo`, or a shell that resolves it, to clear them or point them at a temporary directory.
`internal/home` refuses the inherited fleet home from a test binary, which covers `go test`, but the real `cfo` binary is not a test binary and no-mistakes has no per-repo step environment setting, so the intent is the only place left to state it.
Commit work on a named feature branch before `run`.
The project must be initialized for no-mistakes, with readable committed task and origin default-branch `.no-mistakes.yaml` files.
Refresh origin before starting; global reviewer/fixer drift and repository automatic-fix overrides that conflict with policy are refused.
Immediately before invoking native start, `run` captures the complete mutable launch evidence twice and requires the snapshots to match.
The evidence binds task metadata, frozen policy, worktree identity, named branch, clean submitted head, current origin default branch, local tracking ref, submitted and trusted repository configuration digests, shared configuration digest, and effective Codex primary.
It then uses the native strict launch nonce and validation generation and requires the returned receipt to bind the exact branch, submitted head, generation, and intent digest.
The shared configuration routes native Codex through the guarded `cfo exec --cfo-native-gate` and `cfo exec resume --cfo-native-gate` invocation shapes.
The task-local launch contract starts as pending and can authorize only the first agent while the native run has no completed invocation.
At the actual agent boundary, that guard finds the active run by its native worktree, loads the matching task-local launch contract, and requires the durable run head and current native worktree head to agree.
The initial head must equal the submitted head, while a later head requires durable native fixer or completed rebase transition evidence before the guard rechecks the shared configuration, origin default branch, tracking ref, both repository configuration blobs, and Codex primary.
The active run must record no-mistakes v1.75.1 build `37ed232`, and its complete identity is read again after the file, Git, and remote checks.
After those checks, it re-reads both the complete active run identity and native worktree HEAD immediately before delegating and refuses any change to the authorized pair.
After native returns, the driver requires the durable run identity, no-mistakes version, and build to match the receipt.
When no invocation exists because native parked at a pre-agent gate, the driver atomically binds the pending contract to that exact run ID and permits only that run's guarded response.
After the first managed agent returns, the driver requires the trusted configuration SHA, recorded Codex gpt-5.6-sol model, and SHA-256 of the persisted review global configuration before atomically promoting the contract to verified.
A failed proof atomically revokes the pending contract before best-effort cleanup, so a Windows removal failure cannot preserve authorization.
Receipt-only validation and after-the-fact cancellation are not treated as proof that the pre-agent boundary was safe.
A repository's `agent` field continues to select only its native primary path and cannot replace the global reviewer or fixer profiles.
An earlier unresolved run cannot be restarted to reset its budget.
Use native read-only `axi status` and `axi logs` to inspect progress; the engine's guarded `axi sync` remains the branch synchronization interface after validation.

`respond` requires durable evidence of one parked gate in this project's current branch and a verified task contract bound to that run.
It refuses a gate already answered or still running and checks that the active review's persisted automatic limit is disabled or zero.
An explicitly selected finding with an empty action is actionable only when the active gate is not review and its persisted automatic-fix limit is positive.
Review remains fail-closed for empty or unknown actions.
Fix responses name concrete actionable finding IDs; selecting an `ask-user` finding is an explicit decision, never automatic consent.
Approval is accepted only when every finding is explicitly `no-op`, or the findings list is empty.
Unsupported or malformed evidence fails closed.
The command returns exit 3 for unresolved work requiring a CFO decision, without advancing the gate.
Other refusals return exit 1.
This is a cooperative driver guard, not an operating-system sandbox around direct native commands.
Agents must use the driver for managed tasks and must not invoke native mutation commands to bypass its budgets.

`recover` returns custody after a failed or cancelled unpublished run when native recovery is blocked by a stale recorded head.
For pipeline-owned recovery, it requires a clean worktree whose head exactly matches both the submitted head and native gate branch.
The stale recorded commit must be an ancestor of that preserved head.
Before a compare-and-swap repairs the stale database field, the old commit is anchored under `refs/no-mistakes/recovery/<run>/recorded`.
The driver then invokes native `sync --recover --keep-local` and verifies that custody was durably returned without moving the local or gate head.
When native already reports clean user-owned custody, the registered task worktree and branch may contain rebases and follow-up fixes made after custody returned.
Before a CFO-managed alignment, the driver stores the stale gate head under the immutable `refs/no-mistakes/recovery/<run>/gate` ref and verifies it again before moving the gate or database heads.
That ref is durable CFO-swap evidence, so every `custody_returned` retry that finds it requires the stale commit and the native recovery anchor before accepting an aligned state.
With no CFO-swap evidence, a native `custody_returned` state is accepted only when its native anchor equals the fully aligned local, gate, recorded, and submitted head.
The driver imports the local commit into the internal repository without writing a fetch ref, and compare-and-swaps the internal gate ref and both database heads to the local head.
If the process exits after the gate compare-and-swap, the stale gate anchor lets the next recovery recognize that exact half-state and finish the database compare-and-swap.
After a reported database compare-and-swap failure, the driver reads the exact run row before deciding whether the advanced gate can roll back.
A confirmed unchanged row permits rollback, a committed row continues forward, and an uncertain or conflicting row preserves the advanced gate for a safe retry.
Native status must confirm all three heads before success is reported.
If a later status check triggers rollback, the gate moves back only after the database rollback succeeds, so a transient database failure preserves the forward-aligned retry state.
The local branch and origin refs are never moved.
The command never resets a branch or recovers a pushed run.
Pipeline-owned recorded-head repair requires strict ancestry.
After native has already returned clean user-owned custody, the registered identity, unpublished-run state, stale-head anchor, and compare-and-swap guards are the authorized boundary, so the driver does not reject legitimate rebases or follow-up commits by comparing them with the stale head.

This repository's committed automatic-fix overrides remain authoritative for a new submitted branch.
After the shared idle apply, migrate each idle legacy task explicitly before starting its next run.
Newly spawned tasks freeze policy v2 directly.

## Reading speed evidence

`cfo doctor` separates validation invocation timing by harness, recorded model, role, step and outcome.
Successful calls, errors and cancellations never share a timing row.
Error and cancellation durations measure failure latency, not successful speed.
Spawn hints show outcome counts and label implementation unmeasured, because the native database observes validation agents rather than the implementation session.
Missing or unreadable telemetry stays an explicit unavailable result.
No implementation-speed comparison can be inferred from these gate timings.
