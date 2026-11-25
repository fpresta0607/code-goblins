# goblins handoff

`goblins handoff <id> [--wip]` \| `handoff return <id> [--pr <url>]` \| `handoff cancel <id>`

Hands one goblin's task to a cloud session through GitHub, and takes the work back.
The cloud session needs no `goblins` and no home: it reads the packet, branches from the goblin's head, pushes to a branch of its own and opens a pull request into the goblin's branch, saying in its body what it ran and what it could not.

`handoff` refuses, changing nothing, a `local-only` task, a helper or a goblin with helpers, a task with a gate run on its branch, one already handed off or not running, a dirty worktree without `--wip`, and a branch whose ready pull request has CI still running.
Then it pauses the goblin with the `handoff` reason, which asks it for a handoff written for publication, commits the worktree with `--wip` as `wip(handoff): <id>`, pushes the branch, opens a draft pull request into the default branch or reuses the goblin's open one, and writes the handoff packet into that pull request's body between `<!-- goblins-handoff v1 -->` and `<!-- /goblins-handoff -->`.
The packet holds the goblin's handoff, the issues the task claims, the branch and its commits, the task's verification commands marked Windows-only where the Linux CI job cannot run them, and the rules for the cloud session.
It never carries the brief, the decisions or a status line: a handoff line that shares eight words in a row with a line of the brief, of a Markdown file in the task's data directory or of a status line is refused.
On every repository it passes the screen [goblins issue file](issue/file.md) applies to a public repository's issue: a refusal names the line and publishes nothing.
A goblin that wrote no handoff of its own publishes nothing either, and the task stays paused: `goblins resume` or `cancel` resumes it, and a new `handoff` then asks it again.
Each step is recorded in `state/handoffs/<id>.json`, so a failure part-way leaves the task paused and a second `handoff` goes on from the step that failed.
It prints the prompt to paste into a cloud session, and the record as JSON.

`return` finds the cloud's pull request into the goblin's branch, or takes `--pr` when there are several.
It refuses a pull request from a fork or from an author other than the account `gh` works as, a goblin of a later session running on the branch, a goblin branch that moved since the handoff, a dirty worktree, and a ready pull request with CI still running.
It fast-forwards the branch to the cloud's head, or merges it, aborting a conflict with the worktree as it was and the task still handed off, then pushes the branch, closes the draft the handoff opened for a `no-mistakes` task, and resumes the goblin with a note naming the cloud's commits and pull request.

`cancel` resumes the goblin without the cloud's work, marks the packet cancelled and closes the draft as `return` does; a pull request the handoff reused stays as it was.

A plain `goblins resume` of a handed-off task is refused, and so is `goblins pause --reason handoff`.
