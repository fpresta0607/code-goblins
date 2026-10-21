# Pipeline policy

`config/pipeline.json` is the checked-in CFO policy for no-mistakes v1.75.1.
The CFO remains the driver; no pipeline steps are reimplemented here.
The binary still owns review, fixes, tests, lint, documentation, push, PR creation and CI.

## Task classes

| Spawn class | Review repair cycles | Gate agents under v6 | Gate agents under v5 |
| --- | --- | --- | --- |
| `ordinary` (default) | 2 | The task's own harness | Codex gpt-6.1-sol xhigh, then Claude |
| `high-risk` | 3 | The task's own harness | Codex gpt-6.1-sol xhigh, then Claude |
| `mechanical` | 2 | The task's own harness | Codex gpt-6.1-sol xhigh, then Claude |

Use `cfo spawn <id> ... --class high-risk` for a high-risk task.
A task freezes the version its home's `config/pipeline.json` holds when it is spawned.
The checked-in file is v5, and a home takes v6 by writing its own, as [A gate on its task's own harness](#a-gate-on-its-tasks-own-harness) describes.
No-mistakes tasks receive a policy snapshot at `state/tasktmp/<id>/pipeline.json`, with its class and SHA-256 recorded in task metadata.
Editing the source policy does not change a running task's snapshot, and `cfo switch` retains it.
Direct-PR and local-only tasks retain their existing delivery paths.
Existing tasks without a snapshot are not silently migrated.

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
Before a CFO-managed run starts, the driver therefore requires the effective field to be absent, which inherits the global chain, or to select only Codex explicitly, which runs that repository's gates without the fallback.
The driver does not rewrite repository configuration, and non-CFO no-mistakes use retains its native repository policy.

## A gate on its task's own harness

Policy v6 names no harness.
Every gate role of a task, which is review, review fix, test, document and lint, runs on the harness that task's goblin runs on, with the model and effort its record holds.
A Claude Code goblin's gate runs Claude Code, and a Codex goblin's gate runs Codex.
A task whose record names no model or no effort takes the machine config's `agent_config.<harness>` value, which is the operator's default for that harness.
A gate with neither is refused, and the refusal names both ways to name them: `cfo switch <id> --model <model> --effort <effort>`, or `agent_config.<harness>`.
The harness is read from the task's record when a run starts and is not frozen in the snapshot, so after `cfo switch` the next run follows the new harness.
A run already started keeps the agents it started with.

### How a run carries its own agents

One no-mistakes daemon serves every goblin, and v1.75.1 reads its agent chain from two places only: the machine's `config.yaml` and the repository's trusted default-branch `.no-mistakes.yaml`.
Neither differs between two tasks of one repository, and its one per-run pin, `--model` with `--effort`, serves pi alone.
So a run carries its own agents as a launch selection, which `cfo pipeline run` writes and no-mistakes proves.

1. The driver writes `state/tasktmp/<id>/launch-selection-<launch>.json`, a file of its own for each start: the trusted default-branch commit, `"apply": true`, and one ordered chain for the primary, reviewer and fixer roles.
2. It starts the run with `--launch-nonce`, `--validation-generation`, which is the task's frozen policy hash, and `--launch-assertion <file>`, and prints the chain on a `pipeline gate agent:` line.
3. The daemon replaces that run's agent chain, each named harness's model and effort, and every review role with the selection, for that run alone and in memory.
4. It then proves the selection as it proves any launch assertion: against a fresh fetch of the trusted commit and against the arguments each agent will be started with, before a run exists and before any agent starts.
5. The selection and its proof are stored with the run, where neither can change, and a daemon that restarts proves the stored selection again before it recovers the run.

Nothing edits the machine's `config.yaml` for a run, so two tasks on different harnesses gate at once under one daemon and one config.
A no-mistakes build that cannot apply a selection refuses the launch before it takes the branch: a build with no launch assertion rejects the flag, and a build that proves but cannot apply rejects the `apply` field.
The driver then says which build v6 needs.
A repository's own `agent` field does not stop a v6 start, because the run's selection replaces it.
Resume restarts a paused task's gate run past `cfo pipeline run`, and that start carries the same selection, on the harness the goblin comes back on.
It runs `axi sync --recover` first only when the paused run holds gate commits it never pushed, since no-mistakes refuses that recovery for a run whose head is its push binding, and for one whose goblin then pushed follow-up commits over that head; such a run is restarted from the branch as its goblin left it.
It holds to the driver's checks as well.
A resume whose replacement run cannot be given its agents, such as one coming back on pi, starts no run and says why.
A paused task frozen before v6 restarts its run only while the machine config still matches its policy, and is otherwise refused with the `cfo pipeline migrate <id>` that clears it, as `cfo pipeline run` refuses it.

### A second harness only when named

With no fallback named, a gate agent that fails fails its step, and no other provider starts.
The operator names a fallback in the home's `config/pipeline.json`, with its harness, model and effort in full:

```json
{
  "version": 6,
  "fallback": {"harness": "codex", "model": "gpt-6.1-sol", "effort": "xhigh"},
  "auto_fix": {"review": 0, "test": 1, "lint": 1, "rebase": 1, "ci": 1},
  "classes": {
    "ordinary": {"review_cycles": 2},
    "high-risk": {"review_cycles": 3},
    "mechanical": {"review_cycles": 2}
  }
}
```

A task on another harness then names the fallback second in its chain, and only while the fallback's own status command says somebody is signed in: `claude auth status --json` or `codex login status`, read as the quick start reads them.
A fallback that is signed out, missing or unclear is left out, and the `pipeline gate agent:` line says so.
A task on the fallback's own harness has no second harness.
A task frozen before the fallback was named takes it with `cfo pipeline migrate <id>`, as it takes a newer version.

### What v6 needs

Policy v6 needs a no-mistakes build that applies launch selections.
That is the fork at `https://github.com/fpresta0607/no-mistakes` with its pull request 5, and with pull request 4, b05697a, which v5's Codex arguments already need.
The install puts in the build of that fork `install.ps1` pins, commit `bcbb444`, the head of its pull request 9, which carries pull requests 4 and 5, and each release builds and signs it beside its own programs.
The checked-in `config/pipeline.json` stays at v5, so a fresh install gates as before until the policy moves to v6.
no-mistakes proves Codex and Claude Code selections only, so a pi goblin has no gate under v6: `cfo spawn` refuses a pi task in `no-mistakes` mode there, and `cfo pipeline run` refuses one switched to pi.

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
A config that already matches the policy changes nothing, so `config-apply` then needs no window and returns at once.
Without the window it refuses, naming each gate run in flight with its repository, branch and status and saying the change waits for them, or saying that the daemon runs with no run in flight.
Native v1.75.1 treats completed, failed, cancelled and ci_monitor_interrupted runs as terminal; pending, running, unknown and NULL statuses still block the window.
An interrupted CI monitor is not a passed gate and requires no cancellation or database rewrite to establish that it has stopped executing.
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

Owned machine fields under v6 are v5's without the chain and the Codex profile: `agent_args_override.codex` and `agent_args_override.claude` as below, the absence of `review_agents`, the absence of `agent_path_override.codex`, `auto_fix.review: 0`, and one automatic follow-up each for test, lint, rebase and CI.
v6 does not own `agent` or `agent_config`.
The `agent` list is the operator's, and it serves only runs started outside `cfo pipeline run`, since every run the CFO drives carries its own agents.
`agent_config` holds the operator's default model and effort for a task that names none.
So a machine whose operator set `agent: [claude]` by hand, as on 2026-10-09, reaches v6 with `cfo pipeline config-apply` writing the two argument lists and nothing else, and one config serves every harness and every fallback.
Owned machine fields under v5 are `agent: [codex, claude]`, `agent_config.codex: {model: gpt-6.1-sol, effort: xhigh}`, the absence of `review_agents`, `agent_args_override.codex: [-c, 'service_tier="default"', --ignore-user-config, --disable, plugins, --disable, apps, -c, 'personality="pragmatic"', -c, 'model_auto_compact_token_limit_scope="total"', -c, features.multi_agent=true, -c, project_doc_max_bytes=65536]`, `agent_args_override.claude: [--strict-mcp-config]`, the absence of `agent_path_override.codex`, `auto_fix.review: 0`, and one automatic follow-up each for test, lint, rebase and CI.
Under v4 `agent_args_override.codex` is `[-c, 'service_tier="default"']` and the policy does not own Claude's arguments.
Under v2 and v3 `agent` is `[codex]` and both global `review_agents` roles carry the version's Codex profile.
The raw Codex argument override forces standard service for every managed role, so a user-level fast or priority preference cannot leak into a gate, while `agent_config` owns model and reasoning effort.
Removing the Codex executable override makes no-mistakes resolve the native `codex` command from `PATH`; executable overrides for other harnesses remain operator-owned.
Up to v4 the exact legacy CFO-owned Claude model and effort vector is removed during apply, and a differing operator-owned Claude vector is preserved.
From v5 the policy owns Claude's arguments as well, since Claude is a gate agent of its chain, and under v6 the agent of every Claude Code goblin's gate.
Document follow-ups and other native settings retain their existing values.
Spawn never rewrites shared YAML.
The YAML parser dependency is needed to preserve unrelated configuration structurally; v3.0.1 avoids the old parser's [known panic vulnerability](https://pkg.go.dev/vuln/GO-2022-0603).

Run `config-apply` before migrating any live task snapshot, unless `config-drift` already reports no drift for the current policy.
`migrate` requires the applied target-version global configuration and the task's pipeline lock.
It writes only the task's own snapshot, metadata and status, never the shared config, so it runs while the daemon serves gate runs: a policy that only catches up with the machine never needs the daemon stopped.
It accepts every forward transition, from any of v1 to v5 to any later version, and preserves the task class and `review_cycles` cap exactly.
Between two v6 policies, which can differ only in the fallback the operator names, it moves the task to the current one the same way.
The same frozen policy is a no-op; downgrades and invalid snapshots are refused.
Before replacing either owned field, it writes a task-local transaction journal containing the validated old and new snapshots and the expected audit event.
An interrupted command resumes that journal under the same pipeline, cleanup and metadata locks before any pipeline command trusts the snapshot hash.
The task-scoped metadata lock also serializes `cfo pr check` and `cfo switch`, so neither command can publish a stale whole-record update over the migrated hash.
Migration removes its journal only after the snapshot replacement, metadata hash update, and audit append have all completed while that lock remains held.
Recovery finishes the migration once or rolls both owned fields back when audit persistence fails, without overwriting unrelated task metadata.
An append reported as successful commits the audit without a second read, while an errored append whose persisted state cannot be reread leaves the forward journal intact for retry.
The task status receives one line containing the class, cap, old hash and new hash.
Migration never changes a worktree, native run row, gate ref, origin ref, or review result.

## Driving a task

```powershell
cfo pipeline run <id> --intent-file intent.md
cfo pipeline respond <id> --action fix --findings finding-id --instructions "Concrete guidance"
cfo pipeline respond <id> --action approve
cfo pipeline respond <id> --branch fix/connections --action approve
cfo pipeline recover <id>
cfo pipeline migrate <id>
```

Write the run's intent to a file and name it with `--intent-file`.
A short one may be given with `--intent <text>`.
Either way `cfo pipeline run` hands it to no-mistakes in a file, never on a command line, and so does the restart of a paused task's run.
An intent is prose, often several thousand characters that name a shell, and on 2026-10-02 Microsoft Defender detected one on a command line as `Trojan:Win32/ClickFix.DQ!MTB`, the lure that has a person paste such a line, and Windows refused the start.
A no-mistakes before 1.86.0 has no `--intent-file`, so for such a build alone the intent stays on the command line, and the command says so.
A response's `--instructions` still reach no-mistakes on its command line, which is the only way it takes them, so keep them short and plain.

Commands operate in the recorded task worktree and use its frozen policy.
A task that gates several branches from extra worktrees names one: `run`, `respond` and `recover` take `--branch <branch>`, and `respond` and `recover` take `--run <run-id>` instead.
The command then acts in whichever of the task's recorded worktrees, its own or an extra one `cfo worktree add` made, has that branch checked out, because a native command answers the latest run of the branch checked out where it runs.
A run must be its branch's latest run in the project, and a branch or run that none of the task's worktrees has checked out is refused before anything reaches the gate.
The pane exports `CFO_HOME` and `CFO_STATE_OVERRIDE` and every gate step inherits both, so the `--intent` text must require any step that runs `cfo`, or a shell that resolves it, to clear them or point them at a temporary directory.
`internal/home` refuses the inherited fleet home from a test binary, which covers `go test`, but the real `cfo` binary is not a test binary and no-mistakes has no per-repo step environment setting, so the intent is the only place left to state it.
Commit work on a named feature branch before `run`.
The project must be initialized for no-mistakes, with readable committed task and origin default-branch `.no-mistakes.yaml` files.
Refresh origin before starting; global reviewer/fixer drift is refused.
A repository's committed `auto_fix` counts are read from the submitted branch, which can edit its own `.no-mistakes.yaml`, so each is held to the frozen policy's count as a ceiling: a repository may lower one, which only sends more to a person, and a count above the policy's, a negative count, or a key the policy does not govern (anything but review, test, lint, rebase, ci and its legacy name babysit) is refused.
No-mistakes v1.75.1 does not expose an assertion that binds an expected trusted SHA and effective primary after its fresh fetch but before agent creation.
Up to v5 the `run` check validates the trusted inputs before invoking native start and does not establish that post-fetch binding.
Under v6 the launch selection establishes it: the daemon proves the trusted commit and every role's agents after its own fetch and before any agent starts.
A repository's `agent` field continues to select only its native primary path and cannot replace the global reviewer or fixer profiles.
An earlier unresolved run cannot be restarted to reset its budget.
Use native read-only `axi status` and `axi logs` to inspect progress; the engine's guarded `axi sync` remains the branch synchronization interface after validation.

`respond` requires durable evidence of one parked gate in this project's current branch.
It refuses a gate already answered or still running and checks that the active review's persisted automatic limit is disabled or zero.
An explicitly selected finding with an empty action is actionable only when the active gate is not review and its persisted automatic-fix limit is positive.
Review remains fail-closed for empty or unknown actions.
Fix responses name concrete actionable finding IDs; selecting an `ask-user` finding is an explicit decision, never automatic consent.
Approval is accepted only when every finding is explicitly `no-op`, or the findings list is empty.
The one exception is the CFO's own decision: `respond <id> --action approve --accept <ids>` takes the open findings as they stand.
It is honoured only from the registered primary CFO process, proven as `cfo question` proves it, and a goblin running it on its own gate is refused before anything is recorded.
The IDs must be exactly the gate's open `ask-user` and `auto-fix` findings, no more and no fewer; a finding with an empty action still needs its fix.
The task's status log records `pipeline-findings-accepted` with the step, run, round and IDs before approve reaches the gate.
Unsupported or malformed evidence fails closed.
The command returns exit 3 for unresolved work requiring a CFO decision, without advancing the gate.
Other refusals return exit 1.
This is a cooperative driver guard, not an operating-system sandbox around direct native commands.
Agents must use the driver for managed tasks and must not invoke native mutation commands to bypass its budgets.

`recover` returns custody after a failed or cancelled unpublished run when native recovery is blocked by a stale recorded head.
For pipeline-owned recovery, it requires a clean worktree whose head exactly matches both the submitted head and native gate branch.
The stale recorded commit must be an ancestor of that preserved head, or the gate's own rebase of it.
A run whose rebase step moved its recorded head and that then failed before it pushed leaves that rebase unverified, and native recovery refuses it with `blocked_recover_unverified_head`, with or without `--keep-local`.
The driver accepts such a head only as a pure rebase: its first-parent commits that no remote-tracking ref of the gate repository holds, laid on the first commit one does, must carry the submitted head's changes and nothing else.
A gate fix commit on the rebase, or a base no remote-tracking ref holds, is refused, so keeping the submitted head drops nothing that is not already published.
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
Pipeline-owned recorded-head repair requires strict ancestry or that proof of a pure gate rebase.
After native has already returned clean user-owned custody, the registered identity, unpublished-run state, stale-head anchor, and compare-and-swap guards are the authorized boundary, so the driver does not reject legitimate rebases or follow-up commits by comparing them with the stale head.

This repository's committed automatic-fix overrides remain authoritative for a new submitted branch.
Once the shared config runs the current policy, migrate each task frozen at an older version explicitly before its next gate command.
Newly spawned tasks freeze the version the home's `config/pipeline.json` holds.

## Verification levels

`cfo gate test` is this repository's test step (`commands.test` in `.no-mistakes.yaml`), and it plans before it runs.

```powershell
cfo gate test                     # fast, the default: static checks and the quick changed packages
cfo gate test --level affected    # every package the change reaches, which CI's run also covers
cfo gate test --level full        # every package
cfo gate test --plan              # print the plan and run nothing
```

| Level | `go vet` | `go test` |
| --- | --- | --- |
| `fast` | the packages the change reaches | the changed packages and the packages a contract selects, unless the policy lists them as slow |
| `affected` | the packages the change reaches | the same packages |
| `full` | every package | every package |

A change reaches the packages that own a changed file, the packages that import one of those directly or transitively, and the packages a contract of the policy names for a changed file.
A package owns the Go files in its directory, the files under its `testdata` folder and the files its `//go:embed` patterns cover, so a doc the binary embeds selects the package that embeds it and a doc nothing embeds selects none.

A change requires `affected` before it merges, and `full` once `go.mod` or `go.sum` changed, the policy cannot be read, or a changed file is one the policy does not account for.
CI tests the level a change requires on the pull request's own run, reading what the change reaches as `cfo gate test --plan` does, and every package in the merge train's run that lands it.
So CI is the check of the level a change requires, and nothing reaches main that the full level did not test.
With no `--level` the step runs `fast`, which is what the gate runs.
Running the required level here as well ran the slowest packages twice, once on this machine and once in CI: from 2026-10-05 to 2026-10-07 every such local run took a median of 33 minutes, where CI tested every package in 11.
A level asked for runs instead, such as `affected` to reproduce what CI found; a run at a level narrower than the change requires exits 0 when its checks pass, and its last line and its report name the level the change requires, which CI's run checks.
Before a push, `cfo gate prepush` runs more than the fast level and picks the tests of a slow package itself, so nobody runs tests by a name they guessed ([Before a push](#before-a-push)).
The plan prints why each package is in it (`changed`, `imports <package>`, or a contract's reason with the file that changed) and every test run the level left to a broader one, with why.
It then lists the changed files that are outside the Go checks, under the policy's reason for each, and the changed files the policy does not account for, so what the step does not check is read from its output and not assumed:

```text
cfo gate test: level fast, the default, since CI runs every package; the change requires affected: the default for a change
cfo gate test: the change since 0123abcd reaches 3 package(s); CI runs every package
- example.com/m/internal/doctor (tests run or read these scripts: install.ps1)
- example.com/m/internal/installscript (tests run or read these scripts: install.ps1)
- example.com/m/internal/installtest (tests run or read these scripts: install.ps1)
left to the affected level:
- tests of example.com/m/internal/installscript (a slow package)
changed files outside the Go checks:
- README.md, docs/pipeline.md (documentation and housekeeping no Go check reads)
```

CI tests the level the change requires on the pull request, and every package in its merge train's run, whatever the local level was.

The policy is `config/verify.json`:

```json
{
  "version": 1,
  "slow_packages": ["cmd/cfo", "internal/installscript", "internal/supervisor"],
  "contracts": [
    {
      "paths": ["install.ps1", "install.cmd", "tools/pin-installer.ps1"],
      "packages": ["internal/doctor", "internal/installscript", "internal/installtest"],
      "why": "tests run or read these scripts"
    }
  ],
  "outside": [
    {
      "paths": ["README.md", "docs/**"],
      "why": "documentation and housekeeping no Go check reads"
    }
  ]
}
```

`slow_packages` are the packages whose tests take minutes on a loaded machine, each named by its directory from the repository root with forward slashes, and `.` for the root package; the fast level vets them and leaves their tests to `affected`.

`contracts` say what the import graph cannot see: the files a package's tests read by path, such as a script they run.
A change to a file one of a contract's `paths` names selects the contract's `packages`, named as `slow_packages` names them, and the plan gives the contract's `why` with the file.
A contract selects its own packages and not the packages that import them.

`outside` names the files no Go check reads, each rule with why: a doc nothing embeds, or the frontend's sources, which its own checks cover.
A change to one of them selects nothing, and the plan lists it under the rule's reason.

A path is a pattern from the repository root with forward slashes: `*` and `?` match inside one name as Go's `path.Match` does, `**` stands for any number of folders, none included, and case does not count.
A file a package owns is that package's whatever a pattern says of it, and a contract is read before `outside`.

A policy that has `contracts` or `outside` says what every file is.
Under it a changed file that is in no package, under no contract and not listed as outside is one nobody has said anything about, so the change requires `full` and the plan names the file.
`TestTheRepositoryPolicyAccountsForEveryTrackedFile` in `internal/gatetest` fails when a tracked file is in that state, so a new kind of file gets its line in the policy in the change that adds it; that change is itself planned by the default branch's policy, which does not know the file yet, and so requires `full` once.
A policy with neither field keeps the earlier rule: a file no package owns selects nothing and nothing is listed.

The policy that applies is the default branch's, read from the commit where the branch left it and never from the branch's own copy, so a branch, or a fix commit under a gate, cannot loosen the policy it is checked by.
A change to the file therefore takes effect once it is on the default branch.
A build ignores a field it does not know, so the build a gate has installed keeps reading a policy that a later build extended.
A file that does not parse, or a version the build does not know, makes the change require `full`.
A repository with no policy file has no slow packages.

Every run that is not `--plan` leaves a report, and beside it a log of what its commands wrote, in `<user cache folder>\cfo\verify\reports\<project>\`, and its verdict line names the report.
A run at a level narrower than the change requires ends with one more line naming the level the change requires, which CI's run checks.
`CFO_VERIFY_DIR` names a folder to use in place of `<user cache folder>\cfo\verify`, so they go under its `reports\<project>\`.
The report holds the project, the task when a goblin ran it, the commit, where the branch left the default branch, how many files were uncommitted, the level run and the level required with why, the policy and the toolchain, each package with why it was selected, each test run left out with why, and each command with its start, duration, exit code and status: `passed`, `failed`, `over_budget` when a check passed but ran past its budget, or `not_run` when admission or an earlier command failed.
For the test command it also holds what became of each package: `passed`, `failed`, `build_failed` when it did not compile, `no_tests`, or `unfinished` when its tests were still running as the run ended, with its time, how many tests it ran, the tests that failed and the tests that started and never finished, which is how a test that hangs shows.

The tests run as `go test -json`, and the step prints what `go test` prints without `-v`: a compile error as it arrives, each package's summary line as the package ends, and for a package that did not pass what it printed itself and what its failed and unfinished tests printed.
A run in which a package did not pass ends its tests' output by saying which packages, and which tests in them:

```text
cfo gate test: go test did not pass in 2 of 7 package(s):
- example.com/m/internal/spawn: TestSwitchWaits did not finish
- example.com/m/cmd/cfo: TestUpdate, TestUpdate/rollback failed
```

The log holds every line the tests wrote, as `go test -v` prints them, so a passing test's lines and every test's own time are read there.
If the test output or log cannot be written, the test check fails even when `go test` exits zero.
The 20 reports of a project written last are kept, the one a run just wrote always among them, and an older one is removed with its log.
A log with no report belongs to a run still going and stays, until nothing has written to it for 24 hours, when the project's next run removes it.
A run that cannot write its report says so and keeps its verdict: the checks decide the exit code, never the store.
A report is written whole or not at all, and the board reads them: a live task's card shows the newest run of its change, as [docs/native-board.md](native-board.md) describes.

### Taking turns

Every `go vet` and `go test` command waits for a turn at every level: the shared capacity limits concurrent commands, every goblin's and every gate's alike, in the order they asked, with a low-priority run behind every other.
Any other command stands in the same line behind `cfo gate turn`, described under [Any command in the line](#any-command-in-the-line).
The janitor's prunes of the package caches stand in it too, each as its own `cfo gate turn --wait 30s`, started only while the line has one slot that no run holds or waits for, so a run that takes the line first defers the prune rather than queue it.
Each command releases its turn when execution ends and joins the common line again before its next command.
The default remains one, which keeps heavy commands from competing for the same machine's resources.
Production admission always uses the operating system's user cache folder under `cfo/verify/slots`, regardless of report or process environment redirects.
The shared `capacity.json` setting accepts only `{"capacity":1}` or `{"capacity":2}`; absence means one.
Activating capacity two requires a separately reviewed quiet window with no legacy command executing or queued.
An unreadable or invalid setting, or any nonempty `CFO_VERIFY_SLOTS` process setting, refuses admission.
Only test binaries can use explicitly isolated admission stores.

A command also waits while either physical or commit availability is under the shared floor.
At capacity one the floor is 4 GiB.
At capacity two it is weighed for the run at the head of the line: what each run holding a slot may still use, its expected memory less what it commits now, then the head's own expected memory and the desktop reserve, and never under 4 GiB.
A run's expected memory is what `cfo gate turn --memory` declares, or 2.5 GiB without it, and a run's card in the line carries it with what the run commits, measured as it runs.
A run behind the head never passes it, however well a smaller run would fit.
An unavailable reading or an hour of floor waiting refuses admission and starts no command.

While it waits, a run says which run holds the turn, for how long and under what budget, and where it stands in line, as it starts to wait, whenever its place in line changes and once a minute:

```text
cfo gate test: waiting for its turn (3m0s so far): the turn is held by code-goblins at 0123abcd, affected level, in C:\work\code-goblins (pid 4242), for 12m0s of its 1h30m0s budget; 1 run is ahead of this one in line
```

Every take, protection and release of a turn goes through the line's lock, which another run's change holds for milliseconds, and each change waits five seconds for it.
A run that finds the lock busy for all five seconds, as a loaded machine can keep it, as it first checks the line, takes its turn at the head of the line, or protects the turn it took, keeps its place and tries again.
At the head of the line it says `the line's lock is busy` as the lock turns busy.
It tries again within its admission's limit, an hour for `cfo gate test` and `cfo gate prepush`, past which it gives up with why, and for as long as it waits for `cfo gate turn` and `cfo verify`, which set none.
On 2026-10-14 a run that had waited an hour and fourteen minutes reached the head of the line and ended with no turn on one busy change of the lock.

A gate shows a step's output only once the step has ended, so `cfo gate turns` prints the line from outside while the wait lasts: each run that holds a turn, the runs that wait in the order they take their turns, a low-priority run marked and behind every other, and the memory when the machine is short of it.
At capacity two it also shows what each run is expected to use and what each holder uses now.
`cfo gate turns --json` gives the same as one JSON object, for the board to read, whose keys stay as they are and are always there, an empty line being empty lists: `capacity`, `is_weighed`, `holding` and `waiting`, each run with `who`, `pid`, `since`, `budget_seconds`, `now`, `estimate_bytes`, `used_bytes` and `is_low_priority`, and `memory` with `available_bytes`, `commit_available_bytes`, `floor_bytes` and `is_short`.
Under a run that holds a turn, `cfo gate turns` prints what the run says it is doing, which the run renews every five seconds while its tests run: how many packages are done, which tests are running and for how long, and the last line of its output.
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

`cfo gate turn` puts any command in that same line, so one heavy run goes at a time on the machine whoever started it: another repository's test command under its gate, a goblin's own suite, or a run one of the Overlord's sessions starts.
`cfo gate test`, `cfo gate prepush` and `cfo gate turn` wait through the same admission code in the same store, so a run of any of them waits for a run of the others.

```text
cfo gate turn [--as <name>] [--wait <duration>] [--memory <size>] [--low-priority] [--detach <log-file>] -- <program> [<argument>...]
```

Everything before `--` is `cfo`'s, and everything after it is the command's, its own flags and a `--` of its own included.
`cfo` starts `<program>` as a process with exactly those arguments, unparsed and with no shell of its own in front, so a command that needs a shell names one: `cfo gate turn -- cmd.exe /c "npm test && npm run lint"`.
`--as <name>` names the run to whoever reads the line.
`--wait <duration>` bounds the wait for the turn, as `--wait 30s`: a run that takes no turn within it runs nothing and exits 125 with a last line that says so, and once it has its turn the wait no longer counts, so its command runs until it ends.
`--low-priority` has the run wait at the lowest priority: it takes its turn only while no ordinary run waits, and an ordinary run that joins the line after it still goes first, unless the low-priority run is already taking its turn, as the desktop's CI jobs wait, and `cfo gate turns` marks it.
`--memory <size>`, as `--memory 7GiB` or `--memory 512MiB`, is the most memory the run is expected to use, which a line of two slots weighs before it starts the run beside another, as [Taking turns](#taking-turns) says.
Without it a run is expected to use 2.5 GiB.
A caller such as a gate knows its repository, branch and run, which the command and the folder do not say, so it should give them: `--as "PrecisionDocs feat/x, run 01M4FG7F"`.
A run is named `<name>: <command>` with `--as` and `<command> in <folder>` without it.
`cfo` adds `, task <Goblin> (<id>)` when the run is a fleet task's: a goblin's own run by its terminal, and a gate's run by the task registered for the run's source worktree, as for `cfo gate test`.
When the run ends in a Codex or pi goblin's own terminal and worktree while the goblin sits idle at its prompt, it types one `CFO:` line into that goblin, as `cfo send` would, naming the run, its exit code and how long it took, since neither harness starts a turn when a background run ends; `cfo gate prepush` does the same, and an interrupted run tells nothing.
A goblin in a turn is not told, since one that waited on the run there has read how it ended.
If the line cannot be typed, the run says so in one line on standard error, and its exit code stays the command's.
A gate the goblin started inherits its terminal's environment, but runs its steps in the gate's own worktree, so a gate's run types nothing.

**Not a gate's wait.**
A command that waits on a no-mistakes gate takes no turn.
`no-mistakes axi run`, `axi respond` and `axi answer`, and `cfo pipeline run` and `cfo pipeline respond`, which drive them, wait for the gate's next decision, through every agent step before it.
The gate's test command takes its own turn, since the fork stands the repository's `commands.test` behind `cfo gate turn`, as **A gate behind it** below says, so a turn held while a gate waits orders no heavy run and keeps every other run in line through the gate's agents' work.
On 2026-10-13 two goblins' fix rounds held the turn that way while up to ten runs waited.
The run exits 125 and says what to run instead: the gate's command without `cfo gate turn`, and any heavy command that came with it in a `cfo gate turn` of its own.
It names the command it was given on one line, so its last line is still its own:

```text
cfo gate turn: this run takes no turn: no-mistakes axi respond waits on a gate, whose test command takes its own turn, so a turn held while it waits only keeps every other run in line through the gate's agents' work
cfo gate turn: run the gate's command without cfo gate turn, and any heavy command beside it in a cfo gate turn of its own: no-mistakes axi respond --action fix --wait 10m
```

`cfo` reads the words of every argument, so a shell's command string, as `cmd.exe /c` and `powershell -Command` take, is read too.
A word ends at a space, a quote, `&`, `|`, `;`, `(`, `)`, `<` or `>`, and a program is known by its file name without `.exe`, in any case.
A refused run tells its goblin that it ended, as a run that did not start does.
A script the command runs is not read.

**In the background.**
`--detach <log-file>` starts the same run, with its name and its wait, as a process of its own and returns at once, printing its pid on standard output.
The run starts the way the supervisor is started: in a console of its own with no window, which its command shares, so nothing of it ever shows on the desktop, in a process group of its own, and out of its caller's job where the job allows it, so it outlives its caller.
Its standard output and error are appended to `<log-file>`, its pid is written to `<log-file>.pid` for its goblin to end it by, and its last line in the log is `cfo gate turn: the detached run ended with exit <code> after <time>`.
A run that cannot be started detached starts nothing and exits 125.
Since no harness waits on a detached run, its end is told to the goblin of any harness that sits idle at its prompt, Claude Code included.

**What it waits for.**
It waits while another run holds the turn, and at the head of the line while free physical memory or free commit is under the floor.
It sets no limit of its own on either wait.
Where `cfo gate test` refuses a run after an hour of memory under the floor, `cfo gate turn` waits for as long as memory is short, and the limits that end a run are its command's own and its caller's.
The wait counts in the wrapper's wall time, so a caller that budgets its test command should start the budget at `took its turn`, or allow for the wait.

**What it prints.**
Every line of its own goes to standard error and starts with `cfo gate turn: `.
With the turn free and memory above the floor it writes no line at all.
As it starts to wait, whenever its place in line changes and once a minute, it says which run holds the turn and for how long, or what memory the machine is short of, and where it stands:

```text
cfo gate turn: waiting for its turn (3m0s so far): the turn is held by PrecisionDocs feat/x, run 01M4FG7F: cmd.exe /c npm test, task Zora (cg-example) (pid 4242), for 12m0s; 1 run is ahead of this one in line
cfo gate turn: waiting for its turn (40s so far): 3.1 GB of memory is available and the floor is 4.0 GB; this run is next in line
```

Once, when it takes a turn it waited a second or more for:

```text
cfo gate turn: took its turn after 3m0s
```

Then comes the command's own output, and after it only `cfo`'s lines below: what the run ended or left running, why it ended its command, and what it could not clean up or prune.
A gate shows a step's output only once the step has ended, so while a run waits the line is read from outside with `cfo gate turns`.

**What it leaves untouched.**

- Standard output. `cfo` writes nothing to it, and the command inherits the handle.
- Standard error. The command inherits the handle, after the lines above.
- Standard input, the working folder and the environment. All inherited, with one variable added, `CFO_VERIFY_TURN`, described below.
- The exit code. It is the command's own, whatever it is.
- The command's limits. `cfo` adds no timeout and raises none.

**When no command runs.**

| Exit code | Last line on standard error | What happened |
| --- | --- | --- |
| 125 | `cfo gate turn: this run takes no turn: <reason>`, `cfo gate turn: run it without cfo gate turn: <command>`, or `cfo gate turn: the run ended its command, since free memory or commit stayed under the desktop reserve: <detail>` | The run gave no result of its command's. Either the line ended the command for the machine's memory, or the command was never started. The line's store or the machine's memory could not be read, the run's record could not be begun, the wait was interrupted, standard output or error was no file, as a process's always is, the command waits on a gate, which the line before the last says, or the arguments named no command, which also prints the usage. |
| 126 | `cfo gate turn: the command did not start: <error>` | A turn was taken and given back, and `<program>` could not be started, or the run's custody of it could not be recorded first. |

Neither is a test result, so a caller reads 125 as "the machine gave this run no turn" and never asks an agent to repair tests that did not run.
A command that itself exits 125 or 126 is told apart by the last line, which then is not `cfo gate turn`'s unless the run named what its command left running or why it ended the command.

**The turn.**
The turn is held from before the command starts until the command and what it left running have ended, and is given back whatever its exit code.
A wrapper that is ended leaves a turn the next run takes, since the holder is recorded by process id and start time, so no lock a dead run left ever stops the machine's tests.
The next run first proves the dead run's job ended, which takes a few seconds, and one that cannot prove it takes no turn and exits 125 until it can.
The command runs in a Windows job of its own that every process it starts joins and none can leave, whatever becomes of its parent, and the job ends with the wrapper, so a wrapper ended by force ends its command with everything the command started, and nothing the command started runs on outside the turn but a machine service, which the run leaves running by design, as below.
On Ctrl+C or Ctrl+Break while it waits, `cfo` leaves the line and exits 125.
While the command runs, `cfo` keeps the turn through the signal and exits with the command's code: the command shares the console, receives the same signal and answers for itself.
Once the command's own process has exited, interrupted or not, what it left running may go on for ten seconds, after which `cfo` ends it and names each process it ended.
A machine service the command started, Docker Desktop with what it runs, the no-mistakes daemon with its gate agents or the Scrawl server, serves the whole machine, so it is left running and named:

```text
cfo gate turn: its command left node.exe pid 5150 running after it exited, which the run ended
cfo gate turn: its command started Docker Desktop.exe pid 6120, which serves the whole machine, so the run left it running
```

**The measured run.**
The command runs as a measured execution, as each `cfo verify` command does: its record holds its wait, its CPU time, its memory, its processes and how it ended, and is kept in the report store's `executions` folder with the others and pruned with them after each run.
It holds none of the command's output, which goes to `cfo`'s own handles and is its caller's to keep.
While free physical memory or free commit stays under the desktop reserve for ten seconds, 2 GiB of each unless the store's `reserve.json` says otherwise, `cfo` ends the command with everything it started.
The reading is the whole machine's, so what the check ends is the one run the line lets use the machine's spare memory, whatever else holds memory then.
What the command was ended at is no result of its own, so `cfo` exits 125 with a last line that says why, which a gate parks the run on rather than ask for a repair:

```text
cfo gate turn: the run ended its command, since free memory or commit stayed under the desktop reserve: verify: execution 4KQ3NAWZPXDMR7YHTJ2CE6LFBU failed (interrupted): verify: reserve pressure for 10s: 1.2 GB of memory and 9.8 GB of commit available, under the desktop reserve of 2.0 GB and 2.0 GB
```

A command that exits by itself keeps its own exit code: once its own process has exited, memory under the reserve for ten seconds ends what it left, as the linger's end does, and fails nothing.
A running sample that cannot be written ends no command, since a sample is progress and the final measurement is the evidence, and a run interrupted after it took its turn but before its command started ends there with 125, since its command could never hear the interrupt.
The record's commit reading is the documented one for the process that reads it, which is the machine's free commit unless that process sits in a job with a commit limit, as no `cfo` job is.
Claude goblins launched since this change start with Claude Code's own low-memory ending of idle background shells turned off, since it ended a goblin's queued `cfo gate turn` and lost its place in the line, while this check ends the run the line let use the memory.

**A command that takes turns itself.**
This repository's test command is `cfo gate test`, which takes a turn for each of its checks, and behind the wrapper it would wait for the turn its own wrapper holds, for good.
So the wrapper gives its command `CFO_VERIFY_TURN`, which names the turn it holds: the slot, the holder's process id and the moment it was taken.
A `cfo gate test`, a `cfo gate prepush` or a nested `cfo gate turn` that finds it, with that turn still held by that same live process, runs inside it: it takes no turn, waits for none, and says what it is doing beside the holder's turn.
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
`EndAndWait` also lets the job's handle close only once the job counts nothing beyond the list.
Verification code can use `TerminateAndWait` to terminate every job member and close its handle only after Windows reports zero active processes, or `EndAndWait` to do the same while sparing a machine service the command started.
That cleanup is bounded to five seconds or the caller's shorter deadline, and a failed proof retains the handle for a retry.
Recovery uses `TerminateAndWait`, which spares nothing.
The generic executor's `End` and `Release` contracts are unchanged by these additive APIs, and `cfo gate turn` runs its command through the measured executor described below.
This accounting alone does not establish trusted execution evidence or authorize a GitHub check.
`internal/verify.Monitor` uses those measurements to wait for the whole job, retain cumulative and sampled peaks separately, and interrupt on unreadable measurements, failed evidence writes, or physical or commit headroom below its reserve.
Its reserve is the memory a run leaves the desktop, a cancellation threshold, not a reservation or a kernel limit.
It sits under the 4 GiB admission floor, since a run admitted at the floor uses the memory between the two, so a reserve at the floor would cancel nearly every run that used its headroom.
The executor reads it from `reserve.json` in the admission store, as `{"physical_gib": 2, "commit_gib": 2}`, and keeps 2 GiB of each when the file is absent.
Each value must be at least half a gigabyte and under the admission floor, and any other content, such as an unknown key, a missing dimension or trailing data, refuses the run before it starts.
A caller can raise the reserve but never lower it below the store's.
Free memory is the whole machine's, so one sample under the reserve can be another program's spike, and the run is cancelled only once free memory stays under the reserve for ten seconds.
Once the command's own process has exited, what it left running may run on for a linger of ten seconds, after which the run ends it and records each process by name and pid as a leftover, and `cfo verify` names them on standard error.
The run keeps the command's own result, since tools such as build servers and browser bridges leave helpers behind on purpose, and a later certification can still require a run with none.
Only a process still running counts, since Windows can count one that has exited in its job for a moment after.
A process a nested `cfo gate turn` lets go of when its command ends is still in the executor's job, so it is ended and named the same way.
A machine service the command started, Docker Desktop with what it runs, the no-mistakes daemon with its gate agents or the Scrawl server, serves the whole machine, so the run's end leaves it running, as every teardown of a goblin does, and records it apart from the leftovers it ended, and `cfo verify` names it on standard error.
The run's cleanup is confirmed once nothing but such a service runs in its job, by the job's own list of its processes, where a process the run may not open counts as running.
Only then does the job stop ending what it holds when its handle closes, so a controller that dies first, or an end that fails, still ends everything in it.
A recovery after a controller died spares nothing, since that controller's job ended everything in it when its handle closed.
The command's output to its logs is limited to 1 GiB together, past which the run is interrupted, and a running sample is written to the record every ten seconds and the final measurement always.
A run whose command writes to its caller's own output, as `cfo gate turn`'s does, keeps no logs and has no such limit.
Until the command's process exits the monitor still waits for the whole job, and it samples again as soon as that process exits rather than at its next poll.
`RunWithin` bounds a command from the moment it starts, so its wait in line never counts against that limit, and the limit stops once the command's own process exits, so what it left is judged by the linger alone.
A command its limit ends fails with `verify.ErrLimit` and an interrupted record, which a tier records as timed out, while an internal wait that ran out of time is a failure but no timeout.
A failed cleanup returns `cleanup_uncertain`, so its caller must retain admission custody, and successful cleanup does not turn an interrupted run into a completed one.
`internal/verify.Executor` composes admission, per-command worker custody, job measurement and durable lifecycle records on Windows.
Records under the report store's `executions` folder separate queue time from execution time, preserve the command's actual exit code when available, and require complete measurements and cleanup before recording a pass.
The record names its complete stdout and stderr logs, while the returned command result holds at most the last 4 MiB of each stream.
Input is an inherited file handle, so an arbitrary reader cannot leave a blocked I/O pump behind after the command exits, and a command given none reads an empty input.
Interruption requests Ctrl+Break only for the command's own process group, then performs bounded whole-job cleanup even when no shared console can deliver that request.
A command that shares its caller's console group, as `cfo gate turn`'s does, is sent no Ctrl+Break, since that event reaches only a whole group, its caller's included: an interrupt reaches it from the console itself, and pressure ends its job at once.
Each custody record prevents slot release until its execution confirms cleanup, including when a parent wrapper tries to release a nested command's turn.
A custody record names its execution's Windows job and the process that controls the execution, and it is written before the job or any worker exists.
The job is named `Global\cfo-verify-` followed by the execution's random run ID, in the machine's global namespace so a run in any logon session finds it, its creation refuses a name that already exists, and no child inherits its handle.
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
A failed mutation-lock release retains its exact lease identity for a later retry, and cannot reclaim a changed or foreign live owner.
Stale lease removal claims the record against writes and replacement, validates its identity while claimed, and deletes through that same Windows handle.
Mutation-lease creation excludes readers until the writer closes, so contenders cannot mistake an in-progress publication for a corrupt record.
An interrupted publication that leaves an incomplete record still refuses admission.
Recovery preserves failure status, and an executor with pending cleanup must not be discarded.
`cfo verify` and `cfo security` run each command of a tier through this executor, as docs/project-runtime.md says.
They have it read the source the command runs on, once its turn is taken and before it starts and again once it has ended, and the record keeps both reads under `source`.
`cfo gate test` reads the source of each of its checks the same way and keeps the reads in its report.
A read is the commit checked out, its own tree, and the git tree of the files git does not ignore as they were on disk, whatever the worktree's own index held.
So a commit of exactly those files has that tree, and a worktree that matches its commit has the commit's own.
The read stages them into an index and an object store of its own, which it removes after, so it leaves the worktree's index as it was and writes nothing into the repository.
It inherits none of the git variables that name another repository, index or object store, as a git hook or a gate daemon sets them, and it sets aside a file system monitor and `core.ignoreStat`.
It is incomplete when the tree does not hold all of those files as they were: a submodule with changes of its own, which a tree records only by the commit it is at, or one not checked out, a file git leaves unstaged, as a sparse checkout leaves the files it keeps off the disk, or a file that changed while the tree was read.
The tree's content is git's, after line-ending conversion and clean filters, with this machine's `core.filemode=false` a file's mode is its commit's, and on this case-insensitive disk a rename that changes only a name's case keeps the commit's spelling, so a check that depends on line endings, modes or the case of a name can differ from what the tree says.
Ignored files are inputs too, such as installed dependencies or the built board `internal/boardweb` embeds, and are no part of a source, so a source is not all of what a check ran on.
A command tested one source only when both reads are the same complete source, which says only that the two reads agree: a file changed and changed back between them goes unseen.
One whose reads differed, or could not be read, keeps its own result, and the tier or the gate step says on standard error that it tested no one source.
A read is held within the check's turn, outside its time against a budget, and is bounded at two minutes, at which it ends with what its git started.
After each tier, and after each `cfo gate turn` run, the executions store keeps the newest 100 passes, 50 failures and 20 runs that never started, each kept apart, so a burst of one never evicts the evidence of another, and apart from them the newest 200 runs that kept no logs, as `cfo gate turn`'s, whose output their caller keeps.
What it counts fits 2 GiB with its logs, the oldest passes going first, then runs that never started, then failures, but never past the newest pass and the newest five failures, so a run whose own logs overran the budget keeps its evidence.
A record whose custody is still held, one still pending, and one it cannot read or that a newer cfo wrote, while younger than a week, is never removed or counted, so the 2 GiB covers only what it counts.
A record a kept pass rests on, while that pass may still be taken again within its week, is counted and never removed, the newest passes first and only within half the 2 GiB, so passes never crowd out the evidence of failures, and a pass past that half is taken again only while its records outlive the budget.
A pass that cannot be read stops the round from removing anything, since it may rest on any record, and `cfo verify` prunes once its tier's own pass is kept, so that pass pins its records first.
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
It has no budget and no timeout, no disk floor, which `cfo gate test` keeps for itself, and no report file, since its caller keeps the log.

**A gate behind it.**
A gate puts every run's test command behind the line, with no repository naming it, by starting the command behind four words: `cfo gate turn --`, then exactly the program and arguments it starts today.
On this machine that is `cfo gate turn -- cmd.exe /c <command string>`, started as an argument list with no shell in front, with `--as` before the `--` when the gate names its run.
The words go in front of the whole command as a list and are never joined to the command string, where `a && b` would put only `a` behind the turn.
This is the contract the no-mistakes fork's wrapper setting is built against.
The fork's side, as task cg-gate-fork-minutes built it, is a setting in the machine's `~/.no-mistakes/config.yaml`, on since the installed `cfo` has `cfo gate turn`, as an older `cfo` refuses `gate turn` with exit 2, which a gate reads as a failed test command:

```yaml
test_command_wrapper:
  command: ["C:\\dev\\code-goblins\\bin\\cfo.exe", "gate", "turn", "--as", "{repo} {branch}, run {run_short}", "--"]
  refusal_prefix: "cfo gate turn: "
```

The fork fills `{repo}`, `{branch}` and `{run_short}` once, then starts nine arguments with no shell in front:

```text
C:\dev\code-goblins\bin\cfo.exe  gate  turn  --as  "PrecisionDocs feat/x, run 01M4FG7F"  --  C:\WINDOWS\system32\cmd.exe  /c  <the command string, as one argument>
```

The full path to `cfo.exe` keeps the gate daemon's `PATH` from choosing which `cfo` runs.
It stands every run's `commands.test` behind the line, each machine-local additional test check, and the re-run of `commands.test` on the base commit, and not prepare, lint, format or a repository gate.
An exit of 125 or 126 whose last line that is not empty starts with `refusal_prefix`, or a wrapper that cannot be started at all, parks the test step with one `test-command-no-turn` finding: no fix agent is asked, fix runs the test command again, and approve goes on without the baseline.
So this repository's own `cfo gate test` runs behind `cfo gate turn` under a gate, inside the turn its wrapper holds.

A repository whose `.no-mistakes.yaml` names no `commands.test` gives the fork nothing to wrap: its test step logs `no test command configured` and asks the test agent to run the tests, which it does outside the line.
So every gated repository names its suite there, on its default branch.
The string runs as `cmd.exe /c <string>`, so it is in `cmd.exe` syntax, with `&&` between commands.
A gate's worktree holds tracked files only, so `commands.prepare` installs what the suite needs, outside the line:

```yaml
commands:
  prepare: "pnpm install --frozen-lockfile"
  test: "pnpm run build && pnpm test"
```

What the test command leaves running is ended ten seconds after it exits, and after a baseline that passed the test agent still gathers live evidence, outside the line.

### A desktop CI job in the line

A machine with the desktop CI runner installed (`cg-ci-runner`, a Windows service the Supreme Overlord installs from a signed release) runs one GitHub Actions job at a time for one repository, in a Hyper-V VM put back to a clean checkpoint before every job.
The service holds the repository's GitHub App key and drives the VM, and it runs a job only when the fleet asks, on its named pipe, which only the fleet's Windows user, SYSTEM and Administrators may open.
Every 30 seconds the watcher asks it whether a job of its labels waits, and starts one turn for it, at the lowest priority, so it never goes ahead of a run that waits, and expected to use 7 GiB, the VM's 6 GB at most with its Hyper-V worker and the job's own process, which the line weighs since the VM's memory is the worker's, outside the turn:

```
cfo gate turn --low-priority --memory 7GiB --as "desktop CI: <job> of run <id>" --detach <state>\ci-runner\job-<run>-<time>.log -- <this cfo> ci-runner job
```

No other job's turn starts while that run lives.
`cfo ci-runner job` holds the pipe open for the length of the job, so the turn's end, however it comes, ends the job, and the service then turns the VM off, puts it back to clean and deletes the job's runner.
It exits 0 when the job ran, 3 when no job came for the runner and 1 otherwise.
A machine with no runner installed is asked, finds no pipe, and says nothing, and trouble with a runner that is installed is said once an hour in `<state>\ci-runner\watch.log`.
`cfo ci-runner status` says whether a job runs and how the last one ended.

## Before a push

`cfo gate prepush` is what a goblin runs before each push, so that CI is not the first to run a test the change broke.
From 2026-09-05 to 2026-10-10, 164 tests failed on pull request runs of the `go` workflow.
Of them 52 percent were in a test file the pull request had changed and 23 percent in another file of a package it had changed.
Another 23 percent were in a package it had not touched: most of those are tests that fail by chance, and the rest are guards that read the whole tree.
Those guards failed eight pull requests in all, counting the ones in a package the pull request had changed.
Until then every brief said to run the touched tests by name and let CI run the rest, so a goblin guessed which tests its change could break, and each wrong guess cost a red run, a second push and about 20 minutes.

The pick is the gate step's plan and more, in the order a failure is likeliest and cheapest to find:

1. The imports of every package the change reaches and of its tests, by `go list -test`, which builds nothing, takes seconds and is where an import cycle shows.
2. The guards: the tests the policy names under `guards` in `config/verify.json`. They read the whole tree, as the two that read every command `main.go` dispatches do, so a change anywhere can fail them, and they run whatever changed. A new whole-tree guard is named there. A test of the policy fails a guard whose test is gone, since it would run nothing and pass.
3. The changed packages that are quick to test, and the packages a contract names for a changed file, each whole.
4. For a change under `frontend`, the type check, the lint, the unit tests and the browser specs the change touched or names.
5. The changed packages the policy lists as slow, without their slowest tests. They come after the board's checks because a busy machine can spend the whole limit in them: on 2026-10-10 the replays of two pull requests were ended by the limit inside the quick tests of `cmd/cfo` and `internal/supervisor`.
6. `go vet` of every package the change reaches, which is where a test of an importer that no longer builds shows. It comes after the changed packages because each of those is built and vetted by its own tests. As the first check it took 7 minutes of one replay on a busy machine to report an import cycle that `go list` reports in 14 seconds, and another replay was ended by its limit in the seventh of its 22 checks.
7. The packages that import a changed one, nearest first.

A browser spec is picked when it changed, when it opens a fixture page that changed, when it imports a changed file beside the specs, or when it shares a word of its name with a changed source, as `run-terminal.spec.ts` does with `RunCard.tsx`.
Most specs open the whole board, so what a spec imports does not say what it covers, and its name is what there is to go by.
Where `frontend/node_modules` is missing the run starts none of the board's checks and says so, because npm can exit 0 where it found no program to run.

A package the policy lists as slow runs without its slowest tests.
The run reads this machine's own record of the shortest time each test has passed in, under `cfo\verify\times` in the user's cache folder, and leaves out, with `go test -skip`, the tests the record has at 2 seconds or longer.
The record keeps a test's fastest pass because a test left out is not run and so is never timed again: in two hours of replays on a busy machine 13 tests of `internal/supervisor` and 3 of `cmd/cfo` passed in over 2 seconds that had passed in under 2 before, and a record of the latest time would have left each of them out from then on.
A test that grows slower keeps its old time and keeps running, which costs time and loses nothing.
On 2026-10-07 and 2026-10-08 the tests under 2 seconds were 81 to 88 percent of the tests of `cmd/cfo` and `internal/supervisor` and 9 to 28 percent of their time.
A test in a test file the change touched always runs, slow or not.
A slow test in the test file named for a changed source, as `spawn_test.go` is for `spawn.go`, runs when the record has it under 15 seconds.
From 2026-09-05 to 2026-10-10 sixteen slow tests failed a pull request from a test file it had not touched, in a package it had changed.
Five of them, in four pull requests, were in the test file named for a source the pull request changed, and each took under 15 seconds here.
Two were the tests of PR 613 that the pick would otherwise have left to CI.
The slower tests beside a changed source stay left to CI: counting them all as touched ran 37 slow tests, seven minutes of the limit, for a change to one file of `cmd/cfo`.
A slow package this machine has not timed runs whole when it changed, which is what times it, and is left to CI when it only imports the change.
Every `cfo gate prepush` run and every `cfo gate test` run adds what it timed to the record.

The run fits the machine.
It waits for the machine's turn in the line `cfo gate test` runs wait in, so the two never run together, and it tests one package at a time.
It starts a check only while free memory and commit are over the 4 GB floor, and none after its time limit, which is 15 minutes of running unless `--limit` says otherwise.
Beside a working fleet free memory dips under the floor for seconds at a time, so a run waits for the floor, says once what it waits for, and leaves the rest to CI only when the limit passes first or memory cannot be read.
A run that gave up at the first low reading ran 9 of its 42 checks on 2026-10-10 and left the two changed slow packages to CI.
A check still running at the limit is ended, which is no failure.
It is ended with everything it started, since each check runs in a Windows job of its own that every process it starts joins and none can leave, a test's helper whose parent already exited included, and the run names each process that ended by its name and pid:

```text
cfo gate prepush: the time limit ended tests of internal/supervisor and these processes it ran: go.exe pid 4242, supervisor.test.exe pid 4343, supervisor.test.exe pid 4404
```

Ending a check with what its parents still reached left that helper running on 2026-10-12, a `supervisor.test.exe` whose test process had already ended.
The job ends with the run too, so a run that is itself ended, as a goblin's tool ends it, leaves nothing of its checks running, and so do the checks of a `cfo gate test` a gate's time limit ends.
A machine service a check started, such as Docker Desktop, is spared when the run ends a check, as a goblin's terminal spares it.
Whatever it did not run it names under `left to CI`, each with why: the slow tests by number and time, the other browser specs, the board's build, and the checks the limit or the memory cut.
CI is the check of those: a pull request's own run starts every job the change can alter, and the merge train's run that lands it starts them all.
That is one more reason the guards run here whatever changed: a pull request's own run starts no job for a package its change does not reach, so a guard in such a package would first fail in the merge train.

It prints the pick before it runs anything, and `--plan` prints it with each check's command and runs nothing.
This is the pick for a change to three files of `internal/supervisor`:

```text
cfo gate prepush: 3 files changed since 3301d7c9, which picks 10 checks to run within 15m0s
1. imports of 5 packages and their tests (an import cycle fails every test of its package)
2. guard tests of the root package (its tests read every link of the contract, the workflows and the shipped skills, and every tracked file for a real tailnet's name)
3. guard tests of cmd/cfo (they read every command main.go dispatches)
   - TestEveryCommandSaysWhetherItActsAsTheCFO
   - TestEveryCommandIsAControlOrStaysAtNormalOnPurpose
4. guard tests of internal/gatetest (they read every tracked file against this policy)
   - TestTheRepositoryPolicyAccountsForEveryTrackedFile
   - TestTheRepositoryPolicyNamesOnlyFieldsAndPackagesThatExist
5. tests of internal/execx (the read and process-start guards scan every Go file: internal/supervisor/error_wakes.go and 2 more)
6. tests of internal/fsx (the read and process-start guards scan every Go file: internal/supervisor/error_wakes.go and 2 more)
7. tests of internal/supervisor but for 179 slower ones (changed)
8. go vet of 5 packages (they build against what changed)
9. tests of internal/digest (imports internal/supervisor)
10. tests of cmd/cfo but for 152 slower ones (imports internal/supervisor)
left to CI:
- 179 tests of internal/supervisor (they take 2s or longer each here, 29m2s in all, and are in files the change did not touch)
- 152 tests of cmd/cfo (they take 2s or longer each here, 1h10m23s in all)
policy: config/verify.json version 1 at 3301d7c9
```

The first check that fails ends the run with exit 1, and its last line names the check and the tests:

```text
cfo gate prepush: failed at check 7 of 10, tests of internal/supervisor but for 179 slower ones: TestOverlapReadThatKeepsFailingWakesTheCFOOnTheThirdPassInARow failed. CI would fail on it too, so fix it before you push.
```

A test that fails runs once more by itself before it fails the push.
A test that waits on a real terminal can miss its own deadline while another run holds this machine's processors, which the change did not cause and CI would not see, so a test that passes by itself is named as that and the run goes on.
That second run has five minutes of its own, past the limit when the limit ended the check, so whether a test failed never depends on where the limit fell.
In a replay on a busy machine four tests that wait for a terminal's host failed beside the one test the change had broken, the limit ended the check, and without a second run all five were named as failures CI would repeat.
A test whose second run does not end in its five minutes fails the push, in a line that says it failed and never passed.

A run in which nothing failed exits 0 and says how many of the picked checks ran.
A pick that holds nothing this machine can run, such as a change to the board where the board is not installed, exits 0 and says that no check ran here and how many it left to CI.
A run the machine gave no turn says that nothing ran and exits 1.

`cfo verify <task>` runs this as the project's fast tier, by `verification.fast` in `data/projects/code-goblins/project.json`, so a goblin and the CFO run the same check by the same command.
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

`cfo doctor` separates validation invocation timing by harness, recorded model, role, step and outcome.
Successful calls, errors and cancellations never share a timing row.
Error and cancellation durations measure failure latency, not successful speed.
Spawn hints show outcome counts and label implementation unmeasured, because the native database observes validation agents rather than the implementation session.
Missing or unreadable telemetry stays an explicit unavailable result.
No implementation-speed comparison can be inferred from these gate timings.
