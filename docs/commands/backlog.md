# goblins backlog

`goblins backlog`

done <id>   close a retired delivered task's queued row, preserving its evidence and continuation under Done

accept <id> --generation <generation> --report <file> --receipt <file>   the registered CEO accepts one generation of a task as delivered, such as an installation that opens no pull request

review   say which queued rows, parked rows and paused tasks need a decision, each with its evidence and a proposal:
  - `done already`: every issue the work names is closed and every pull request it names merged, so the row or the paused task can be marked done or retired with that evidence.
  - `overtaken`: a step only the Overlord or the CEO takes has an issue closed, a pull request merged or a date passed since it was written, so the step can be closed or say what it still waits on.
  - `fold`: queued rows and paused tasks of one repository that name an issue still open in common, or touch the same path, so the later ones can fold into the first, or into the paused goblin.
  - `stale`: a paused task or a held or parked row with no dated condition whose last activity on record is older than the stale threshold, so the owner is asked once whether to resume, fold or retire it; one with no activity on record is not stale.

`review` reads the backlog, the briefs and the task records, and asks GitHub one query for each repository for the state of every issue and pull request they name.
It spends no model tokens and changes nothing.
`stale_work_days` in `config/fleet.json` sets the stale threshold in days, 7 unless set.
What it could not read, such as a repository GitHub would not answer for, is listed as `not read`, and the work that names its issues or pull requests, with the work that could fold with it, is left out of that review.

## Accepting a delivery

A task that installed something, with no delivery of its own to report such as a pull request or a local-only task's kept report, is retired by cleanup as stopped, and every task that waits on it keeps waiting.
`accept` records that one generation of it delivered, which is a fact of its own: it changes neither the task's record nor the outcome that says how it was retired.
Only the registered primary CEO runs it, never a goblin, a helper or a gate agent.

`--generation` is the generation the task stands at: `spawn_gen` in `state/<id>.meta` while the task is live, and `generation` in `state/outcomes/<id>.json` once it is cleaned up.
`--report` is the saved acceptance report and `--receipt` the saved installation receipt, each a file in the home's `data` folder.
The receipt is this JSON and no other field:

```json
{
  "task": "<id>",
  "generation": "<generation>",
  "image": {"path": "<full path of the installed program>", "sha256": "<its SHA-256, in lowercase hex>"},
  "daemon": {"pid": 1234, "started": "2026-11-26T09:00:00Z"}
}
```

The command accepts it only once the receipt names that task and generation, the installed program holds the receipt's SHA-256, and the process of the daemon's `pid`, created at `started`, runs a program with those same bytes.
Anything else is refused and records nothing.

The accepted delivery is kept in `state/accepted-deliveries/<id>.json` with the report's SHA-256, the receipt's identity and who accepted it.
Every wait on the task then clears: a `blocked-by` on a queued row, a pause until the task and a prepared wait.
It counts only for its own generation, so a task resumed, switched or started again since waits to be accepted again.
Before cleanup, cleanup keeps it and closes the row as `done`; after cleanup, `accept` closes the row itself.
Giving the same evidence again changes nothing and reads nothing from the machine.
Other evidence for an accepted generation is refused, and a record that cannot be read is left as it is and said on whatever waits on it.

Read the [applicable dispatch procedure](../contract/dispatch.md) before acting.
