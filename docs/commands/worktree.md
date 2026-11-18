# goblins worktree

`goblins worktree add <id> <name> [--ref <commit>] [--project <name|path>]` \| `remove <id> <name> [--project <name|path>]`

Give a task a second worktree of its project, `<id>-<name>` beside its own in the home, detached at origin's default branch or `--ref`, and record it on the task, so `goblins cleanup` removes it with the task; `remove` returns one early, refusing one with uncommitted work and keeping work not on the default branch as a local archive tag.
With `--project <name|path>` the extra is a worktree of that project instead, in its folder of the home's worktrees, with `--ref` a commit of it, for a task whose work spans a second repository; `goblins cleanup` returns it to that project, keeping its unlanded work there, and `remove` takes the same `--project`.
A worktree made any other way is a stray the janitor reports
