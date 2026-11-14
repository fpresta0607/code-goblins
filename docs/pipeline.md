# Pipeline policy

`config/pipeline.json` is the checked-in CEO policy for no-mistakes v1.75.1.
The [delivery contract](contract/delivery.md#delivery) assigns validation ownership; no pipeline steps are reimplemented here.
The binary still owns review, fixes, tests, lint, documentation, push, PR creation and CI.

## Task classes

| Spawn class | Review repair cycles | Gate agents under v6 and v7 | Gate agents under v5 |
| --- | --- | --- | --- |
| `ordinary` (default) | 2 | The task's own harness | Codex gpt-6.1-sol xhigh, then Claude |
| `high-risk` | 3 | The task's own harness | Codex gpt-6.1-sol xhigh, then Claude |
| `mechanical` | 2 | The task's own harness | Codex gpt-6.1-sol xhigh, then Claude |

Use `goblins spawn <id> ... --class high-risk` for a high-risk task.
A task freezes the version its home's `config/pipeline.json` holds when it is spawned.
The checked-in file is v7, which an install writes only into a home that has none, so a home that holds an earlier version keeps it until its operator writes v7 there, as [A gate that only checks](#a-gate-that-only-checks) describes.
No-mistakes tasks receive a policy snapshot at `state/tasktmp/<id>/pipeline.json`, with its class and SHA-256 recorded in task metadata.
Editing the source policy does not change a running task's snapshot, and `goblins switch` retains it.
Direct-PR and local-only tasks retain their existing delivery paths.
They have no gate and no frozen pipeline policy, so every `goblins pipeline` command that names one refuses it by its mode and names that mode's own route, never a migration, since no migration can give a task a gate it was never spawned with (issue 1438).
A direct-PR task runs its selected project checks and its pull request's CI, and a local-only task lands with `goblins merge-local <id>`.
Delivery selection and frozen project checks follow [Delivery intake](contract/delivery.md#delivery); automatic issue intake follows the [root-issue contract](contract/root-issues.md#root-issues).
Existing no-mistakes tasks without a snapshot are not silently migrated.

Policy v4 names one ordered chain for every gate role: Codex gpt-6.1-sol at xhigh effort first, through the machine's existing ChatGPT OAuth login, and Claude next when Codex cannot run.
It renders the shared config as the operator set it on 2026-10-08, `agent: [codex, claude]`, with no review role pinned outside that chain.
Claude's model and effort stay the operator's, in `agent_config.claude`, which the policy does not own.
Frozen v1 Claude Opus/high, v2 Codex gpt-5.6-sol/high and v3 Codex gpt-6.1-sol/xhigh policies remain valid with their original hashes until explicitly migrated.
Versions 2 and 3 pin global `review_agents.reviewer` and `review_agents.fixer` to their Codex profile without fallbacks, and version 4 removes `review_agents` so review and review-fix follow the chain.
Policy v5 keeps v4's chain and starts every gate agent without the operator's MCP servers, which belong to the operator's own sessions.
A gate's Codex skips the user `config.toml` that declares them, along with the plugins and apps that bring servers of their own, and a gate's Claude loads only servers named with `--mcp-config`, of which there are none.
A `-c mcp_servers={}` override cannot do it, because Codex merges overrides into its tables and every server stays.
The user settings a gate's Codex keeps working under are passed as `-c` arguments instead: `personality`, `model_auto_compact_token_limit_scope`, `features.multi_agent` and `project_doc_max_bytes=65536`.
The gate passes model, reasoning effort, service tier, approvals and sandbox itself, and the rest of a user config serves the operator's own sessions: notices, the desktop app, plugins, marketplaces, hook trust and folder trust.
The `project_doc_max_bytes` pin keeps up to 64 KiB of `AGENTS.md` in the gate of a repository that does not disable project settings, where Codex's own default is 32 KiB.
It needs no-mistakes b05697a or later, which passes a repository's own `project_doc_max_bytes=0` after the pin when that repository disables project settings, and Codex applies the later value.
An earlier no-mistakes refuses to start any gate of such a repository when a raw argument pins the value, so v5 reaches a machine only together with that build.
No-mistakes v1.75.1 resolves test, document, and lint from the primary agent.
Its effective repository `agent` normally comes from the trusted default branch, but a trusted `allow_repo_commands` setting delegates that field to the submitted branch.
Before a CEO-managed run starts, the driver therefore requires the effective field to be absent, which inherits the global chain, or to select only Codex explicitly, which runs that repository's gates without the fallback.
The driver does not rewrite repository configuration, and non-CEO no-mistakes use retains its native repository policy.

## A gate on its task's own harness

Policy v6 names no harness, and neither does v7.
Every gate role of a task, which is review, review fix, test, document and lint, runs on the harness that task's goblin runs on, with the model and effort its record holds.
A Claude Code goblin's gate runs Claude Code, a Codex goblin's gate runs Codex, and a pi goblin's gate runs pi.
A task whose record names no model or no effort takes the machine config's `agent_config.<harness>` value, which is the operator's default for that harness.
A gate with neither is refused, and the refusal names both ways to name them: `goblins switch <id> --model <model> --effort <effort>`, or `agent_config.<harness>`.
The harness is read from the task's record when a run starts and is not frozen in the snapshot, so after `goblins switch` the next run follows the new harness.
A run already started keeps the agents it started with.

### How a run carries its own agents

One no-mistakes daemon serves every goblin, and v1.75.1 reads its agent chain from two places only: the machine's `config.yaml` and the repository's trusted default-branch `.no-mistakes.yaml`.
Neither differs between two tasks of one repository, and its one per-run pin, `--model` with `--effort`, serves pi alone.
So a run carries its own agents as a launch selection, which `goblins pipeline run` writes and no-mistakes proves.

1. The driver writes `state/tasktmp/<id>/launch-selection-<launch>.json`, a file of its own for each start: the trusted default-branch commit, `"apply": true`, and one ordered chain for the primary, reviewer and fixer roles.
2. It starts the run with `--launch-nonce`, `--validation-generation`, which is the task's frozen policy hash, and `--launch-assertion <file>`, and prints the chain on a `pipeline gate agent:` line.
3. The daemon replaces that run's agent chain, each named harness's model and effort, and every review role with the selection, for that run alone and in memory.
4. It then proves the selection as it proves any launch assertion: against a fresh fetch of the trusted commit and against the arguments each agent will be started with, before a run exists and before any agent starts.
5. The selection and its proof are stored with the run, where neither can change, and a daemon that restarts proves the stored selection again before it recovers the run.

Nothing edits the machine's `config.yaml` for a run, so two tasks on different harnesses gate at once under one daemon and one config.
A no-mistakes build that cannot apply a selection refuses the launch before it takes the branch: a build with no launch assertion rejects the flag, and a build that proves but cannot apply rejects the `apply` field.
The driver then says which build v6 and later need.
A repository's own `agent` field does not stop a start from v6, because the run's selection replaces it.

### A second harness only when named

With no fallback named, a gate agent that fails fails its step, and no other provider starts.
The operator names a fallback in the home's `config/pipeline.json`, with its harness, model and effort in full:

```json
{
  "version": 7,
  "fallback": {"harness": "codex", "model": "gpt-6.1-sol", "effort": "xhigh"},
  "auto_fix": {"review": 0, "test": 0, "lint": 0, "rebase": 1, "ci": 0},
  "classes": {
    "ordinary": {"review_cycles": 2},
    "high-risk": {"review_cycles": 3},
    "mechanical": {"review_cycles": 2}
  }
}
```

A task on another harness then names the fallback second in its chain, and only while the fallback's own status command says somebody is signed in: `claude auth status --json`, `codex login status`, or for pi `pi auth check --provider <provider> --json --no-refresh` on the provider the fallback's model names, read as the quick start reads them.
pi signs in per provider, so a pi fallback is asked about its own model's provider and never about pi's default one.
A fallback that is signed out, missing or unclear is left out, and the `pipeline gate agent:` line says so.
A task on the fallback's own harness has no second harness.
A task frozen before the fallback was named takes it with `goblins pipeline migrate <id>`, as it takes a newer version.

### What v6 needs

Policy v6 needs a no-mistakes build that applies launch selections.
That is the fork at `https://github.com/fpresta0607/no-mistakes` with its pull request 5, and with pull request 4, b05697a, which v5's Codex arguments already need.
A pi gate needs one more, the fork's pull request 10, which teaches the launch assertion to prove a `pi` selection, so a build without it accepts the Codex and Claude Code chains only.
The install puts in the build of that fork `install.ps1` pins, commit `710f031`, the head of the fork's main, and each release builds and signs it beside its own programs.
That main carries pull requests 4 to 10, the fix that gives a gate daemon a temporary directory of its own that outlives the goblin that started it (pull request 32), and the fix that hands the Test step's correction turn the whole response an agent refused, not a snippet of it (pull request 31), and the fix that makes a review with no answer text name what pi ended on and rerun once, not twice (pull request 34).
It also carries pull requests 46, 49, 52, 56, 60, 61 and 72, and the change that holds a repair round's rereview to what the fix changed, keeping the previous round's verdict on each file the fix left alone that no open finding names (pull request 79).
The checked-in `config/pipeline.json` is v7, which needs the same build.
no-mistakes proves Codex, Claude Code and pi selections only, so a goblin on any other harness has no gate from v6: `goblins spawn` refuses such a task in `no-mistakes` mode there, and `goblins pipeline run` refuses one switched to it.

### A gate that only checks

Policy v7 is v6 whose gate repairs no Test, Lint or CI failure: `auto_fix` is 0 for test, lint and CI, review stays 0 and rebase stays 1.
It follows the Supreme Overlord's decision of 2026-11-09, "Gate checks only, the goblin fixes its own failures".
From 2026-11-07 13:12Z to 2026-11-09 13:20Z each automatic Test repair round took 80 to 131 minutes in the gate's database, and three rounds of one branch took 382 minutes together, while a gate repair agent rebuilds the context the goblin that owns the branch already holds.
Under v7 a failed Test, Lint or CI step starts no repair agent and parks with its findings, and the goblin that owns the branch fixes them, as [The goblin fixes its own failures](#the-goblin-fixes-its-own-failures) describes.
A repository's committed `auto_fix` counts are ceilings under the frozen policy, so under v7 a repository whose `.no-mistakes.yaml` names a test, lint or CI count above 0 is refused at `goblins pipeline run`.
This repository names no test or lint count, so its machine config's governs each, and it names CI at 0.
A home moves to v7 as it moves to any version: its operator writes v7 into the home's `config/pipeline.json`, runs `goblins pipeline config-apply` in an idle window, and migrates each task frozen at an earlier version, as [Explicit machine configuration](#explicit-machine-configuration) describes.

### The goblin fixes its own failures

When `goblins pipeline run` or `respond` returns with its run still parked at a Test, Lint or CI failure that v7 hands back, it prints the step, each open finding with its severity and description, and the step's log, which is `logs/<run>/<step>.log` under the gate's home.
It also appends a `working:` line naming the run, step and findings to the task's status log, and the supervisor shows the task working on them, not waiting on the CEO.
A Test step whose every open finding is `test-command-no-turn` is not handed back: its test command was given no turn, as by a missing required environment, which is the CEO's to repair, so the driver says nothing of it and the supervisor shows the task blocked on its decision.
For a Test or Lint failure it moves the task's branch to the run's head when the branch still holds the head the run was submitted from, and keeps that head at `refs/heads/archive/handback-<run>-<head>` in the gate's repository, so the correction carries every commit the run validated.
A branch the goblin has moved since is left as it is, and the driver says to make the correction on the run's head.
A Test failure is a failed verification, so it also moves the task's implement worker up one tier of its harness, once for that run, and every read of that run says where it went and that the goblin implements the correction itself ([Model routing](contract/dispatch.md#model-routing)).
`respond --action fix` on such a step is refused, since the gate repairs none of it.
The one fix left is a Test rerun of nothing but `test-command-no-turn`, which asks no repair agent, so a test command refused its turn still runs again once its environment is repaired.

The goblin commits one correction of exactly those findings on the run's head and submits it itself:

```powershell
goblins pipeline validate-authored <id> --run <run> --findings <ids>
```

That is the owner path of validation admission, and it needs no CEO admission.
It is taken only from the task's own goblin terminal, only for a run whose policy hands its failure back while the run is still parked at it, and only when `--findings` names exactly the step's open findings.
A run already stopped, whoever stopped it, is never corrected by its owner: its correction is the CEO's to admit.
The worktree must be clean, its HEAD must be one commit on the run's head, and every file that commit changes must be one the branch already changes from origin's default branch or one the step's log or findings name.
A failure names a file by one of two rules.
Its path names it standing on its own, as a compiler, linter or stack trace prints it, such as `internal/pipeline/policy.go:12:3:`, `src/foo.ts(12,3):` or `./main.go:5:2:`: after `./`, or the task's worktree or the run's gate worktree and a slash, or, for a path with a directory, after the start of the text, whitespace, a quote or an opening parenthesis; and before the end of the text, whitespace, a colon, a quote, a parenthesis or a comma.
Its base name before a colon or an opening parenthesis, at the start of a line or after whitespace, as a Go test failure prints `policy_test.go:42:`, names it only when the branch already changes a file in the same directory, the repository's root for a root-level file.
A correction outside that scope, or one from another terminal, is refused before anything is sent, and the refusal names the CEO's `--admission-file` path.
The driver then stops the parked run with native `axi abort`, reads its native terminal preflight, and writes the admission itself, beside the task's frozen policy, as `owner-admission-<run>.json`.
That admission carries the run's own launch nonce, validation generation, intent and launch selection with today's trusted commit, and the task's frozen policy, so the correction is validated in the run's own validation family and every allowance the run spent stays spent.
It goes through the same native `axi validate-authored --admission-file` and journal as the CEO's admission, and the task's status log records a `pipeline-owner-correction:` line with the run, step, findings and task.
Anything wider, and every `validate-terminal`, stays the CEO's admission.

A published CI failure goes back to its goblin through [handback](#driving-a-task), as it already does in a repository that sets `auto_fix.ci: 0`.
Under v7 the goblin's `handback --resume` is held to the same scope: it comes from the task's own goblin terminal, and its one commit on the published head changes only files the branch changes or the CI failure names.
The CEO resumes any other.

A cycle means one repair followed by another review, after the initial review.
The driver counts completed rounds from the native database, so the budget is durable within a native run and restarting the CLI mid-run does not reset it.
A new run for independent work begins a fresh budget; a published CI correction uses [handback](#driving-a-task) to retain its original run's spent allowance.
After two or three repair cycles, actionable review findings remain unresolved.
A clean last review can still pass.
The driver never turns budget exhaustion into approval, skips a step, or uses `--yes`.

## Explicit machine configuration

```powershell
goblins pipeline config-drift
goblins pipeline config-apply
goblins pipeline migrate <id>
```

`config-drift` is read-only and prints owned field names, never their values or unrelated configuration.
`config-apply` requires a scheduled idle window: the daemon must already be stopped and every durable run must be terminal.
A config that already matches the policy changes nothing, so `config-apply` then needs no window and returns at once.
Without the window it refuses, naming each gate run in flight with its repository, branch and status and saying the change waits for them, or saying that the daemon runs with no run in flight.
Native v1.75.1 treats completed, failed, cancelled and ci_monitor_interrupted runs as terminal; pending, running, unknown and NULL statuses still block the window.
An interrupted CI monitor is not a passed gate and requires no cancellation or database rewrite to establish that it has stopped executing.
It takes the native daemon singleton lock throughout the database check, backup and replacement, preventing a concurrent daemon startup.
It does not stop or restart anything, cancel a run, or repair stale state.
The command currently supports Windows, using the native singleton lock contract retained by v1.75.1.
Use `NM_HOME` to select the same native home as no-mistakes; without it in the process both use the user's `NM_HOME`, which `goblins dev-drive move` sets when it moves the gate's home onto the Dev Drive, and otherwise `~/.no-mistakes`.

Before replacement, the original YAML is backed up beside the configuration with a unique timestamped name.
The backup and staged replacement receive the credential store's owner-only file protection because unrelated settings may contain credentials.
Replacement itself is atomic and preserves the live configuration's existing permissions, so an interrupted apply cannot leave a truncated shared file and every principal that could read it before still can.
The command prints the backup path, preserves unrelated YAML settings and comments, and refuses duplicate keys, aliases, anchors and merges rather than making an ambiguous edit.
It refuses a missing or unreadable database or configuration file.
An operator can restore the printed backup in another idle window; restoration is never automatic over an operator's intervening edit.

Owned machine fields under v6 are v5's without the chain and the Codex profile: `agent_args_override.codex` and `agent_args_override.claude` as below, the absence of `review_agents`, the absence of `agent_path_override.codex`, `auto_fix.review: 0`, and one automatic follow-up each for test, lint, rebase and CI.
v6 does not own `agent` or `agent_config`.
The `agent` list is the operator's, and it serves only runs started outside `goblins pipeline run`, since every run the CEO drives carries its own agents.
`agent_config` holds the operator's default model and effort for a task that names none.
So a machine whose operator set `agent: [claude]` by hand, as on 2026-10-09, reaches v6 with `goblins pipeline config-apply` writing the two argument lists and nothing else, and one config serves every harness and every fallback.
Owned machine fields under v7 are v6's, except that `auto_fix.test`, `auto_fix.lint` and `auto_fix.ci` are 0, with `auto_fix.review` still 0 and `auto_fix.rebase` still 1.
So a machine at v6 reaches v7 with `config-apply` writing those three counts and nothing else, and `config-drift` on it names exactly `auto_fix.test`, `auto_fix.lint` and `auto_fix.ci`.
Owned machine fields under v5 are `agent: [codex, claude]`, `agent_config.codex: {model: gpt-6.1-sol, effort: xhigh}`, the absence of `review_agents`, `agent_args_override.codex: [-c, 'service_tier="default"', --ignore-user-config, --disable, plugins, --disable, apps, -c, 'personality="pragmatic"', -c, 'model_auto_compact_token_limit_scope="total"', -c, features.multi_agent=true, -c, project_doc_max_bytes=65536]`, `agent_args_override.claude: [--strict-mcp-config]`, the absence of `agent_path_override.codex`, `auto_fix.review: 0`, and one automatic follow-up each for test, lint, rebase and CI.
Under v4 `agent_args_override.codex` is `[-c, 'service_tier="default"']` and the policy does not own Claude's arguments.
Under v2 and v3 `agent` is `[codex]` and both global `review_agents` roles carry the version's Codex profile.
The raw Codex argument override forces standard service for every managed role, so a user-level fast or priority preference cannot leak into a gate, while `agent_config` owns model and reasoning effort.
Removing the Codex executable override makes no-mistakes resolve the native `codex` command from `PATH`; executable overrides for other harnesses remain operator-owned.
Up to v4 the exact legacy CEO-owned Claude model and effort vector is removed during apply, and a differing operator-owned Claude vector is preserved.
From v5 the policy owns Claude's arguments as well, since Claude is a gate agent of its chain, and from v6 the agent of every Claude Code goblin's gate.
Document follow-ups and other native settings retain their existing values.
Spawn never rewrites shared YAML.
The YAML parser dependency is needed to preserve unrelated configuration structurally; v3.0.1 avoids the old parser's [known panic vulnerability](https://pkg.go.dev/vuln/GO-2022-0603).

Run `config-apply` before migrating any live task snapshot, unless `config-drift` already reports no drift for the current policy.
`migrate` requires the applied target-version global configuration and the task's pipeline lock.
It writes only the task's own snapshot, metadata and status, never the shared config, so it runs while the daemon serves gate runs: a policy that only catches up with the machine never needs the daemon stopped.
It accepts every forward transition, from any of v1 to v6 to any later version, and preserves the task class and `review_cycles` cap exactly.
Between two policies of one version from v6, which can differ only in the fallback the operator names, it moves the task to the current one the same way.
The same frozen policy is a no-op; downgrades and invalid snapshots are refused.
Before replacing either owned field, it writes a task-local transaction journal containing the validated old and new snapshots and the expected audit event.
An interrupted command resumes that journal under the same pipeline, cleanup and metadata locks before `run`, `respond`, `recover` or `migrate` trusts the snapshot hash.
The task-scoped metadata lock also serializes `goblins pr check` and `goblins switch`, so neither command can publish a stale whole-record update over the migrated hash.
Migration removes its journal only after the snapshot replacement, metadata hash update, and audit append have all completed while that lock remains held.
Recovery finishes the migration once or rolls both owned fields back when audit persistence fails, without overwriting unrelated task metadata.
An append reported as successful commits the audit without a second read, while an errored append whose persisted state cannot be reread leaves the forward journal intact for retry.
The task status receives one line containing the class, cap, old hash and new hash.
Migration never changes a worktree, native run row, gate ref, origin ref, or review result.

## Resuming a paused task

Resume restores the contributor's terminal and retains its original validation run, source, findings and repair history.
Pause persists that run's identity before process teardown, including terminal runs and the live runs every pause leaves running.
Resume reads the saved run identity without recovering branch custody, starting validation or replacing the run.
A different current run, unreadable state or a run without a saved pause identity refuses the resume instead of adopting another run.
A direct-PR task whose current branch has no gate can resume even when its saved pause names an earlier branch's run.
A still-live original run is named in the contributor's resume instruction, so it continues that run without starting another beside it.
A run that ended while its goblin was paused, as a daemon restart ends a run whose step was executing, is named with its terminal status.
Resume does not select validation agents or migrate the frozen policy; an older policy alone does not block contributor restoration.
Actual validation requires an explicit supported pipeline command, which still enforces the frozen policy and launch selection.
Continuing retained terminal work requires its supported source-bound continuation, not a replacement run with fresh repair allowance.

## Driving a task

```powershell
goblins pipeline run <id> --intent-file intent.md
goblins pipeline run <id> --intent-file intent.md --closes 1996
goblins pipeline respond <id> --action fix --findings finding-id --instructions "Concrete guidance"
goblins pipeline respond <id> --action fix --findings finding-id --scope-decision <answered-notify-sequence>
goblins pipeline respond <id> --action approve
goblins pipeline respond <id> --action approve --accept ask-1,bug-2 --reason "the only failure was the sandbox's missing compiler"
goblins pipeline respond <id> --branch fix/connections --action approve
goblins pipeline recover <id>
goblins pipeline handback <id> --run <run-id>
goblins pipeline handback <id> --run <run-id> --revalidate-publication
goblins pipeline handback <id> --run <run-id> --resume --receipt <native-receipt-id>
goblins pipeline migrate <id>
```

Write the run's intent to a file and name it with `--intent-file`.
A short one may be given with `--intent <text>`.
Either way `goblins pipeline run` hands it to no-mistakes in a file, never on a command line.
An intent is prose, often several thousand characters that name a shell, and on 2026-10-02 Microsoft Defender detected one on a command line as `Trojan:Win32/ClickFix.DQ!MTB`, the lure that has a person paste such a line, and Windows refused the start.
A no-mistakes before 1.86.0 has no `--intent-file`, so for such a build alone the intent stays on the command line, and the command says so.
Every run starts with `--no-publish-intent`, so the intent, a task's private brief with its machine-local paths, ownership decisions and routing, never becomes the Intent section of the pull request the gate opens (issue 1866).
no-mistakes keeps that on the run, so a later refresh of the body after CI leaves it out too, and every step but the pull request's drafting still reads the whole intent.
no-mistakes never took a closing reference from the intent, so none is lost: its `## Issues` section comes only from `axi run --closes`.
A no-mistakes before 1.80.0 has no such flag, so on it no run starts, and the command says to update it.
A run started before this change, without the flag, keeps publishing its intent; reattaching to it passes no flag, since no-mistakes refuses one a running run was not started with.
Name each issue the run's pull request closes with `--closes`, as `1996` or `owner/repo#1996`, once for each.
no-mistakes neutralizes the closing words in every text an agent wrote, the intent included, so a `Fixes #N` in the intent closes nothing.
A no-mistakes before 1.87.0 takes no `--closes`, so a start that names an issue on one starts no run and names the version to update to.
A reattach to a live run keeps the closing issues it started with.
A response's `--instructions` still reach no-mistakes on its command line, which is the only way it takes them, so keep them short and plain.
An approval `--reason` reaches it the same way, and the driver refuses one that is not a single line of plain text.

Commands operate in the recorded task worktree and use its frozen policy.
A task that gates several branches from extra worktrees names one: `run`, `respond`, `recover`, `handback`, `validate-authored` and `validate-terminal` take `--branch <branch>`.
`respond`, `recover` and `handback` take `--run <run-id>` instead, and `validate-authored --findings` requires it; `terminal-preflight` requires `--run <predecessor>`.
The command then acts in whichever of the task's recorded worktrees, its own or an extra one `goblins worktree add` made, has that branch checked out.
For `respond`, `recover` and `handback`, a selected run must be its branch's latest run in the project.
Preflight and admission bind the exact predecessor's project and branch; native owns latest-owner and replay checks for admission.
A branch or run that none of the task's worktrees has checked out is refused before anything reaches the gate.
The pane exports `GOBLINS_HOME` and `GOBLINS_STATE_OVERRIDE` and every gate step inherits both, so the `--intent` text must require any step that runs `goblins`, or a shell that resolves it, to clear them or point them at a temporary directory.
`internal/home` refuses the inherited fleet home from a test binary, which covers `go test`, but the real `goblins` binary is not a test binary and no-mistakes has no per-repo step environment setting, so the intent is the only place left to state it.
Commit work on a named feature branch before `run`.
The project must be initialized for no-mistakes, with readable committed task and origin default-branch `.no-mistakes.yaml` files.
Refresh origin before starting; global reviewer/fixer drift is refused.
A repository's committed `auto_fix` counts are read from the submitted branch, which can edit its own `.no-mistakes.yaml`, so each is held to the frozen policy's count as a ceiling: a repository may lower one, which only sends more to a person, and a count above the policy's, a negative count, or a key the policy does not govern (anything but review, test, lint, rebase, ci and its legacy name babysit) is refused.
No-mistakes v1.75.1 does not expose an assertion that binds an expected trusted SHA and effective primary after its fresh fetch but before agent creation.
Up to v5 the `run` check validates the trusted inputs before invoking native start and does not establish that post-fetch binding.
From v6 the launch selection establishes it: the daemon proves the trusted commit and every role's agents after its own fetch and before any agent starts.
A repository's `agent` field continues to select only its native primary path and cannot replace the global reviewer or fixer profiles.
An earlier unresolved run cannot be restarted to reset its budget.
A run that is still working is reattached to instead: repeating `goblins pipeline run` with the same branch, submitted head (or current head when no submitted head is recorded) and nonblank retained intent reattaches to it, so no new run starts and no review budget resets.
This original head comparison remains independent of the handback schema and skips the new-start checks: a clean worktree, the committed gate files, and origin's default branch with its tracking ref, which a merge landing while the hold lasts leaves stale until the next fetch (issue 1586).
Only when that head comparison fails does the driver look for a published head the caller adopted, and then for an accepted CI handback.
A run that produced and published a later head, which the caller adopted through guarded `axi sync`, keeps working in CI, so the same nonblank retained intent may reattach to it (issue 1889).
That published head rule requires the branch's latest run to be running, keep custody and have no push in flight, its current head to equal its last published head on the task branch's own push ref with an open PR, and the clean caller to be on that branch at exactly that head.
The driver re-reads the run and the caller before reattaching, and a caller head that is not that published head, including a later unpublished native descendant, stays refused.
Otherwise the driver inspects the latest CI-handback receipt for the latest run of the exact repository and branch, without filtering out a newer unaccepted receipt.
After an accepted CI handback, the same nonblank retained intent may also reattach to a running run when that receipt's accepted head, candidate head, current run head and clean caller HEAD all match exactly.
This exception requires complete valid receipt and candidate checkpoint identities, the canonical recorded caller and its actual symbolic branch, no returned custody or active push, a matching open PR URL, nonempty matching persisted target fingerprints, the canonical push ref and matching candidate upstream and fork identities against the registered repository.
It permits the released publication at the receipt's generation or the accepted publication at exactly the next generation; missing, unreadable, malformed, ambiguous, changed or unaccepted evidence refuses.
The driver re-reads the binding and caller before returning the original run with no trusted launch head and dispatching one bare `axi run`, without intent, launch selection, synchronization, handback, response, retry or new-start fallback.
A later native descendant of the accepted head remains outside this exception unless the published head rule admits it, and each final re-read is a cooperative check, not an atomic native expected-run assertion.
Reattachment grants no finding approval, custody change or repair allowance.
Native `axi run` bounds its hold so a harness tool cap cannot hang it, and a hold that ends with no decision is not a gate failure: `goblins pipeline run` then says the run keeps working and names the reattachment, never only the native exit code.
A daemon serving other runs on a loaded machine can miss its 250 ms health reply, or a request's reply, while it is alive (issue 2158).
`goblins pipeline run` and `goblins pipeline respond` read the daemon by its `daemon.pid` and that process, and by the runs its database holds open, never by a reply deadline, and send it nothing to do so.
A native command a live daemon turned away at its health reply never reached it, so the same command is made again 30 seconds later, four times in all, each wait said as the daemon's turn.
Any other missed reply is not repeated, because its request may have landed: the command says the daemon is busy, not stopped, and names the run the branch already holds, which repeating `goblins pipeline run` reattaches to, so a lost or late reply to a start never becomes a second run.
When every try missed the health reply, nothing reached the daemon: a start with no run on its branch says no run was created, and an answer says it never reached the gate.
After any other miss with no live run on the branch, a start says the lost request may still wait in the daemon and create a run later, and that it must not be repeated now: on 2026-11-11 a busy daemon served three such starts minutes later, and each new run replaced the one before.
It says to read `no-mistakes axi status` until a run is on the branch and then reattach, and to start again only once the daemon answers its health reply and still no run is there.
A scope response turned away on every try says so too, with its journal, never that no retry was made.
Neither ever starts or restarts the shared daemon, whatever native's own hint says, and a daemon whose process reads stopped or unreadable is a question for the CEO.
A start whose intent or head names other work than the live run's, its adopted published head or its verified accepted handback is still refused, with that run and the caller's head named.
Use native read-only `axi status` and `axi logs` to inspect progress; the engine's guarded `axi sync` remains the branch synchronization interface after validation.

For eligible published CI failures with automatic CI repair disabled, `handback` explicitly returns the work to its contributor through the native handback protocol.
It prints the native receipt and next action while retaining the same task, run, pull request, unresolved findings, commits and spent repair allowance.
Follow the native source-preservation instructions, commit the authored correction, then submit it with `handback --resume --receipt <native-receipt-id>` for full validation in that same run before publication.
The wrapper binds each request to the exact recorded repository, run, branch, caller, heads and push generation; continuation also binds the native release receipt.
When Document or Lint advances the publication beyond its recorded Review or Test head, the registered primary CEO may explicitly select `--revalidate-publication` to request native publication revalidation.
Goblins and gate agents cannot select this new source authority, even with inherited primary credentials or ancestry.
Without the flag the existing native eligibility checks apply unchanged.
The selection preserves historical evidence and spending; it does not certify the published descendant, and receipt-bound `--resume` requires fresh proof of the final source in the same run and pull request.
Continuation uses the receipt's recorded selection without requiring the flag again.
Native still verifies source ancestry and publication identity, and a refusal, including one after a native rebase, remains unresolved rather than being relaxed by the wrapper.
Both handback modes refuse a pending or unreadable policy migration journal without changing the journal, frozen policy, task metadata or audit log.
Recover the migration explicitly with `goblins pipeline migrate <id>` before requesting handback.
It requires a native build supporting `axi handback --expect` and receipt-bound `--resume`, plus `--revalidate-publication` when explicitly selected, and refuses when that contract is unavailable.
It never substitutes recovery, approval, a CI fixer or a fresh run.
A refusal or elapsed hold is reported without automatic retry; inspect the native status and receipt before continuing.

Terminal validation admission is separate from `run`, recovery and CI handback:

```powershell
goblins pipeline terminal-preflight <id> --run <predecessor>
goblins pipeline validate-authored <id> --admission-file admission.json
goblins pipeline validate-terminal <id> --branch fix/correction --admission-file admission.json
```

Only the registered primary CEO may submit either admission, including accepting the committed source and choosing a terminal successor's fresh harness selection, except the goblin's own correction of a handed-back failure that [The goblin fixes its own failures](#the-goblin-fixes-its-own-failures) describes.
The registered primary CEO or the task's own authenticated goblin may inspect `terminal-preflight`, which delegates read-only observations to native without fetching, recovering custody, preparing an admission or starting validation.
Gate agents cannot submit admission or inspect preflight, even with inherited CEO or goblin terminal proof.
Keep that output private: it holds local paths, source observations and recovery anchors.
The controller prepares one UTF-8 JSON object of at most 64 KiB under the native build's validation admission contract, using its original predecessor receipt, immutable snapshot digest, original launch nonce and validation generation, original intent digest and publication identity.
Unknown fields and trailing input are refused.
Its `controller_policy` must exactly match this task's frozen policy hash, class and review cycles, as a controller assertion rather than proof of native historical policy.
`historical_source_completeness` stays `unknown`; a clean candidate certifies none of the predecessor's missing dirty or untracked files.
An unchanged terminal candidate also requires the native terminal protocol's independent `terminal_generation`, explicit `accept_unknown_source: true`, current trusted launch assertion and exact `source_anchors`.
The driver checks task, project, canonical recorded worktree, branch, predecessor repository and exact full candidate commit/tree before delegating, requiring a clean worktree including untracked files and submodules; native atomically checks the immutable history, latest owner, original launch and intent, publication target, anchors and successor proof.
Completed ordinary predecessors remain native-ineligible.
To prevent a replaced input file from changing checked bindings, native receives a temporary byte-identical snapshot, deleted when the call ends.
No ordinary `axi run`, policy migration, repair, skip or automatic retry is substituted on refusal.
Every stage runs fresh with zero automatic and manual repair capacity, and native retains the predecessor and cumulative spending.
Repeating identical admission observes the same child; only an untouched pending acceptance can explicitly start after native revalidation, and progressed or cancelled children are never restarted.
Each native admission invocation's raw stdout, stderr, exit code and execution error, including refusals, receipts and cumulative spending, is retained in `state/tasktmp/<id>/validation-admissions.jsonl`, keyed by input digest, with an entry in the task status log.
Local checks that refuse before native invocation create no admission record; preflight prints its private output without writing an admission record.
A bounded hold returns unresolved work and directs the controller to inspect the receipt before explicitly replaying the identical admission.
Pending or unreadable migration journals refuse all three operations without mutation; only explicit `migrate` may resolve them.
These routes require native support for their exact `axi validate-authored --admission-file`, `axi validate-terminal --admission-file` and `axi terminal-preflight --predecessor` contracts.
Authored admission support alone does not provide `terminal-preflight` or `validate-terminal`; both require the companion native terminal-continuation feature.
This driver does not change the [install pin](#what-v6-needs); installing the required native build and this managed driver is separate from validation, and admission grants no live installation or adoption authority.

`respond` requires durable evidence of one parked gate in this project's current branch.
It refuses a gate already answered or still running and checks that the active review's persisted automatic limit is disabled or zero.
An explicitly selected finding with an empty action is actionable only when the active gate is not review and its persisted automatic-fix limit is positive.
Review remains fail-closed for empty or unknown actions.
Fix responses name concrete actionable finding IDs; selecting an `ask-user` finding is an explicit decision, never automatic consent.
When a selected fix has verified `no-op` IDs to ignore, the driver forwards them from the current parked gate through native `--ignore` and binds the recorded run and step with `--run` and `--step`.
An ID that also names an actionable row is never ignored, and duplicated or malformed ignored IDs are refused.
There is no arbitrary managed `--ignore` input: unknown or actionable findings cannot be declined through this translation.
Native refusal, including an earlier round's applied fix that cannot be declined, stays a refusal without a retry or replacement run.
The native ordinary-fix API binds run and step but exposes no expected-round assertion; this translation uses the durable current gate and does not claim an atomic round assertion at native dispatch.
Approval is accepted only when every finding is explicitly `no-op`, or the findings list is empty.
The one exception is the CEO's own decision: `respond <id> --action approve --accept <ids>` takes the open findings as they stand, except a recorded required test environment refusal, which only a successful Test retry clears, as [the binding](#any-command-in-the-line) says.
It is honoured from the registered primary CEO process, proven as `goblins question` proves it.
It is honoured too from the task's own goblin, on its run's review step once that run has spent its review repair cycles (issue 2049).
The goblin is proven by its terminal: the process names the task's own terminal and carries a proof that terminal was given, and an agent a gate started is refused, since it carries the environment of whichever goblin started the shared gate daemon.
A goblin's accept on any other step, the Test step included, before the cycles are spent, on another task's run or with `--reason` is refused with why before anything is recorded.
The IDs must be exactly the gate's open `ask-user` and `auto-fix` findings plus any configured Test command failures accepted under the following exception, no more and no fewer.
A configured Test command failure with an empty action can also be explicitly accepted by the primary CEO with a nonblank `--reason` and every open finding ID in `--accept`, including that failure.
This exception recognizes only the native `test-command` category, `error` severity and configured-command failure description with a nonzero exit code; unrelated empty actions still need their fix.
The response names the recorded native run with `--run`, preserves the failed command, findings, evidence and spent budgets, and records a Test exception rather than a successful command.
Native refusal remains a refusal, including when the installed engine does not support the run binding.
The task's status log records `pipeline-findings-accepted` with the step, run, round, IDs and who accepted (`by=ceo`, or `by=<task>` for its goblin) before approve reaches the gate.
A goblin's accept also reaches the CEO's wake queue as a notify with the same words, which asks nothing, so nothing waits on it; one that cannot be queued sends no approve.
The goblin accepts as they stand only findings that are not correctness, security or regression defects, and a real defect among them goes in one follow-up commit on the same pull request after its first CI, named in its done report.
An approved Test exception likewise keeps the operator's reason: `respond <id> --action approve --reason "<why>"` hands it to the native `--reason`, which no-mistakes records as the Test step's approval reason, and the task's status log records `pipeline-approval-reason` with the step, run, round and reason before approve reaches the gate.
It is the registered primary CEO's reason alone, so a goblin is refused before anything is recorded.
The native engine keeps a reason only for an approve of the Test step, so the driver refuses one for any other gate and for `fix`, and it refuses a blank reason or one that is not a single line of plain text: the engine would store a blank as none and the run would read as a Test exception nobody explained (issue 1422).
An empty `--reason`, as `--reason "$WHY"` gives with `WHY` unset, is a reason given and refused like any blank one, never read as no reason (issue 1585).
A reason never approves anything, so open findings still need the exact `--accept` and a clean gate still needs its own approve.
The registered primary CEO alone may answer a pending Review finding with `respond <id> --action fix --findings <one-id> --scope-decision <answered-notify-sequence>`.
The reference is the task's own `notify-<task>-<sequence>` ID or its bare sequence, and it identifies the answered blocked notify the task asked in its current generation, whose exact answer is the accepted requirement correction; another task's notify is refused.
The managed driver reads native typed `get_run` source identity and the latest persisted `axi rounds --step review --run <run> --json` round before writing the one private JSON scope file native consumes.
It retains that request's bytes and hash with the exact native stdout, stderr and outcome in the task-private `review-scope-decisions.jsonl` journal, and reports the response only once native's own rounds evidence keeps that exact decision.
An old native engine, wrong or unavailable acknowledgement, unreadable evidence, replay with any changed scope binding, or a non-primary caller refuses without an ordinary response fallback, automatic retry, changed repair budget or replacement run.
It requires a native build carrying the typed accepted-Review-scope contract: `get_run` RunInfo naming the parked Review round, `--scope-decision-file`, and an `axi rounds --json` document whose round publishes `id`, `step_result_id`, `reviewed_head_sha` and `trusted_config_sha` and whose top level has the `scope_decisions` array; an earlier build cannot carry it: a round missing those fields is refused before the scope file is written or a response is sent, and any refusal or unkept `scope_decisions` acknowledgement is recorded with its exact native outcome in `review-scope-decisions.jsonl`, with no ordinary-response fallback, automatic retry or new run.
A blank `--scope-decision`, as an unset variable gives, is a decision given and refused, never read as no decision.
Unsupported or malformed evidence fails closed.
The command returns exit 3 for unresolved work requiring a CEO decision, or on a spent review its own goblin's accept, which the refusal names, without advancing the gate.
Other refusals return exit 1.
This is a cooperative driver guard, not an operating-system sandbox around direct native commands.
Agents must use the driver for managed tasks and must not invoke native mutation commands to bypass its budgets.

`recover` returns source custody for eligible failed or cancelled runs without moving the contributor's branch.
An authored validation child interrupted by restart that inherited its predecessor's publication has a separate recovery path from an unpublished run.
Immutable admission provenance must bind the unchanged recorded and submitted candidate, original launch and intent to the initial child, and its inherited publication, PR identity and push generation to the predecessor snapshot.
The registered caller must be clean at the recorded candidate; any remaining managed worktree must also be clean at that head in the registered gate repository.
Native `terminal-preflight` must verify current history, latest ownership and the exact authored-admission and restart recovery anchors.
The gate branch must still match the inherited publication or the predecessor's recorded or submitted head.
Before custody is returned, native status must offer plain `axi sync --recover`, and its receipt must confirm recovered custody with unchanged source.
The driver then verifies the durable custody stamp, admission, local head, retained gate branch and source anchors without controller-side database updates or gate alignment.
The output reports the local head and gate branch head separately.
A retry after custody returned requires native `local_ahead` / `blocked_local_ahead` with an `ahead` relation, which is expected when the candidate is ahead of the retained publication.
An unpublished authored child can also retain its predecessor's older gate branch while its unchanged recorded and submitted candidate is already user-owned.
That case requires the same immutable admission, clean source, verified history and exact authored-candidate anchor, with native `user_owned` state and safety and an `equal` relation.
The driver reports the separately preserved local and gate heads without changing refs, history or the null custody stamp, since native has already released this source.
Ordinary unpublished cancellations gain no authored-provenance exception.
A step whose status is still running, fixing, awaiting_approval or fix_review keeps refusing, as does one whose recorded agent process is still alive, or whose recorded pid's liveness cannot be read, since an unanswered pid read is never permission (issue 2166).
A pid left on a failed or cancelled step by a daemon crash is evidence of past ownership, not present ownership, so it does not block recovery once no process holds it.
Ordinary published runs, children that published their own source, changed or missing provenance or anchors, stale generations, configured automatic repair or a spent repair round and uncertain history remain refused.
Recovery preserves failed stages, historical source uncertainty and cumulative automatic and manual spending; it renews no repair allowance or review cycles.
It returns source custody only: a later authored correction still requires the source-bound validation admission described above and every required stage before publication.
Default repository CI exercises these controller paths and refusals with real Git and SQLite fixtures, substituting only the external native transport.
The separate native admission, restart, recovery and continuation journey keeps its prepared-executable/source binding and explicit native-fixture prerequisite.

For an unpublished run, `recover` can repair a stale recorded head that blocks native recovery.
It first preserves whatever the run's worktree still holds uncommitted, as described below, and names that commit in its output.
For pipeline-owned recovery that keeps the submitted head, it requires a clean worktree whose head exactly matches both the submitted head and native gate branch.
The stale recorded commit must be an ancestor of that preserved head, or the gate's own rebase or merge of it.
A run whose rebase step moved its recorded head and that then failed before it pushed leaves that rebase unverified, and native recovery refuses it with `blocked_recover_unverified_head`, with or without `--keep-local`.
The driver accepts such a head only as a pure rebase or a pure merge.
A rebase's first-parent commits that no remote-tracking ref of the gate repository holds, laid on the first commit one does, must carry the submitted head's changes and nothing else.
A run under `rebase.strategy: merge` integrates with a merge commit instead, which must have exactly two parents, the submitted head first and a commit a remote-tracking ref holds second, and the tree `git merge-tree --write-tree` gives for those two.
A gate fix commit or conflict resolution on the rebase or merge, or content from a base no remote-tracking ref holds, is refused, so keeping the submitted head drops nothing that neither it nor a remote-tracking ref holds.
Before a compare-and-swap repairs the stale database field, the old commit is anchored under `refs/no-mistakes/recovery/<run>/recorded`.
The driver then invokes native `sync --recover --keep-local` and verifies that custody was durably returned without moving the local or gate head.
A recorded head that holds the gate's own commits, such as a rebase followed by review fixes, would lose them if the submitted head were kept, so it is recovered at the recorded head instead, and the refusal from the submitted head names that head.
With the clean task branch at the recorded head, native status offers plain `sync --recover`; the driver requires the gate branch to still name the submitted head, runs that recovery, which returns custody without moving the local or gate branch, and requires the local head unchanged.
It also requires the engine to have verified the recorded head when the run ended, as its crash recovery does, since native recovery first checks an unverified head against the gate branch and the head it then takes can move the task branch.
The `custody_returned` alignment below then moves the gate branch and both database heads to the recorded head, since native recovery leaves the gate branch at the submitted head, which a rebased head never descends from, and the next run's push to the gate would be refused as a non-fast-forward.
When native already reports clean user-owned custody, the registered task worktree and branch may contain rebases and follow-up fixes made after custody returned.
Before a CEO-managed alignment, the driver stores the stale gate head under the immutable `refs/no-mistakes/recovery/<run>/gate` ref and verifies it again before moving the gate or database heads.
That ref is durable CEO-swap evidence, so every `custody_returned` retry that finds it requires the stale commit and the native recovery anchor before accepting an aligned state.
With no CEO-swap evidence, a native `custody_returned` state is accepted only when its native anchor equals the fully aligned local, gate, recorded, and submitted head.
The driver imports the local commit into the internal repository without writing a fetch ref, and compare-and-swaps the internal gate ref and both database heads to the local head.
If the process exits after the gate compare-and-swap, the stale gate anchor lets the next recovery recognize that exact half-state and finish the database compare-and-swap.
After a reported database compare-and-swap failure, the driver reads the exact run row before deciding whether the advanced gate can roll back.
A confirmed unchanged row permits rollback, a committed row continues forward, and an uncertain or conflicting row preserves the advanced gate for a safe retry.
Native status must confirm all three heads before success is reported.
If a later status check triggers rollback, the gate moves back only after the database rollback succeeds, so a transient database failure preserves the forward-aligned retry state.
The local branch and origin refs are never moved.
The command never resets a branch.
Pipeline-owned recorded-head repair requires strict ancestry or that proof of a pure gate rebase.
After native has already returned clean user-owned custody, the registered identity, unpublished-run state, stale-head anchor, and compare-and-swap guards are the authorized boundary, so the driver does not reject legitimate rebases or follow-up commits by comparing them with the stale head.

A gate's fixer works in the run's worktree, which no-mistakes removes with force once the run is over, so the driver keeps what the fixer left uncommitted before anything takes that worktree away.
Before unpublished recovery runs any native command, and before a stop aborts a live run, every tracked and untracked change in the run's worktree is committed and pinned under `refs/no-mistakes/recovery/<run>/worktree` in the gate repository, and that commit's parent is the head the run ended at.
The commit is made from an index of the driver's own, so the goblin's staged and unstaged work is exactly as it left it, and a first capture stands rather than being replaced by a later one a goblin may already be restoring.
The commit is named in the branch-sync evidence the recovery reads, in `goblins pipeline recover`'s output, and in the lifecycle record of a stop, so an interrupted fixer's repairs stay recoverable with `git cherry-pick -n <commit>` after the worktree is gone.
On 2026-10-23 an allowance-interrupted review repair lost its frontend changes exactly this way: the step failed, custody returned at the submitted head, and native recovery swept the worktree before anything read it (issue 1416).

This repository's committed automatic-fix overrides remain authoritative for a new submitted branch.
Once the shared config runs the current policy, migrate each task frozen at an older version explicitly before its next gate command.
Newly spawned tasks freeze the version the home's `config/pipeline.json` holds.

## Verification levels

`goblins gate test` is this repository's test step (`commands.test` in `.no-mistakes.yaml`), and it plans before it runs.

```powershell
goblins gate test                     # fast, the default, or changed for a project whose hosted CI is its test evidence
goblins gate test --level changed     # vet as the change requires, and only the tests the change added or edited
goblins gate test --level affected    # every package the change reaches, which CI's run also covers
goblins gate test --level full        # every package
goblins gate test --plan              # print the plan and run nothing
goblins gate test --focused           # repair failures and paths from NO_MISTAKES_TEST_FOCUS
goblins gate lint                    # required vet scope, without running tests
```

| Level | `go vet` | `go test` |
| --- | --- | --- |
| `changed` | the packages the change requires | the tests the change added or edited, each by name in its package, plus required policy guards |
| `fast` | the packages the change reaches | the changed packages and the packages a contract selects, unless the policy lists them as slow, plus required policy guards |
| `affected` | the packages the change reaches | the same packages |
| `full` | every package | every package |

See [the test and lint command contract](commands/gate/test.md) for focused repair inputs, named-failure requirements, fallback scope and vet-only lint behavior.
The Test step reuses the repository environment and existing fixtures described in CONTRIBUTING.md.
Unavailable browser or live-harness prerequisites leave their scenarios untested with the missing prerequisite named, without building another full application.

At every level, `goblins gate test` also selects the trusted policy's `guards`, even when no Go package changed.
A guard runs separately before vet and package tests, with their `go test` arguments and `-timeout 45m`, only when those package tests do not already cover its whole package, including packages scheduled alone.
A guard with named `tests` runs exactly those tests, so selecting it does not run unrelated slow tests; without `tests`, it runs its whole package.
An optional `goos` restricts the guard to that operating system; other platforms list it as deferred with the required and current platforms, never as executed, and its platform's CI remains required.
`goblins gate prepush` uses the same guard declarations and platform rules, omitting a separate guard run when its selected whole-package tests already cover it.

A change reaches the packages that own a changed file, the packages that import one of those directly or transitively from their code or tests, and the packages a contract of the policy names for a changed file.
A package owns Go build inputs (`.go`, `.s`, `.c`, `.h` or `.syso`) directly in its directory, the files under its `testdata` folder and the files its embed patterns cover, even deleted files, so a doc the binary embeds selects the package that embeds it and a doc nothing embeds selects none.
A changed Go build input in a directory with no package names a deleted or moved package, which is not tested, but every package that still imports it directly or transitively is.

A change requires `affected` before it merges, and `full` once `go.mod` or `go.sum` changed, the policy cannot be read, or a changed file is one the policy does not account for.
CI tests the level a change requires on the pull request's own run, reading what the change reaches as `goblins gate test --plan` does, and every package in the merge train's run that lands it.
So CI is the check of the level a change requires, and nothing reaches main that the full level did not test.
With no `--level` the step runs `fast`, which is what the gate runs.
Running the required level here as well ran the slowest packages twice, once on this machine and once in CI: from 2026-10-05 to 2026-10-07 every such local run took a median of 33 minutes, where CI tested every package in 11.
A level asked for runs instead, such as `affected` to reproduce what CI found; a run at a level narrower than the change requires exits 0 when its checks pass, and its last line and its report name the level the change requires, which CI's run checks.

`changed` is the level of a project whose hosted CI is its test evidence.
It vets what the change requires, and runs only the test functions the change added or edited, read from the Go source at the merge base and at HEAD, each package's with `-run '^(TestA|TestB)$'`.
A test whose function did not change, a test helper and `TestMain` are not run here, and its plan names each package whose tests it left with `left to CI:`, since CI runs every test before the merge.
Nothing is skipped: what this level leaves, CI runs on the pull request and in the merge train, and the CI step stays the judge, with no approval by hand.

Before a push, `goblins gate prepush` runs more than the fast level and picks the tests of a slow package itself, so nobody runs tests by a name they guessed ([Before a push](#before-a-push)).

The plan reads the checkout before anything runs, which is `go list` opening every Go file one after another.
That read takes a second with the file cache warm, and waits on the disk when it is not: for hours after a reboot, a read that reaches the disk can take seconds.
A plan gets 15 minutes.
One that outlasts them tested nothing, so `goblins gate test` and `goblins gate prepush` exit 125 with a last line that starts `nothing was tested, since the plan stalled`, as `goblins gate turn` does for a run that took no turn, and `goblins verify` records that command as `not_run`.
Nothing about the change failed, and the next run starts from a warmer file cache.
The plan prints why each package is in it (`changed`, `imports <package>`, or a contract's reason with the file that changed) and every test run the level left to a broader one, with why.
It then lists the changed files that are outside the Go checks, under the policy's reason for each, and the changed files the policy does not account for, so what the step does not check is read from its output and not assumed.
This excerpt shows the package-selection part of the plan:

```text
goblins gate test: level fast, the default, since CI runs every package; the change requires affected: the default for a change
goblins gate test: the change since 0123abcd reaches 3 package(s); CI runs every package
- example.com/m/internal/doctor (tests run or read these scripts: install.ps1)
- example.com/m/internal/installscript (tests run or read these scripts: install.ps1)
- example.com/m/internal/installtest (tests run or read these scripts: install.ps1)
left to the affected level:
- tests of example.com/m/internal/installscript (a slow package)
changed files outside the Go checks:
- CONTRIBUTING.md, docs/pipeline.md (documentation and housekeeping no Go check reads)
```

CI tests the level the change requires on the pull request, and every package in its merge train's run, whatever the local level was.

The repository's verification policy is `config/verify.json`.

`slow_packages` are the packages whose tests take minutes on a loaded machine, each named by its directory from the repository root with forward slashes, and `.` for the root package; the fast level vets them and leaves their package tests to `affected`, subject to the required guard rules above.

`contracts` say what the import graph cannot see: the files a package's tests read by path, such as a script they run.
A change to a file one of a contract's `paths` names selects the contract's `packages`, named as `slow_packages` names them, and the plan gives the contract's `why` with the file.
A contract selects its own packages and not the packages that import them.

`outside` names the files no Go check reads, each rule with why: a doc nothing embeds, or the frontend's sources, which its own checks cover.
A change to one of them selects no package, and the plan lists it under the rule's reason.

A path is a pattern from the repository root with forward slashes: `*` and `?` match inside one name as Go's `path.Match` does, `**` stands for any number of folders, none included, and case does not count.
A file a package owns is that package's whatever a pattern says of it, and a contract is read before `outside`.

A policy that has `contracts` or `outside` says what every file is.
Under it a changed file that is in no package, under no contract and not listed as outside is one nobody has said anything about, so the change requires `full` and the plan names the file.
`TestTheRepositoryPolicyAccountsForEveryTrackedFile` in `internal/gatetest` fails when a tracked file is in that state, so a new kind of file gets its line in the policy in the change that adds it; that change is itself planned by the default branch's policy, which does not know the file yet, and so requires `full` once.
`TestTheRepositoryPolicySelectsEveryPackageWhoseTestsReadAChangedFile` fails when a test names a tracked file, as a path from its package's directory, that does not select that package when it changes, so a file a test reads is never put outside the Go checks.
A policy with neither field keeps the earlier rule: a file no package owns selects nothing and nothing is listed.

The policy that applies is the default branch's, read from the commit where the branch left it and never from the branch's own copy, so a branch, or a fix commit under a gate, cannot loosen the policy it is checked by.
A change to the file therefore takes effect once it is on the default branch.
A build ignores a field it does not know, so the build a gate has installed keeps reading a policy that a later build extended.
A file that does not parse, or a version the build does not know, makes the change require `full`.
A repository with no policy file has no slow packages.

Every `goblins gate test` run that is not `--plan` leaves a report, and beside it a log of what its commands wrote, in `<user cache folder>\goblins\verify\reports\<project>\`, and its verdict line names the report.
A run at a level narrower than the change requires ends with one more line naming the level the change requires, which CI's run checks.
`GOBLINS_VERIFY_DIR` names a folder to use in place of `<user cache folder>\goblins\verify`, so they go under its `reports\<project>\`.
The report holds the project, the task when a goblin ran it, the commit, where the branch left the default branch, how many files were uncommitted, the level run and the level required with why, the policy and the toolchain, each package with why it was selected, each test run left out with why, and each command with its start, duration, exit code and status: `passed`, `failed`, `over_budget` when a check passed but ran past its budget, or `not_run` when admission or an earlier command failed.
For the test command it also holds what became of each package: `passed`, `failed`, `build_failed` when it did not compile, `no_tests`, or `unfinished` when its tests were still running as the run ended, with its time, how many tests it ran, the tests that failed and the tests that started and never finished, which is how a test that hangs shows.

The tests run as `go test -json`, and the step prints what `go test` prints without `-v`: a compile error as it arrives, each package's summary line as the package ends, and for a package that did not pass what it printed itself and what its failed and unfinished tests printed.
A run in which a package did not pass ends its tests' output by saying which packages, and which tests in them:

```text
goblins gate test: go test did not pass in 2 of 7 package(s):
- example.com/m/internal/spawn: TestSwitchWaits did not finish
- example.com/m/cmd/goblins: TestUpdate, TestUpdate/rollback failed
```

The log holds every line the tests wrote, as `go test -v` prints them, so a passing test's lines and every test's own time are read there.
If the test output or log cannot be written, the test check fails even when `go test` exits zero.
The 20 reports of a project written last are kept, the one a run just wrote always among them, and an older one is removed with its log.
A log with no report belongs to a run still going and stays, until nothing has written to it for 24 hours, when the project's next run removes it.
A run that cannot write its report says so and keeps its verdict: the checks decide the exit code, never the store.
A report is written whole or not at all, and the board reads them: a live task's card shows the newest run of its change, as [docs/native-board.md](native-board.md) describes.

### A project whose hosted CI is its test evidence

A project's machine-local `project.json` can declare its gate Test light and its hosted CI its test evidence:

```json
{
  "light_test": {"memory_mib": 1024, "limit_minutes": 15},
  "test_evidence": "hosted-ci"
}
```

`light_test` is the most memory the Test step is expected to use and the bound it is ended at, both over zero.
`test_evidence` is empty or `hosted-ci`, and `hosted-ci` needs `light_test`, since a Test step that leaves the tests to CI is one that runs light.
A gate's Test step of such a project, which its wrapper's `--gate-run` names, takes a light turn, as [Light runs beside the line](#light-runs-beside-the-line) says, and its command is given `GOBLINS_TEST_EVIDENCE=hosted-ci`, under which `goblins gate test` with no `--level` runs `changed`.
A project without the record, or with neither field, keeps its local-first testing, unchanged.
A record changes only after a build that reads it is installed, and the record of this repository is the machine-local one, not `.no-mistakes.yaml`.

### Taking turns

Every `go vet` and `go test` command waits for a turn at every level: the shared capacity limits concurrent commands, every goblin's and every gate's alike, in the order below.
Any other command stands in the same line behind `goblins gate turn`, described under [Any command in the line](#any-command-in-the-line).
The janitor's prunes of the package caches stand in it too, each as its own `goblins gate turn --wait 30s`, started only while the line has one slot that no run holds or waits for, so a run that takes the line first defers the prune rather than queue it.
Each command releases its turn when execution ends and joins the common line again before its next command.
The default remains one, which keeps heavy commands from competing for the same machine's resources.
Production admission always uses the operating system's user cache folder under `goblins/verify/slots`, regardless of report or process environment redirects.
The shared `capacity.json` setting accepts only `{"capacity":1}` or `{"capacity":2}`; absence means one.
Activating capacity two requires a separately reviewed quiet window with no legacy command executing or queued.
An unreadable or invalid setting, or any nonempty `GOBLINS_VERIFY_SLOTS` process setting, refuses admission.
Only test binaries can use explicitly isolated admission stores.

The line goes by what a run is and then by when it asked, so urgent work does not wait behind everything else.
From the front:

1. A run that has waited two hours, whatever it is, in the order those runs asked.
2. A gate's Test step, which its wrapper's `--gate-run` names, since a whole delivery waits on it.
3. A run of a task with `(priority: production-defect)` on its backlog row.
4. A run of priority 0, of a row that names no priority or one the line does not know, or of no task, which is the place every run had when the line kept only the order they asked in.
5. Runs of priority 1, then 2, then 3, then `ops-root`, the last of the backlog's rows.
6. A low-priority run.

Test steps order among themselves by the same task priorities, and a Test step of any priority goes ahead of every run that is not one.
Runs of one place keep the order they asked in.
`goblins gate turn`, `goblins gate test`, `goblins gate prepush` and `goblins verify` each read the priority on their task's row as they join the line, so a row changed later moves no run that already waits.
A row they cannot read costs a run only its priority: it says so and stands as a run with no task.
A gate's Test step finds its task through its run's recorded managed checkout and the fleet task that owns it, so its command stands by that task's priority in the managed checkout and in the run's base clone alike.
That read only orders the line, so it checks no origin, HEAD or comparison, and a changed local origin costs a step nothing.
A step whose run no fleet task owns, or whose record cannot be read, stands as a Test step with no task and says nothing of it.

The order is only of runs that wait: a run that holds a turn, or is in the moment of taking one, keeps it until it ends, whatever joins the line behind it.

No run starves.
Once a run has waited two hours it moves to the front, where only runs that have waited longer stand ahead of it, and nothing that joins later passes it.
Two hours is a gate's 90 minute turn budget and half an hour more.
Waits reached an hour on a busy morning, so a limit that short would turn a busy line back into the order runs asked in, just when the order matters, and a limit much longer leaves a goblin idle for more of its session than a whole turn is worth.

Every build orders the line by the names of its places and nothing else, so a run an older build started agrees with a newer one on who goes first.
An older build's run takes its place by when it asked and never moves to the front.

A command also waits while either physical or commit availability is under the shared floor.
At capacity one the floor is 4 GiB.
At capacity two it is weighed for the run at the head of the line: what each run holding a slot may still use, its expected memory less what it commits now, then the head's own expected memory and the desktop reserve, and never under 4 GiB.
A run's expected memory is what `goblins gate turn --memory` declares, or 2.5 GiB without it, and a run's card in the line carries it with what the run commits, measured as it runs.
At capacity two a run that fits the memory free goes past a run ahead of it that does not fit for its own expected memory, so a run that expects more than the machine has free holds back no run behind it.
No run passes one that has waited the line's two-hour limit, so a run that expects more than is free still takes its turn within that limit.
An unavailable reading or an hour of floor waiting refuses admission and starts no command.

#### Light runs beside the line

A light run takes a turn of its own beside the slots, however many runs hold them or wait for them, once free physical memory and free commit cover its floor.
Its floor is what each run holding a slot and each light run beside it may still use, its expected memory less what it commits now, then its own expected memory and the desktop reserve.
A heavy run still grows toward its expected memory, so a light run admitted on memory a heavy run is about to use would have that run ended at the desktop reserve.
It is admitted by free memory, never by a count, so any number of light runs go beside the slots while memory covers them.
A light run names its expected memory and its bound, as `goblins gate turn --light --memory 1GiB --execution-limit 15m` does, or takes no turn.
At its bound it is ended with everything it started, and exits 124 with a last line that says the light run outlived its bound.
At capacity two the head of the line weighs what each light run may still use, as it weighs each slot's holder.
A run started inside a light turn, as `goblins gate test` under its wrapper is, runs inside that turn.

While it waits, a run says which run holds the turn, for how long and under what budget, and where it stands in line, as it starts to wait, whenever its place in line changes and once a minute:

```text
goblins gate test: waiting for its turn (3m0s so far): the turn is held by code-goblins at 0123abcd, affected level, in C:\work\code-goblins (pid 4242), for 12m0s of its 1h30m0s budget; 1 run is ahead of this one in line
```

Every take, protection and release of a turn goes through the line's lock, which another run's change holds for milliseconds, and each change waits five seconds for it.
A run that finds the lock busy for all five seconds, as a loaded machine can keep it, as it first checks the line, takes its turn at the head of the line, or protects the turn it took, keeps its place and tries again.
At the head of the line it says `the line's lock is busy` as the lock turns busy.
It tries again within its admission's limit, an hour for `goblins gate test` and `goblins gate prepush`, past which it gives up with why, and for as long as it waits for `goblins gate turn` and `goblins verify`, which set none.
A run that a gate's agent started waits three minutes at most for its turn, as [A gate agent's wait](#any-command-in-the-line) says.
On 2026-10-14 a run that had waited an hour and fourteen minutes reached the head of the line and ended with no turn on one busy change of the lock.

A gate shows a step's output only once the step has ended, so `goblins gate turns` prints the line from outside while the wait lasts: each run that holds a turn, the runs that wait in the order they take their turns, a low-priority run marked, each after the first with why it stands behind the one before it, as `behind run 1: a gate's Test step goes ahead of a goblin's own turn` or `behind run 2: it asked first`, and the memory when the machine is short of it.
At capacity two it also shows what each run is expected to use and what each holder uses now.
When memory and commit, less the about 1 GB a new goblin's session takes, are short of the floor for the line's next run, its last line says the supervisor starts and resumes no goblin by itself, keeping the test slots open, as the scheduler's own line says ([The fleet never idles](contract/dispatch.md#the-fleet-never-idles)).
`goblins gate turns` shows each light run apart from the slots' holders, as `light, beside the slots:`, with its bound and the memory it uses of what it is expected to use.
`goblins gate turns --json` gives the same, less why a run stands behind another, as one JSON object, for the board to read, whose keys stay as they are and are always there, an empty line being empty lists: `capacity`, `is_weighed`, `holding`, `light` and `waiting`, each run with `who`, `pid`, `since`, `budget_seconds`, `now`, `estimate_bytes`, `used_bytes` and `is_low_priority`, `memory` with `available_bytes`, `commit_available_bytes`, `floor_bytes` and `is_short`, and `hold`, which says why no goblin starts or resumes by itself and is empty when one may.
Under a run that holds a turn, `goblins gate turns` prints what the run says it is doing, which the run renews every five seconds while its tests run: how many packages are done, which tests are running and for how long, and the last line of its output.
A new gate run names the task registered for its source worktree, including the owner of an extra worktree, in its turn and report.
Its turn names the task's goblin with it, as `Zora (cg-example)`, when the task's record names one, and its report keeps the id.
The run's recorded repository and branch select that worktree; the shared process's task variable is not used.
If the run or owner cannot be verified, the task stays unnamed and the log explains why.

```text
turn: code-goblins at 0123abcd, affected level, in C:\work\code-goblins (pid 4242), for 12m0s of its 1h30m0s budget
  now: go test: 3 package(s) done; running example.com/m/internal/spawn TestSwitchWaits for 40s; last output: ok  	example.com/m/internal/lock	21.2s
waiting:
1. code-goblins at 89abcdef, affected level, in C:\work\other (pid 5150), for 3m0s
```

Waiting is not execution or a passed check.
It is part of the run's duration and of no check's own time: the verdict line and the report's `queue_seconds` sum the waits before its commands.
Failed admission fails the run, records its reason in `queue_note`, and preserves unstarted required checks as `not_run` with no start or execution duration.

A turn has a budget: 90 minutes at `fast` and `affected`, twice `go test`'s package timeout, and 3 hours at `full`.
Checks that ran past their budget do not pass: the check is recorded `over_budget` and the run fails.
A live or unverifiable process retains custody past its budget until its execution ends or its recorded PID and birth time prove it is gone.
Unreadable or incomplete custody records retain their place regardless of age; the line command reports the uncertainty instead of reporting an empty line.
A cancelled waiter removes only its own exact record, and a release cannot remove a later lease from the same process.
A store failure prevents commands from starting.

### Any command in the line

`goblins gate turn` puts any command in that same line, so one heavy run goes at a time on the machine whoever started it: another repository's test command under its gate, a goblin's own suite, or a run one of the Overlord's sessions starts.
`goblins gate test`, `goblins gate prepush` and `goblins gate turn` wait through the same admission code in the same store, so a run of any of them waits for a run of the others.

```text
goblins gate turn [--as <name>] [--wait <duration>] [--execution-limit <duration>] [--memory <size>] [--low-priority] [--light] [--detach <log-file>] -- <program> [<argument>...]
```

Everything before `--` is `goblins`'s, and everything after it is the command's, its own flags and a `--` of its own included.
`goblins` starts `<program>` as a process with exactly those arguments, unparsed and with no shell of its own in front, so a command that needs a shell names one: `goblins gate turn -- cmd.exe /c "npm test && npm run lint"`.
`--as <name>` names the run to whoever reads the line.
`--wait <duration>` bounds the wait for the turn, as `--wait 30s`: a run that takes no turn within it runs nothing and exits 125 with a last line that says so, and once it has its turn the wait no longer counts.
`--execution-limit <duration>`, as `--execution-limit 5m`, starts one deadline after admission, before startup, and keeps that deadline through the payload, descendants left by an exited parent, and finalization.
It works with or without `--detach`, independently of `--wait`; both accept only positive durations, and an invalid, zero, negative or missing value starts nothing.
Without an execution limit, the existing unlimited execution and ten-second descendant linger remain unchanged.
A timeout exits 124 even if the command itself exited zero, names the execution record on standard error, and records `verify.ErrLimit` as a failure.
A timeout before the command launches records `not_started`, with no invented command exit code.
Cleanup still uses the existing measured job owner and spares machine services, with five seconds for the job cleanup proof, five seconds for the command wait, bounded custody release, and a final retry bounded to ten seconds.
Those cleanup and receipt operations can extend elapsed time past the execution deadline, and native Windows calls must return before their cancellation can be observed; the limit is not a guarantee that the wrapper exits at that exact instant.
A cleanup that cannot be confirmed keeps its custody and reports uncertainty; later confirmed cleanup never makes the timed-out run pass.
`--low-priority` has the run wait at the lowest priority: it takes its turn only while no ordinary run waits, and an ordinary run that joins the line after it still goes first, unless the low-priority run is already taking its turn or has waited the line's two hours, as the desktop's CI jobs wait, and `goblins gate turns` marks it.
`--memory <size>`, as `--memory 7GiB` or `--memory 512MiB`, is the most memory the run is expected to use, which a line of two slots weighs before it starts the run beside another, as [Taking turns](#taking-turns) says.
Without it a run is expected to use 2.5 GiB.
`--light` has the run take a light turn beside the slots, which needs `--memory` and `--execution-limit`, as [Light runs beside the line](#light-runs-beside-the-line) says.
A caller such as a gate knows its repository, branch and run, which the command and the folder do not say, so it should give them: `--as "PrecisionDocs feat/x, run 01M4FG7F"`.
A run is named `<name>: <command>` with `--as` and `<command> in <folder>` without it.
`goblins` adds `, task <Goblin> (<id>)` when the run is a fleet task's: a goblin's own run by its terminal, and a gate's run by the task registered for the run's source worktree, as for `goblins gate test`.
When the run ends in a Codex or pi goblin's own terminal and worktree while the goblin sits idle at its prompt, it types one `CEO:` line into that goblin, as `goblins send` would, naming the run, its exit code and how long it took, since neither harness starts a turn when a background run ends; `goblins gate prepush` does the same, and an interrupted run tells nothing.
A goblin in a turn is not told, since one that waited on the run there has read how it ended.
If the line cannot be typed, the run says so in one line on standard error, and its exit code stays the command's.
A gate the goblin started inherits its terminal's environment, but runs its steps in the gate's own worktree, so a gate's run types nothing.

**Not a gate's wait.**
A command that waits on a no-mistakes gate takes no turn.
`no-mistakes axi run`, `axi respond` and `axi answer`, and `goblins pipeline run` and `goblins pipeline respond`, which drive them, wait for the gate's next decision, through every agent step before it.
The gate's test command takes its own turn, since the fork stands the repository's `commands.test` behind `goblins gate turn`, as **A gate behind it** below says, so a turn held while a gate waits orders no heavy run and keeps every other run in line through the gate's agents' work.
On 2026-10-13 two goblins' fix rounds held the turn that way while up to ten runs waited.
The run exits 125 and says what to run instead: the gate's command without `goblins gate turn`, and any heavy command that came with it in a `goblins gate turn` of its own.
It names the command it was given on one line, so its last line is still its own:

```text
goblins gate turn: this run takes no turn: no-mistakes axi respond waits on a gate, whose test command takes its own turn, so a turn held while it waits only keeps every other run in line through the gate's agents' work
goblins gate turn: run the gate's command without goblins gate turn, and any heavy command beside it in a goblins gate turn of its own: no-mistakes axi respond --action fix --wait 10m
```

`goblins` reads the words of every argument, so a shell's command string, as `cmd.exe /c` and `powershell -Command` take, is read too.
A word ends at a space, a quote, `&`, `|`, `;`, `(`, `)`, `<` or `>`, and a program is known by its file name without `.exe`, in any case.
A refused run tells its goblin that it ended, as a run that did not start does.
A script the command runs is not read.

**In the background.**
`--detach <log-file>` starts the same run, with its name, wait and execution limit, as a process of its own and returns at once, printing its pid on standard output.
The run starts the way the supervisor is started: in a console of its own with no window, which its command shares, so nothing of it ever shows on the desktop, in a process group of its own, and out of its caller's job where the job allows it, so it outlives its caller.
Its standard output and error are appended to `<log-file>`, its pid is written to `<log-file>.pid` for its goblin to end it by, and its last line in the log is `goblins gate turn: the detached run ended with exit <code> after <time>`.
A run that cannot be started detached starts nothing and exits 125.
Since no harness waits on a detached run, its end is told to the goblin of any harness that sits idle at its prompt, Claude Code included.

**What it waits for.**
It waits while another run holds the turn, and at the head of the line while free physical memory or free commit is under the floor.
It sets no limit of its own on either wait, but for a run that a gate's agent started, as below.
Where `goblins gate test` refuses a run after an hour of memory under the floor, `goblins gate turn` waits for as long as memory is short unless `--wait` bounds admission.
Its execution limit never counts that wait.
The wait counts in the wrapper's wall time, so a caller that budgets its test command should start the budget at `took its turn`, or allow for the wait.

**A gate agent's wait.**
A gate ends an agent whose invocation ran past its budget, and it counts the agent's wait for a turn in that budget, since it cannot tell a run that waits in line from one that hangs.
So a run that a gate's agent started, which the `NO_MISTAKES_GATE` the gate sets in the agent's environment says, waits three minutes at most for its turn, behind other runs, for memory and for the line's lock alike, in `goblins gate turn`, `goblins gate test` and `goblins gate prepush`.
Three minutes outlasts a short check ahead and a dip of memory under the floor, and is a tenth of the 30 minutes a gate gives an agent unless its machine says otherwise.
A shorter `--wait` still ends the wait first, a longer one does not lengthen it, and a turn taken within it is never cut.
The three minutes are the run's whole wait and not each turn's: `goblins gate test` takes a turn for each of its checks, so a check waits only what the checks before it left of them, and a check with none left takes a turn that is free and otherwise leaves the line at once, with the checks before it run and no other started.
`goblins gate lint` takes a turn for each of its checks the same way, so what is said here of `goblins gate test` holds for it.
A run that takes no turn in that time starts nothing and leaves the line: `goblins gate turn` exits 125 and `goblins gate test` and `goblins gate prepush` exit 1, each with a line that tells the agent to go on without the run:

```text
goblins gate turn: this run takes no turn: no turn came within 3m0s, which is as long as a run a gate's agent starts waits in line, since its gate counts the wait against the agent's invocation budget. Go on without this run and start nothing heavy outside the line: commit the work, and the gate runs the project's checks in a turn of its own
```

The agent loses nothing by it, since the gate's Test step runs the project's checks in a turn of its own, which no agent's budget counts.
The gate's own Test step, which its wrapper's `--gate-run` names, has no such limit whatever its environment says, and neither has a goblin's own run or any other.
On 2026-11-02 two review fixers each spent their whole hour in line behind six heavy steps on a machine short of memory, and were ended with their repairs written and not committed, which failed both runs in Review (issue 1932).

**What it prints.**
Every line of its own goes to standard error and starts with `goblins gate turn: `.
With the turn free and memory above the floor it writes no line at all.
As it starts to wait, whenever its place in line changes and once a minute, it says which run holds the turn and for how long, or what memory the machine is short of, and where it stands:

```text
goblins gate turn: waiting for its turn (3m0s so far): the turn is held by PrecisionDocs feat/x, run 01M4FG7F: cmd.exe /c npm test, task Zora (cg-example) (pid 4242), for 12m0s; 1 run is ahead of this one in line
goblins gate turn: waiting for its turn (40s so far): 3.1 GB of memory is available and the floor is 4.0 GB; this run is next in line
```

Once, when it takes a turn it waited a second or more for:

```text
goblins gate turn: took its turn after 3m0s
```

Then comes the command's own output, and after it only `goblins`'s lines below: what the run ended or left running, why it ended its command, and what it could not clean up or prune.
A gate shows a step's output only once the step has ended, so while a run waits the line is read from outside with `goblins gate turns`.

**What it leaves untouched.**

- Standard output. `goblins` writes nothing to it, and the command inherits the handle.
- Standard error. The command inherits the handle, after the lines above.
- Standard input, the working folder and the environment. All inherited, with one variable added, `GOBLINS_VERIFY_TURN`, described below.
- The exit code. It is the command's own, whatever it is.
- The command's limits. `goblins` adds no timeout and raises none.

**When no command runs.**

| Exit code | Last line on standard error | What happened |
| --- | --- | --- |
| 125 | `goblins gate turn: this run takes no turn: <reason>`, `goblins gate turn: run it without goblins gate turn: <command>`, or `goblins gate turn: the run ended its command, since free memory or commit stayed under the desktop reserve: <detail>` | The run gave no result of its command's. Either the line ended the command for the machine's memory, or the command was never started. The line's store or the machine's memory could not be read, the run's record could not be begun, no turn came within the run's `--wait` or within the three minutes a run that a gate's agent started waits, the run was interrupted before its command started, standard output or error was no file, as a process's always is, the command waits on a gate, which the line before the last says, or the arguments named no command, which also prints the usage. |
| 126 | `goblins gate turn: the command did not start: <error>` | A required environment binding was refused before admission or once the turn was taken, or a turn was taken and given back and `<program>` could not be started, or the run's custody of it could not be recorded first. |

Neither is a test result, so a caller reads 125 as "the machine gave this run no turn" and never asks an agent to repair tests that did not run.
A command that itself exits 125 or 126 is told apart by the last line, which then is not `goblins gate turn`'s unless the run named what its command left running or why it ended the command.

**The turn.**
The turn is held from before the command starts until the command and what it left running have ended, and is given back whatever its exit code.
A wrapper that is ended leaves a turn the next run takes, since the holder is recorded by process id and start time, so no lock a dead run left ever stops the machine's tests.
The next run first proves the dead run's job ended, which takes a few seconds, and one that cannot prove it takes no turn and exits 125 until it can.
The command runs in a Windows job of its own that every process it starts joins and none can leave, whatever becomes of its parent, and the job ends with the wrapper, so a wrapper ended by force ends its command with everything the command started, and nothing the command started runs on outside the turn but a machine service, which the run leaves running by design, as below.
On Ctrl+C or Ctrl+Break while it waits, `goblins` leaves the line and exits 125.
While the command runs, `goblins` keeps the turn through the signal and exits with the command's code: the command shares the console, receives the same signal and answers for itself.
Once the command's own process has exited, interrupted or not, what it left running may go on for ten seconds, or until an explicit execution limit expires first, after which `goblins` ends it.
The normal linger names each process it ended.
A machine service the command started, Docker Desktop with what it runs, the no-mistakes daemon with its gate agents or the Scrawl server, serves the whole machine, so it is left running and named:

```text
goblins gate turn: its command left node.exe pid 5150 running after it exited, which the run ended
goblins gate turn: its command started Docker Desktop.exe pid 6120, which serves the whole machine, so the run left it running
```

**The measured run.**
The command runs as a measured execution, as each `goblins verify` command does: its record holds its wait, its CPU time, its memory, its processes and how it ended, and is kept in the report store's `executions` folder with the others and pruned with them after each run.
It holds none of the command's output, which goes to `goblins`'s own handles and is its caller's to keep.
While free physical memory or free commit stays under the desktop reserve for ten seconds, 2 GiB of each unless the store's `reserve.json` says otherwise, `goblins` ends the command with everything it started.
The reading is the whole machine's, so what the check ends is the one run the line lets use the machine's spare memory, whatever else holds memory then.
What the command was ended at is no result of its own, so `goblins` exits 125 with a last line that says why, which a gate parks the run on rather than ask for a repair:

```text
goblins gate turn: the run ended its command, since free memory or commit stayed under the desktop reserve: verify: execution 4KQ3NAWZPXDMR7YHTJ2CE6LFBU failed (interrupted): verify: reserve pressure for 10s: 1.2 GB of memory and 9.8 GB of commit available, under the desktop reserve of 2.0 GB and 2.0 GB
```

A command that exits by itself keeps its own exit code: once its own process has exited, memory under the reserve for ten seconds ends what it left, as the linger's end does, and fails nothing.
A running sample that cannot be written ends no command, since a sample is progress and the final measurement is the evidence, and a run interrupted after it took its turn but before its command started ends there with 125, since its command could never hear the interrupt.
The record's commit reading is the documented one for the process that reads it, which is the machine's free commit unless that process sits in a job with a commit limit, as no `goblins` job is.
Claude goblins launched since this change start with Claude Code's own low-memory ending of idle background shells turned off, since it ended a goblin's queued `goblins gate turn` and lost its place in the line, while this check ends the run the line let use the memory.

**A command that takes turns itself.**
This repository's test command is `goblins gate test`, which takes a turn for each of its checks, and behind the wrapper it would wait for the turn its own wrapper holds, for good.
So the wrapper gives its command `GOBLINS_VERIFY_TURN`, which names the turn it holds: the slot, the holder's process id and the moment it was taken.
A `goblins gate test`, a `goblins gate prepush` or a nested `goblins gate turn` that finds it, with that turn still held by that same live process, runs inside it: it takes no turn, waits for none, and says what it is doing beside the holder's turn.
Any other value, such as one a process kept from a run that has ended, is ignored, and the run joins the line as every run does.

**Executor accounting.**
`internal/execx.Tree.Usage` reads cumulative CPU time, current and peak private commitment, active and lifetime process counts from the Windows job, including descendants whose parents exited.
It samples the private working set separately, which is neither commitment nor a measured lifetime peak, and counts apart the members this process may not read, as a sandboxed browser's, which the sample leaves out.
A member it may not open counts as still running, since nothing shows it has exited.
These reads are observations, not resource reservations or enforced limits.
An unreadable job or process measurement returns an error, and platforms without whole-tree support report that accounting is unavailable.
Windows lists no process that a job nested in a job is ending, from the moment that end begins until the process is done, and counts it either no more or for as long as its threads take to end, so `internal/proc.JobProcessIDs` returns the list with how many more members the job counts than it lists.
A headless Chrome puts most of its processes each in a job of its own, which ends them once the browser has ended: such a process left its browser's job's list with up to 21 threads not yet ended and was done up to 2.5 s later, while the job counted it beyond the list for up to 474 ms or not at all, and a browser run pinned to four busy processors kept the count above the list for over 250 ms, which once ended its run (issues 1025 and 1066).
A process that exits, ends itself, or is ended by its handle or by the job it runs in stays listed until it has ended.
A sample takes the list as it is.
An end that spares a machine service goes by what the job holds to show that nothing else still runs, so it also reads the machine's processes for the job's members the list leaves out: `internal/proc.EndJob`, which `End`, `EndAndWait`, a terminal's close and a browser login's end use, is done only once those have ended too, within the end's own deadline, and a goblin's stop ends them as it ends any other process in its terminal's job.
It reads the machine again only once each process it ended has exited: on a machine whose cores other work fills those exits take a while and one read can take seconds, so a read begun before them found them still there and the next began past the end's deadline, which then named them as outliving it (issue 1275).
`EndAndWait` also lets the job's handle close only once the job counts nothing beyond the list.
Verification code can use `TerminateAndWait` to terminate every job member and close its handle only after Windows reports zero active processes, or `EndAndWait` to do the same while sparing a machine service the command started.
That cleanup is bounded to five seconds or the caller's shorter deadline, and a failed proof retains the handle for a retry.
Recovery uses `TerminateAndWait`, which spares nothing.
The generic executor's `End` and `Release` contracts are unchanged by these additive APIs, and `goblins gate turn` runs its command through the measured executor described below.
This accounting alone does not establish trusted execution evidence or authorize a GitHub check.
`internal/verify.Monitor` uses those measurements to wait for the whole job, retain cumulative and sampled peaks separately, and interrupt on unreadable measurements, failed evidence writes, or physical or commit headroom below its reserve.
Its reserve is the memory a run leaves the desktop, a cancellation threshold, not a reservation or a kernel limit.
It sits under the 4 GiB admission floor, since a run admitted at the floor uses the memory between the two, so a reserve at the floor would cancel nearly every run that used its headroom.
The executor reads it from `reserve.json` in the admission store, as `{"physical_gib": 2, "commit_gib": 2}`, and keeps 2 GiB of each when the file is absent.
Each value must be at least half a gigabyte and under the admission floor, and any other content, such as an unknown key, a missing dimension or trailing data, refuses the run before it starts.
A caller can raise the reserve but never lower it below the store's.
Free memory is the whole machine's, so one sample under the reserve can be another program's spike, and the run is cancelled only once free memory stays under the reserve for ten seconds.
Once the command's own process has exited, what it left running may run on for a linger of ten seconds, after which the run ends it and records each process by name and pid as a leftover, and `goblins verify` names them on standard error.
The run keeps the command's own result, since tools such as build servers and browser bridges leave helpers behind on purpose, and a later certification can still require a run with none.
Only a process still running counts, since Windows can count one that has exited in its job for a moment after.
A process a nested `goblins gate turn` lets go of when its command ends is still in the executor's job, so it is ended and named the same way.
A machine service the command started, Docker Desktop with what it runs, the no-mistakes daemon with its gate agents or the Scrawl server, serves the whole machine, so the run's end leaves it running, as every teardown of a goblin does, and records it apart from the leftovers it ended, and `goblins verify` names it on standard error.
The run's cleanup is confirmed once nothing but such a service runs in its job, by the job's own list of its processes, where a process the run may not open counts as running.
Only then does the job stop ending what it holds when its handle closes, so a controller that dies first, or an end that fails, still ends everything in it.
A recovery after a controller died spares nothing, since that controller's job ended everything in it when its handle closed.
The command's output to its logs is limited to 1 GiB together, past which the run is interrupted, and a running sample is written to the record every ten seconds and the final measurement always.
A run whose command writes to its caller's own output, as `goblins gate turn`'s does, keeps no logs and has no such limit.
Until the command's process exits the monitor still waits for the whole job, and it samples again as soon as that process exits rather than at its next poll.
`RunWithin` bounds a command from the moment it starts, so its wait in line never counts against that limit, and the limit stops once the command's own process exits, so what it left is judged by the linger alone.
The executor's separate `ExecutionLimit`, used by `goblins gate turn --execution-limit`, begins before startup and remains active through finalization, using the same absolute deadline throughout without changing `RunWithin`'s command-only limit.
A command its limit ends fails with `verify.ErrLimit` and an interrupted record, which a tier records as timed out, while an internal wait that ran out of time is a failure but no timeout.
A failed cleanup returns `cleanup_uncertain`, so its caller must retain admission custody, and successful cleanup does not turn an interrupted run into a completed one.
`internal/verify.Executor` composes admission, per-command worker custody, job measurement and durable lifecycle records on Windows.
Records under the report store's `executions` folder separate queue time from execution time, preserve the command's actual exit code when available, and require complete measurements and cleanup before recording a pass.
The record names its complete stdout and stderr logs, while the returned command result holds at most the last 4 MiB of each stream.
Input is an inherited file handle, so an arbitrary reader cannot leave a blocked I/O pump behind after the command exits, and a command given none reads an empty input.
Interruption requests Ctrl+Break only for the command's own process group, then performs bounded whole-job cleanup even when no shared console can deliver that request.
A command that shares its caller's console group, as `goblins gate turn`'s does, is sent no Ctrl+Break, since that event reaches only a whole group, its caller's included: an interrupt reaches it from the console itself, and pressure ends its job at once.
Each custody record prevents slot release until its execution confirms cleanup, including when a parent wrapper tries to release a nested command's turn.
A custody record names its execution's Windows job and the process that controls the execution, and it is written before the job or any worker exists.
The job is named `Global\goblins-verify-` followed by the execution's random run ID, in the machine's global namespace so a run in any logon session finds it, its creation refuses a name that already exists, and no child inherits its handle.
When that controller is verifiably gone, the next admission, or the release of the turn it ran inside, recovers its custody instead of refusing every later run.
Recovery ends whatever still runs in the job and waits until Windows counts it empty, or finds that no job of that name exists, which means nothing runs in it.
It still refuses while a process the controller started after recording custody runs outside the job, as a worker created suspended and never assigned to its job would.
A worker the job's end has just ended, or a controller still exiting, can be listed for a moment, so recovery reads the machine's processes again for a second and a half before it names one.
A dead controller alone is never taken as proof that its workers are gone.
A controller whose state cannot be read, a job this process cannot open, a record that names another run's job, or a record that changed during recovery leaves custody in place and refuses admission.
Each refusal names the custody record's file, and one that names a worker outside the job says to end that process once it is known to be the execution's.
A recovered execution's pending record is marked failed and interrupted with a note naming the recovery, and one its controller finished keeps its result, so recovery never produces a pass.
A record recovery cannot read or write stays as it was and never keeps the line shut, since a pending or unreadable record never passes.
An unreadable custody record still refuses further admission rather than guessing that its workers are gone.
The executor retains failed cleanup jobs for its owner's bounded `RetryCleanup` calls and keeps a confirmed cleanup proof if its evidence write or custody removal needs another attempt.
Slot release reports errors and stays retryable, and an executor retains final evidence-write failures even after workers have ended.
Concurrent recovery waits respect the waiting caller's deadline without holding the executor's registry mutex over job or filesystem operations.
The deadline also bounds custody-lock, file-open and final evidence-rename contention retries.
A failed mutation-lock release retains its exact lease identity and tries again by itself every second until the lock is given back, without waiting for a later change of the same process, and cannot reclaim a changed or foreign live owner.
Stale lease removal claims the record against writes and replacement, validates its identity while claimed, and deletes through that same Windows handle.
Mutation-lease creation excludes readers until the writer closes, so contenders cannot mistake an in-progress publication for a corrupt record.
An interrupted publication that leaves an incomplete record still refuses admission.
Recovery preserves failure status, and an executor with pending cleanup must not be discarded.
`goblins verify` and `goblins security` run each command of a tier through this executor, as docs/project-runtime.md says.
They have it read the source the command runs on, once its turn is taken and before it starts and again once it has ended, and the record keeps both reads under `source`.
`goblins gate test` reads the source of each of its checks the same way and keeps the reads in its report.
A read is the commit checked out, its own tree, and the git tree of the files git does not ignore as they were on disk, whatever the worktree's own index held.
So a commit of exactly those files has that tree, and a worktree that matches its commit has the commit's own.
The read stages them into an index and an object store of its own, which it removes after, so it leaves the worktree's index as it was and writes nothing into the repository.
That index starts from a copy of the worktree's own, so git opens only the files whose size or times changed since the worktree's index last saw them, where a fresh index had it open and hash each of a worktree's thousands of files, which ran past the read's limit on a loaded machine (issue 1615).
A worktree index that marks a file assume-unchanged or skip-worktree would hide that file's change, and git can refuse to reset that index to the commit, so the read then starts from the commit alone.
It inherits none of the git variables that name another repository, index or object store, as a git hook or a gate daemon sets them, and it sets aside a file system monitor and `core.ignoreStat`.
It is incomplete when the tree does not hold all of those files as they were: a submodule with changes of its own, which a tree records only by the commit it is at, or one not checked out, a file git leaves unstaged, as a sparse checkout leaves the files it keeps off the disk, or a file that changed while the tree was read.
The tree's content is git's, after line-ending conversion and clean filters, with this machine's `core.filemode=false` a file's mode is its commit's, and on this case-insensitive disk a rename that changes only a name's case keeps the commit's spelling, so a check that depends on line endings, modes or the case of a name can differ from what the tree says.
Ignored files are inputs too, such as installed dependencies or the built board `internal/boardweb` embeds, and are no part of a source, so a source is not all of what a check ran on.
A command tested one source only when both reads are the same complete source, which says only that the two reads agree: a file changed and changed back between them goes unseen.
One whose reads differed, could not be read, or were incomplete tested no one source, so it fails whatever its exit code, in a tier and in the gate step alike, and its failure says why.
A read is held within the check's turn, outside its time against a budget, and is bounded at two minutes, at which it ends with what its git started.
After each tier, and after each `goblins gate turn` run, the executions store keeps the newest 100 passes, 50 failures and 20 runs that never started, each kept apart, so a burst of one never evicts the evidence of another, and apart from them the newest 200 runs that kept no logs, as `goblins gate turn`'s, whose output their caller keeps.
What it counts fits 2 GiB with its logs, the oldest passes going first, then runs that never started, then failures, but never past the newest pass and the newest five failures, so a run whose own logs overran the budget keeps its evidence.
A record whose custody is still held, one still pending, and one it cannot read or that a newer goblins wrote, while younger than a week, is never removed or counted, so the 2 GiB covers only what it counts.
A record a kept pass rests on, while that pass may still be taken again within its week, is counted and never removed, the newest passes first and only within half the 2 GiB, so passes never crowd out the evidence of failures, and a pass past that half is taken again only while its records outlive the budget.
A pass that cannot be read stops the round from removing anything, since it may rest on any record, and `goblins verify` prunes once its tier's own pass is kept, so that pass pins its records first.
A log whose record is gone is removed once it is a week old, and a record goes only once its logs are gone.
It removes only plain files named as the store names them, refuses an executions folder that is a junction or link, and a removal that fails is named on standard error, tried again after the next tier, and never fails the tier.
These same-user records are advisory, and the measured executor is not yet wired into a protected publisher.

`ValidatePreparedGeneration` checks an explicitly owned artifact generation against an independently supplied preparation identity and required file inventory.
It requires complete preparation evidence and matching sizes and SHA-256 hashes, so a revision directory or ready marker alone cannot hide missing runtime files.
Incomplete or interrupted preparation, unreadable evidence or files, path traversal, symlinks and reparse points are refused.
It only reads the named generation and does not discover, reinstall, repair or remove shared caches.
Same-user mutable generations remain advisory because their contents can change during or after validation.
Trusted reuse still requires a protected immutable generation, trusted inventory and a successful runtime initialization probe under admission.

**Not in the wrapper.**
It has no budget and no timeout, no disk floor, which `goblins gate test` keeps for itself, and no report file, since its caller keeps the log.

**A gate behind it.**
A gate puts every run's test command behind the line, with no repository naming it, by starting the command behind four words: `goblins gate turn --`, then exactly the program and arguments it starts today.
On this machine that is `goblins gate turn -- cmd.exe /c <command string>`, started as an argument list with no shell in front, with the run's `--gate-run` and `--test-comparison-sha` identity and its `--as` name before the `--`, as the setting below gives them.
The words go in front of the whole command as a list and are never joined to the command string, where `a && b` would put only `a` behind the turn.
This is the contract the no-mistakes fork's wrapper setting is built against.
The fork's side, as task cg-gate-fork-minutes built it, is a setting in the machine's `config.yaml` in the gate's home (`NM_HOME`, else `~/.no-mistakes`), on since the installed `goblins` has `goblins gate turn`, as an older `goblins` refuses `gate turn` with exit 2, which a gate reads as a failed test command:

```yaml
test_command_wrapper:
  command: ["C:\\dev\\code-goblins\\bin\\goblins.exe", "gate", "turn", "--gate-run", "{run}", "--test-comparison-sha", "{test_comparison_sha}", "--as", "{repo} {branch}, run {run_short}", "--"]
  refusal_prefix: "goblins gate turn: "
```

The fork fills `{repo}`, `{branch}`, `{run}` and `{run_short}` once, and `{test_comparison_sha}` with the comparison its Test step selected, then starts these arguments with no shell in front:

```text
C:\dev\code-goblins\bin\goblins.exe  gate  turn  --gate-run  <full run id>  --test-comparison-sha  <resolved Test comparison SHA>  --as  "PrecisionDocs feat/x, run 01M4FG7F"  --  C:\WINDOWS\system32\cmd.exe  /c  <the command string, as one argument>
```

The full path to `goblins.exe` keeps the gate daemon's `PATH` from choosing which `goblins` runs.
It stands every run's `commands.test` behind the line, each machine-local additional test check, and the re-run of `commands.test` on the base commit, and not prepare, lint, format or a repository gate.
An exit of 125 or 126 whose last line that is not empty starts with `refusal_prefix`, or a wrapper that cannot be started at all, parks the test step with one `test-command-no-turn` finding: no fix agent is asked, fix runs the test command again, and approve goes on without the baseline.
So this repository's own `goblins gate test` runs behind `goblins gate turn` under a gate, inside the turn its wrapper holds.

A repository whose `.no-mistakes.yaml` names no `commands.test` gives the fork nothing to wrap: its test step logs `no test command configured` and asks the test agent to run the tests, which it does outside the line.
So every gated repository names its suite there, on its default branch.
The string runs as `cmd.exe /c <string>`, so it is in `cmd.exe` syntax, with `&&` between commands.
A gate's worktree holds tracked files only, so `commands.prepare` installs what the suite needs, outside the line, before the baseline and first Test agent invocation.
This repository configures:

```yaml
commands:
  prepare: "goblins gate prepare"
  test: "goblins gate test"
```

`goblins gate prepare` reads the existing scoped change plan and runs `npm ci` in the validation worktree's own `frontend` directory only when the plan contains a path under `frontend/`, including source and lockfile changes.
Go-only and documentation-only changes install nothing.
An existing `node_modules` directory does not authorize reuse: every frontend-affected preparation runs the locked install, without sharing materialized dependencies between worktrees or adding a receipt or cache.
On Windows it invokes npm through `cmd.exe /d /c npm ci`, so standard `npm.cmd` installations work.
An unreadable plan fails preparation before an installer starts; missing tooling and installer failures also fail preparation, with no fallback to an unlocked install.
Missing or invalid lockfiles are rejected by `npm ci`, not independently validated by this command.
The command reports the locked install's elapsed time; its isolated regression fixtures are in `cmd/goblins/gate_prepare_test.go`.
Activation requires this configuration on the default branch, from which the gate trusts it, and a reviewed installed goblins build supporting both `goblins gate prepare` and `goblins gate test`.

An existing verified Python environment can be required by the project's machine-local `data/projects/<checkout folder>/project.json`:

```json
"test_environment": { "python_version": "3.12.10" }
```

This binding selects only `.venv` in the registered source task's primary worktree.
The wrapper's `--gate-run {run}` argument identifies the full native run in the daemon's own home.
Its `--test-comparison-sha {test_comparison_sha}` argument carries the comparison already selected by native Test, as one full nonzero 40- or 64-character lowercase hexadecimal object ID.
The consumer uses that value directly and never resolves another base or reads `runs.base_sha`, which records the previous pushed ref and may be zero.
Without `--gate-run`, `goblins gate turn` keeps the command's existing environment and does not infer a native binding from directory names.
The wrapper verifies the gate's project and branch through the native run database, finds the owning fleet task through the source repository's worktree list, and checks the task's recorded project and worktree against the home's allowed locations.
The managed checkout must have the run's recorded HEAD and canonical native repository.
The command must run there or in a separate base clone whose top level, local origin and HEAD match the managed checkout and the supplied native Test comparison.
It never selects an environment from a client variable or another project's record.
The interpreter must already exist, and an isolated Python probe must report that executable, the selected environment prefix, a distinct existing base prefix and the exact configured version.
The probe has two minutes, since a machine short of memory can take tens of seconds to start a correct interpreter whose files were paged out.
A probe that runs out of that time is refused as a timeout that names its limit, not as a wrong environment, and a later run or reattach can succeed once memory is free.
The environment directory must resolve inside the registered source worktree.
Python environments with system site packages are supported.

`goblins pipeline run <task> --intent-file <file>` checks a required binding before starting or reattaching to a gate.
The daemon's existing Test wrapper checks it before admission and again after waiting, rejects a changed source or environment, then starts the original program and arguments with `UV_PROJECT_ENVIRONMENT` and `UV_PYTHON` set to the verified paths, `UV_NO_SYNC=1`, `UV_OFFLINE=1` and `UV_PYTHON_DOWNLOADS=never`.
These are fixed binding settings, with no arbitrary client environment forwarding and no shared daemon environment change.
The configured repository command and its actual exit code remain unchanged.
Use this binding for a trusted `uv run` command that must reuse an existing environment without dependency resolution, installation or interpreter downloads; it does not constrain an arbitrary repository command that explicitly overrides those settings.
No environment is created, synchronized or repaired by the binding.
An unavailable, invalid or mismatched required binding refuses execution with exit 126 and an actionable explanation; the native wrapper parks that as an unexecuted command, never a passing baseline.
Repair and verify the existing environment or its project setting, then request the same Test command again through `goblins pipeline respond <task> --action fix --findings test-command-no-turn`.
`goblins pipeline respond` refuses approval of a recorded required-environment refusal, even with `--accept` or `--reason` and after the project setting changes; a successful Test retry is required.
Projects with no `test_environment` keep their existing runner environment: the wrapper reads only their project and recorded checkout from the native run and checks no checkout provenance, so a changed local origin never stops their Test or costs it its place in line.
The machine must have the `test_command_wrapper` configuration above pointing at the reviewed goblins build, including both native identity arguments; the managed preflight refuses a required binding when either is absent or uses another placeholder.
The native build must support the `{test_comparison_sha}` contract delivered by [native pull request 46](https://github.com/fpresta0607/no-mistakes/pull/46).
The CEO activates that native build and the reviewed wrapper setting together through its normal idle configuration window.
The isolated real-daemon acceptance fixture is `tests/acceptance/test_environment.py`.
It consumes source-bound native and consumer artifact receipts and an unused private proof root, uses an existing Python 3.12.10 interpreter and fake command sentinels, and retains its complete native history and proof.

What the test command leaves running is ended ten seconds after it exits, and after a baseline that passed the test agent still gathers live evidence, outside the line.

### A desktop CI job in the line

A machine with the desktop CI runner installed (`cg-ci-runner`, a Windows service the Supreme Overlord installs from a signed release) runs one GitHub Actions job at a time for one repository, in a Hyper-V VM put back to a clean checkpoint before every job.
The service holds the repository's GitHub App key and drives the VM, and it runs a job only when the fleet asks, on its named pipe, which only the fleet's Windows user, SYSTEM and Administrators may open.
Every 30 seconds the watcher asks it whether a job of its labels waits, and starts one turn for it, at the lowest priority, so it goes ahead of a run that waits only once it has waited the line's two hours, and expected to use 7 GiB, the VM's 6 GB at most with its Hyper-V worker and the job's own process, which the line weighs since the VM's memory is the worker's, outside the turn:

```
goblins gate turn --low-priority --memory 7GiB --as "desktop CI: <job> of run <id>" --detach <state>\ci-runner\job-<run>-<time>.log -- <this ceo> ci-runner job
```

No other job's turn starts while that run lives.
`goblins ci-runner job` holds the pipe open for the length of the job, so the turn's end, however it comes, ends the job, and the service then turns the VM off, puts it back to clean and deletes the job's runner.
It exits 0 when the job ran, 3 when no job came for the runner and 1 otherwise.
A machine with no runner installed is asked, finds no pipe, and says nothing, and trouble with a runner that is installed is said once an hour in `<state>\ci-runner\watch.log`.
`goblins ci-runner status` says whether a job runs and how the last one ended.

A hosted job moves to the desktop only once its desktop copy has matched it run after run, and the record of that is the parity record.
The copies run in a mirror workflow of the repository's own, `desktop-mirror.yml`, which a CI run's completion starts and which reports on the default branch's head, never on a pull request's, so no copy waiting for the desktop holds a merge.
Its run is titled `Desktop mirror of CI run <id>`, and its job `desktop-<name>` copies the job `<name>` of that CI run on the very merge the hosted job tested: the hosted job's log names it, `HEAD is now at <sha> Merge <head> into <base>`, and the copy's, `desktop mirror: Merge <head> into <base>`.
`goblins ci-runner parity --repo <owner/name>` reads the mirror's newest finished runs and adds each pair it does not hold yet to `<state>\ci-runner\parity\<owner>-<name>.json`: both conclusions, pytest's counts from each log's summary line, the merge each tested, their times, and from the watcher's log of the desktop job's turn, found by the runner it named, how long the turn waited and what held the line.
A pair matches when both copies tested the same merge, concluded the same and counted the same, and the desktop's run was not lost.
It prints, for each job, how many runs in a row matched up to the newest, how many desktop runs were lost and the newest difference, and once 20 in a row matched, that the hosted job may retire, which stays the Supreme Overlord's decision.

## Before a push

`goblins gate prepush` picks project checks before publication under the [selected validation owner](contract/delivery.md#delivery), so that CI is not the first to run a test the change broke.
From 2026-09-05 to 2026-10-10, 164 tests failed on pull request runs of the `go` workflow.
Of them 52 percent were in a test file the pull request had changed and 23 percent in another file of a package it had changed.
Another 23 percent were in a package it had not touched: most of those are tests that fail by chance, and the rest are guards that read the whole tree.
Those guards failed eight pull requests in all, counting the ones in a package the pull request had changed.
Until then every brief said to run the touched tests by name and let CI run the rest, so a goblin guessed which tests its change could break, and each wrong guess cost a red run, a second push and about 20 minutes.

The pick is the gate step's plan and more, in the order a failure is likeliest and cheapest to find:

1. The imports of every package the change reaches and of its tests, by `go list -test`, which builds nothing, takes seconds and is where an import cycle shows.
2. The guards, under the [verification-level rules](#verification-levels).
   They read the whole tree, so a change anywhere can fail them.
   A new whole-tree guard is named in the policy.
   A test of the policy fails a guard whose test is gone, since it would run nothing and pass.
3. The changed packages that are quick to test, and the packages a contract names for a changed file, each whole.
4. For a change under `frontend`, the type check, the lint, the unit tests and the browser specs the change touched or names.
5. The changed packages the policy lists as slow, without their slowest tests. They come after the board's checks because a busy machine can spend the whole limit in them: on 2026-10-10 the replays of two pull requests were ended by the limit inside the quick tests of `cmd/goblins` and `internal/supervisor`.
6. `go vet` of every package the change reaches, which is where a test of an importer that no longer builds shows. It comes after the changed packages because each of those is built and vetted by its own tests. As the first check it took 7 minutes of one replay on a busy machine to report an import cycle that `go list` reports in 14 seconds, and another replay was ended by its limit in the seventh of its 22 checks.
7. The packages that import a changed one, nearest first.

A browser spec is picked when it changed, when it opens a fixture page that changed, when it imports a changed file beside the specs, or when it shares a word of its name with a changed source, as `run-terminal.spec.ts` does with `RunCard.tsx`.
Most specs open the whole board, so what a spec imports does not say what it covers, and its name is what there is to go by.
Where `frontend/node_modules` is missing the run starts none of the board's checks and says so, because npm can exit 0 where it found no program to run.

A package the policy lists as slow runs without its slowest tests.
The run reads this machine's own record of the shortest time each test has passed in, under `goblins\verify\times` in the user's cache folder, and leaves out, with `go test -skip`, the tests the record has at 2 seconds or longer.
The record keeps a test's fastest pass because a test left out is not run and so is never timed again: in two hours of replays on a busy machine 13 tests of `internal/supervisor` and 3 of `cmd/goblins` passed in over 2 seconds that had passed in under 2 before, and a record of the latest time would have left each of them out from then on.
A test that grows slower keeps its old time and keeps running, which costs time and loses nothing.
On 2026-10-07 and 2026-10-08 the tests under 2 seconds were 81 to 88 percent of the tests of `cmd/goblins` and `internal/supervisor` and 9 to 28 percent of their time.
A test in a test file the change touched always runs, slow or not.
A slow test in the test file named for a changed source, as `spawn_test.go` is for `spawn.go`, runs when the record has it under 15 seconds.
From 2026-09-05 to 2026-10-10 sixteen slow tests failed a pull request from a test file it had not touched, in a package it had changed.
Five of them, in four pull requests, were in the test file named for a source the pull request changed, and each took under 15 seconds here.
Two were the tests of PR 613 that the pick would otherwise have left to CI.
The slower tests beside a changed source stay left to CI: counting them all as touched ran 37 slow tests, seven minutes of the limit, for a change to one file of `cmd/goblins`.
A slow package this machine has not timed runs whole when it changed, which is what times it, and is left to CI when it only imports the change.
Every `goblins gate prepush` run and every `goblins gate test` run adds what it timed to the record.

The run fits the machine.
It waits for the machine's turn in the line `goblins gate test` runs wait in, so the two never run together, and it tests one package at a time.
It starts a check only while free memory and commit are over the 4 GB floor, and none after its time limit, which is 15 minutes of running unless `--limit` says otherwise.
Beside a working fleet free memory dips under the floor for seconds at a time, so a run waits for the floor, says once what it waits for, and leaves the rest to CI only when the limit passes first or memory cannot be read.
A run that gave up at the first low reading ran 9 of its 42 checks on 2026-10-10 and left the two changed slow packages to CI.
A check still running at the limit is ended, which is no failure.
A browser case that failed before the limit ended its check is one: the browser check is run with Playwright's list reporter, for the reader, and with the gate's own reporter, `frontend/tests/gate-cases.ts`, which writes a row as a case ends that is not what the case expected, and the run reads those rows as the check runs.
The list reporter marks a case by its status alone, so a case declared with `test.fail` that failed as expected is marked as one that failed, and the gate's reporter is what tells the two apart (#1587).
So a case that failed before the cutoff fails the push, named, rather than being left to CI as a case that never ran, and a check whose cases all passed before the cutoff is still left to CI.
On 2026-10-27 a prepush run of 141 specs reported three assertion failures, the limit ended it after 465 of 1603 cases, and the run said it passed what it ran (#1542).
A case a retry then passed is no failure, since the gate's reporter writes a row for each try that failed and one for the retry that passed it, and a case is what its last row said.
The case the limit itself ended is no failure either: ending the check ends the browser and the worker running a case, Playwright closes that case as failed when either goes before its runner does, and the gate's reporter writes a row for it.
So the run reads no row once the limit has passed, and that case is left to CI with the cases that never ran (#1927).
The line of a case that failed before the cutoff names the cases the check reached:

```text
goblins gate prepush: failed at check 4 of 4, 1 browser spec: 2 browser cases failed before the time limit ended the check: [board] tests/afk.spec.ts:12:7 › a title, [board] tests/afk.spec.ts:19:3 › another title. CI would fail on them too, so fix them before you push.
```
It is ended with everything it started, since each check runs in a Windows job of its own that every process it starts joins and none can leave, a test's helper whose parent already exited included, and the run names each process that ended by its name and pid:

```text
goblins gate prepush: the time limit ended tests of internal/supervisor and these processes it ran: go.exe pid 4242, supervisor.test.exe pid 4343, supervisor.test.exe pid 4404
```

Ending a check with what its parents still reached left that helper running on 2026-10-12, a `supervisor.test.exe` whose test process had already ended.
The job ends with the run too, so a run that is itself ended, as a goblin's tool ends it, leaves nothing of its checks running, and so do the checks of a `goblins gate test` a gate's time limit ends.
A machine service a check started, such as Docker Desktop, is spared when the run ends a check, as a goblin's terminal spares it.
Whatever it did not run it names under `left to CI`, each with why: the slow tests by number and time, the other browser specs, the board's build, and the checks the limit or the memory cut.
CI is the check of those: a pull request's own run starts every job the change can alter, and the merge train's run that lands it starts them all.
That is one more reason the guards run here whatever changed: a pull request's own run starts no job for a package its change does not reach, so a guard in such a package would first fail in the merge train.

It prints the pick before it runs anything, and `--plan` prints it with each check's command and runs nothing.
This is the pick for a change to three files of `internal/supervisor`:

```text
goblins gate prepush: 3 files changed since 3301d7c9, which picks 9 checks to run within 15m0s
1. imports of 5 packages and their tests (an import cycle fails every test of its package)
2. guard tests of the root package (its tests read every link of the contract, the workflows and the shipped skills, and every tracked file for a real tailnet's name)
3. guard tests of internal/gatetest (they read every tracked file against this policy)
   - TestTheRepositoryPolicyAccountsForEveryTrackedFile
   - TestTheRepositoryPolicyNamesOnlyFieldsAndPackagesThatExist
   - TestTheRepositoryPolicySelectsEveryPackageWhoseTestsReadAChangedFile
4. tests of internal/execx (the read and process-start guards scan every Go file: internal/supervisor/error_wakes.go and 2 more)
5. tests of internal/fsx (the read and process-start guards scan every Go file: internal/supervisor/error_wakes.go and 2 more)
6. tests of internal/supervisor but for 179 slower ones (changed)
7. go vet of 5 packages (they build against what changed)
8. tests of internal/digest (imports internal/supervisor)
9. tests of cmd/goblins but for 152 slower ones (imports internal/supervisor)
left to CI:
- 179 tests of internal/supervisor (they take 2s or longer each here, 29m2s in all, and are in files the change did not touch)
- 152 tests of cmd/goblins (they take 2s or longer each here, 1h10m23s in all)
policy: config/verify.json version 1 at 3301d7c9
```

The first check that fails ends the run with exit 1, and its last line names the check and the tests:

```text
goblins gate prepush: failed at check 6 of 9, tests of internal/supervisor but for 179 slower ones: TestOverlapReadThatKeepsFailingWakesTheCEOOnTheThirdPassInARow failed. CI would fail on it too, so fix it before you push.
```

A test that fails runs once more by itself before it fails the push.
A test that waits on a real terminal can miss its own deadline while another run holds this machine's processors, which the change did not cause and CI would not see, so a test that passes by itself is named as that and the run goes on.
That second run has five minutes of its own, past the limit when the limit ended the check, so whether a test failed never depends on where the limit fell.
In a replay on a busy machine four tests that wait for a terminal's host failed beside the one test the change had broken, the limit ended the check, and without a second run all five were named as failures CI would repeat.
A test whose second run does not end in its five minutes fails the push, in a line that says it failed and never passed.

A run in which nothing failed exits 0 and says how many of the picked checks ran.
A pick that holds nothing this machine can run, such as a change to the board where the board is not installed, exits 0 and says that no check ran here and how many it left to CI.
A run the machine gave no turn says that nothing ran and exits 1.

`goblins verify <task>` runs this as the project's fast tier, by `verification.fast` in `data/projects/code-goblins/project.json`, so a goblin and the CEO run the same check by the same command.
A tier that fails prints its failed check's last line and the file that holds the rest.

### What the replay showed

From 2026-10-08 to 2026-10-10, 18 pull request runs failed CI.
Each was replayed on 2026-10-10: the pull request's head as it was pushed, in a clone of its own with this policy on its base, on this machine beside the working fleet.
The command fails before the push on 10 of the 18, each on the test or check CI failed on: nine by a run of the whole command, and one by a run of the browser check its pick holds.
They are a new command that said nothing of whose it is, two links the contract did not carry, an import cycle in a test, a count a test held, a rule that held a wake back, one of the five tests of PR 613, a unit test of the board, and two pushes of one pull request whose browser specs expected the old sentence.
The quickest took 9 seconds and the slowest 16 minutes, its 15 minutes and a failed test's second run.
The most memory a run and everything it started held at once was 2.2 GB where it ran browser specs, 0.9 GB where it reached the board's type check, and 0.6 GB for the Go checks alone.

A pass here does not say everything CI will, and the other 8 show how:

- Four fail on CI's runner and pass on this machine: a runner with no graphics adapter, a runner whose temporary folder has a short spelling, a time measured against a machine that runs few processes, and a hover that moved a terminal in CI's browser and not in this one.
- One broke only against a main that had moved. CI tests the pull request merged into main, and this runs the branch as it is.
- One is a browser spec the change neither touched nor names, which the pick leaves to CI with the other specs.
- One is a step of the install workflow, which runs the installer on a clean runner.
- One failed by chance in CI.

## Reading speed evidence

`goblins doctor` separates validation invocation timing by harness, recorded model, role, step and outcome.
Successful calls, errors and cancellations never share a timing row.
Error and cancellation durations measure failure latency, not successful speed.
Spawn hints show outcome counts and label implementation unmeasured, because the native database observes validation agents rather than the implementation session.
Missing or unreadable telemetry stays an explicit unavailable result.
No implementation-speed comparison can be inferred from these gate timings.
