# cfo pipeline

`cfo pipeline config-drift \| config-apply \| migrate <id> \| run <id> [--branch <b>] --intent-file <file> \| respond <id> [--branch <b> \| --run <run>] --action <fix\|approve> [--findings <ids>] [--instructions <text>] [--accept <ids>] [--reason <text>] \| recover <id> [--branch <b> \| --run <run>] \| handback <id> [--branch <b> \| --run <run>] [--revalidate-publication] [--resume --receipt <receipt>]`

`cfo pipeline terminal-preflight <id> --run <predecessor>`

`cfo pipeline validate-authored <id> [--branch <b>] --admission-file <file>`

`cfo pipeline validate-terminal <id> [--branch <b>] --admission-file <file>`

Drive a gated task under its frozen policy: report or apply the owned shared no-mistakes settings in an explicit idle window, whose refusal names the gate runs it waits for, explicitly migrate a task's frozen snapshot to the current policy the shared config already runs while preserving its class and repair cap, which never needs the daemon stopped, validate a bounded gate start under the version-specific contract, taking the run's intent from the file `--intent-file` names (a short one may be given with `--intent <text>`) and handing it to no-mistakes in a file, never on a command line, where Microsoft Defender detected a long intent as a lure and Windows refused the start, answer one parked gate with an explicit fix or a no-op approval, recover eligible unpublished work without moving its local branch, or return eligible published CI failures to their contributor through [native handback](../pipeline.md#driving-a-task).
Each acts in the task's own worktree, or with `--branch` or `--run` in whichever of its worktrees, an extra one `cfo worktree add` recorded included, has that branch checked out; a branch or run none of the task's worktrees has is refused.
For `respond`, `recover` and `handback`, a selected run must be its branch's latest; preflight and admission bind an exact predecessor under the [native admission rules](../pipeline.md#driving-a-task).
For `handback`, the registered primary CEO alone may explicitly select `--revalidate-publication` to request native release of a publication advanced after Review or Test, with historical evidence pinned and fresh validation required before publication.
The default is unchanged, receipt-bound continuation retains the same run and spent allowance, and native publication or ancestry refusals are reported without retry or fallback.
For selected fixes, see the [informational finding translation contract](../pipeline.md#driving-a-task).
`--accept` with approve is the registered CEO's own decision to take open findings as they stand under the [exact-IDs rule](../pipeline.md#driving-a-task), recorded in the task's status log; the task's own goblin may make it from its own terminal on its run's review step once that run has spent its review repair cycles, recorded with the task as who accepted and told to the CEO in a notify nothing waits on, and every other goblin accept is refused with why.
A goblin takes as they stand only findings that are not correctness, security or regression defects, and a real defect among them goes in one follow-up commit on the same pull request after its first CI, named in the done report.
For a recognized native configured Test command failure with an empty action, the primary CEO must supply both `--accept` naming every open finding, including the failure, and a nonblank `--reason`.
That response binds the native approval to the recorded run and preserves the failed command as an explicit Test exception; other empty or unknown findings remain refused.
`--reason` with approve is the reason an approved Test exception keeps, handed to the native engine's own `--reason` and recorded in the task's status log; it is the registered CEO's alone, a goblin is refused, and a reason on any other gate, with a fix, or blank or of more than one line is refused, so no approval is ever inferred from one.
Under policy version 6 `run` starts the gate on the task's own harness, with that task's model and effort, as a launch selection no-mistakes proves before any agent starts, prints it on a `pipeline gate agent:` line, and names a second harness only when the operator named it as the policy's fallback and it is signed in.
A live run of the same branch, head and intent is reattached to when its bounded hold ends with no decision, so repeating the command starts no new run and resets no review budget, and none of a new start's checks, such as origin's tracking ref, stops it, while a start of other work is still refused.
Mode eligibility follows [Task classes](../pipeline.md#task-classes).
Validation admission is the registered primary CEO's decision: `validate-authored` submits a declared authored correction, while `validate-terminal` explicitly accepts an unchanged terminal candidate and selects its successor's fresh profile.
`terminal-preflight` is read-only and available to the registered primary CEO or that task's own goblin; its private observations grant no execution or completion authority.
See [terminal validation admission](../pipeline.md#driving-a-task) for the controller JSON requirements, source and policy checks, native feature dependencies, replay rules and private result records.
Never auto-approves and never skips a step; exit 3 means unresolved work, and its refusal names who decides it.
Full contract in [docs/pipeline.md](../pipeline.md)
