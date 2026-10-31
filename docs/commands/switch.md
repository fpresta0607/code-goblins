# cfo switch

`cfo switch <id> [--harness <h>] [--model <m>] [--effort <e>] [--force-dirty] [--restart]`

Change a running goblin's harness, model, or effort in place: same id, same worktree, a new native terminal under the same id.
Stops the old harness on its own terms, then relaunches.
A model-or-effort-only change resumes the harness's own session where the harness has one; otherwise it writes a handoff note and points the new harness at it.
`--restart`, with none of the others, starts the goblin's own harness, model and effort again the same way, onto an update of its harness installed since it started.
Refuses a dirty worktree unless `--force-dirty`.
Answers Claude's "Background work is running" menu with Exit and stop tasks.
A harness that will not exit has its terminal closed, which ends what it started in the terminal, before the new one starts.
What the last harness left running outside its terminal, a dev server, a watcher or a browser bridge, is ended before the new one starts too, at a switch and at a resume alike, and the command names each one: the new harness cannot reach what the last one left in the background.
A goblin whose terminal has ended, as a reboot ends them all, is resumed in place by a switch to what it already runs, even over a dirty worktree.
A task recorded in Herdr by an older build is refused (see docs/native-board.md)
