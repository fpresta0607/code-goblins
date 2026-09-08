# Pipeline policy

`config/pipeline.json` is the checked-in CFO policy for the existing no-mistakes binary.
The CFO remains the driver; no pipeline steps are reimplemented here.
The binary still owns review, fixes, tests, lint, documentation, push, PR creation and CI.

## Task classes

| Spawn class | Review repair cycles | Reviewer |
| --- | --- | --- |
| `ordinary` (default) | 2 | Claude Opus high |
| `high-risk` | 3 | Claude Opus high |
| `mechanical` | 2 | Claude Opus high |

Use `cfo spawn <id> ... --class high-risk` for a high-risk task.
No-mistakes tasks receive a policy snapshot at `state/tasktmp/<id>/pipeline.json`, with its class and SHA-256 recorded in task metadata.
Editing the source policy does not change a running task's snapshot, and `cfo switch` retains it.
Direct-PR and local-only tasks retain their existing delivery paths.
Existing tasks without a snapshot are not silently migrated.

A cycle means one repair followed by another review, after the initial review.
The driver counts completed rounds from the native database, so restarting the CLI does not reset the budget.
After two or three repair cycles, actionable review findings remain unresolved.
A clean last review can still pass.
The driver never turns budget exhaustion into approval, skips a step, or uses `--yes`.

## Explicit machine configuration

```powershell
cfo pipeline config-drift
cfo pipeline config-apply
```

`config-drift` is read-only and prints owned field names, never their values or unrelated configuration.
`config-apply` requires a scheduled idle window: the daemon must already be stopped and every durable run must be terminal.
It takes the native daemon singleton lock throughout the database check, backup and replacement, preventing a concurrent daemon startup.
It does not stop or restart anything, cancel a run, or repair stale state.
The command currently supports Windows, with the v1.48/v1.64 singleton lock contract.
Use `NM_HOME` to select the same native home as no-mistakes, otherwise both use `~/.no-mistakes`.

Before replacement, the original YAML is backed up beside the configuration with a unique timestamped name.
The backup and staged replacement receive the credential store's owner-only file protection because unrelated settings may contain credentials.
The command prints the backup path, preserves unrelated YAML settings and comments, and refuses duplicate keys, aliases, anchors and merges rather than making an ambiguous edit.
It refuses a missing or unreadable database or configuration file.
An operator can restore the printed backup in another idle window; restoration is never automatic over an operator's intervening edit.

Owned machine fields are `agent: [claude]`, `agent_args_override.claude: [--model, opus, --effort, high]`, `auto_fix.review: 0`, and one automatic follow-up each for test, lint, rebase and CI.
The complete Claude argument override is owned; inspect local custom Claude arguments before scheduling apply.
Document follow-ups and other native settings retain their existing values.
No-mistakes v1.48 has no per-run config/model flags, so spawn never rewrites shared YAML.
The YAML parser dependency is needed to preserve unrelated configuration structurally; v3.0.1 avoids the old parser's [known panic vulnerability](https://pkg.go.dev/vuln/GO-2022-0603).

## Driving a task

```powershell
cfo pipeline run <id> --intent "The user's complete objective and constraints"
cfo pipeline respond <id> --action fix --findings finding-id --instructions "Concrete guidance"
cfo pipeline respond <id> --action approve
```

Commands operate in the recorded task worktree and use its frozen policy.
Commit work on a named feature branch before `run`.
The project must be initialized for no-mistakes, with readable committed task and origin default-branch `.no-mistakes.yaml` files.
Refresh origin before starting; reviewer fallbacks and repository automatic-fix overrides that conflict with policy are refused.
An earlier unresolved run cannot be restarted to reset its budget.
Use native read-only `axi status` and `axi logs` to inspect progress; the engine's guarded `axi sync` remains the branch synchronization interface after validation.

`respond` requires durable evidence of one parked gate in this project's current branch.
It refuses a gate already answered or still running and checks the active review's actual automatic limit is zero.
Fix responses name concrete actionable finding IDs; selecting an `ask-user` finding is an explicit decision, never automatic consent.
Approval is accepted only when every finding is explicitly `no-op`, or the findings list is empty.
Unsupported or malformed evidence fails closed.
The command returns exit 3 for unresolved work requiring a CFO decision, without advancing the gate.
Other refusals return exit 1.
This is a cooperative driver guard, not an operating-system sandbox around direct native commands.
Agents must use the driver for managed tasks and must not invoke native mutation commands to bypass its budgets.

The initial policy PR also commits this repository's automatic-fix overrides, which native no-mistakes honors from a new submitted branch.
That permits this PR's own legacy task to use the approved limits without rewriting the shared configuration or migrating a running task.
The driver becomes the normal entry point for newly spawned tasks after the shared idle apply.

## Reading speed evidence

`cfo doctor` separates validation invocation timing by harness, recorded model, role, step and outcome.
Successful calls, errors and cancellations never share a timing row.
Error and cancellation durations measure failure latency, not successful speed.
Spawn hints show outcome counts and label implementation unmeasured, because the native database observes validation agents rather than the implementation session.
Missing or unreadable telemetry stays an explicit unavailable result.
No implementation-speed comparison can be inferred from these gate timings.
