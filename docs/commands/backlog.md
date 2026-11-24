# goblins backlog

`goblins backlog`

done <id>   close a retired delivered task's queued row, preserving its evidence and continuation under Done

review   say which queued rows, parked rows and paused tasks need a decision, each with its evidence and a proposal:
  - `done already`: every issue the work names is closed and every pull request it names merged, so the row or the paused task can be marked done or retired with that evidence.
  - `overtaken`: a step only the Overlord or the CEO takes has an issue closed, a pull request merged or a date passed since it was written, so the step can be closed or say what it still waits on.
  - `fold`: queued rows and paused tasks of one repository that name an issue still open in common, or touch the same path, so the later ones can fold into the first, or into the paused goblin.
  - `stale`: a paused task or a held or parked row with no dated condition whose last activity on record is older than the stale threshold, so the owner is asked once whether to resume, fold or retire it; one with no activity on record is not stale.

`review` reads the backlog, the briefs and the task records, and asks GitHub one query for each repository for the state of every issue and pull request they name.
It spends no model tokens and changes nothing.
`stale_work_days` in `config/fleet.json` sets the stale threshold in days, 7 unless set.
What it could not read, such as a repository GitHub would not answer for, is listed as `not read` and settles nothing.

Read the [applicable dispatch procedure](../contract/dispatch.md) before acting.
