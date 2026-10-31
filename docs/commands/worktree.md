# cfo worktree

`cfo worktree add <id> <name> [--ref <commit>]` \| `remove <id> <name>`

Give a task a second worktree of its project, `<id>-<name>` beside its own in the home, detached at origin's default branch or `--ref`, and record it on the task, so `cfo cleanup` removes it with the task; `remove` returns one early, refusing one with uncommitted work and keeping work not on the default branch as a local archive tag.
A worktree made any other way is a stray the janitor reports
