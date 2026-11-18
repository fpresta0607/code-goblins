# goblins fleet-view

`goblins fleet-view [--json] [--name <name>]`

Typed fleet snapshot (under way / queued / done).
Each goblin under way, paused, queued or finished carries the name and title the board shows for it and its task's title, as `Name - Title (id)` in the Markdown and `goblin_name`, `goblin_title` and `title` in the JSON.
A queued task's name is the one the board gave it, read from the board's store, `state/.goblin-names-queued.json`.

`--name <name>` finds a goblin's task by the name the board calls it, ignoring case.
It prints each task that held the name, newest first: a queued one, then one under way or paused, then finished ones, the last to finish first.
Each row gives the task id, the goblin's name and title, its state, project, row title and what it waits on, and `--json` prints the same rows as JSON.
A name no task holds prints that no goblin has that name and exits 1.
The lookup only reads: it never assigns, renames or writes a name.
