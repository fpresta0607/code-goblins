# cfo cleanup

`cfo cleanup <id>`

Close the task tab and return a finished, proven-inactive task's worktrees and scratch: its worktree (in the home, or where its record says for a task an older build spawned), every extra worktree it recorded with `cfo worktree add`, and its scratch folder are removed and their git administrative entries pruned, and the worktrees the project registers inside those folders, which the task's own harness left there, are returned with them and named in its output, and work that is not on the default branch is first kept as a local tag, `archive/<branch>`.
A task whose gate run is still open is refused before anything changes, as a paused goblin's is, since a pause of any reason leaves its run running, and so is one whose gate cannot be read, which is never taken for one with no run.
`cfo kill <id>` aborts that run after keeping its commits and retires the task.
While the task's worktree is gone or no longer a Git worktree, `cfo kill` cannot find the run (issue #2246), so the refusal also names `no-mistakes axi abort --run <run>` with the refused run's id, after which the cleanup is run again.
A paused task's run is the one its pause saved, read by its id whatever state the task's worktree is in, so `--force-archive`, for a worktree that cannot be validated, and the cleanup of an empty folder refuse it the same way.
The run of the worktree's branch is read as well while that folder is still a Git worktree, since a goblin that runs again can start a newer run there, and either run still open refuses.
A worktree already gone, or a folder no longer a Git worktree, names no branch, so a task with no saved run retires without its run being read.
A task whose project checkout is gone names no run either way, as its kill aborts none (issue #1578), so the kill's own retirement goes through.
A task any of whose worktrees holds uncommitted work is refused before anything changes, never destroyed. cfo removes a worktree's files itself, by the removal every goblin folder goes through, which removes a link as a link and never walks into what it points at: a junction pnpm makes in `node_modules`, which can lead into the store every project on the machine shares, or one into the checkout.
A path past 260 characters and a read-only file go the same way.
It never runs `git worktree remove`, because Git for Windows takes a junction for a folder: it deletes what the junction points at, and it stopped with "Directory not empty" at a junction its own deletion had left dangling, having already deleted the worktree's `.git` link and its record of the worktree.
The `.git` link goes last, so a removal that stops, as one does at a file a running program still holds, puts back the tracked files it took and leaves the clean worktree git still knows.
A removal another process's hold stopped retires the task all the same, since its work is kept by then, and the output names the worktree it left for the janitor, which returns it once nothing holds it.
Any other stop refuses, and running `cfo cleanup` again finishes it.
A recorded worktree that is an empty folder, no Git worktree at all, holds no work, so the folder is removed and the task retires the same way.
One another process holds, as its working directory, is left for the janitor the same way, and git's record of the worktree it was is pruned, so the janitor removes it.
A native task has no tab: it is returned once its terminal has ended, or while its harness waits at its ready composer with no turn in progress, and then its terminal is closed, which ends the harness.
What the goblin left running outside its terminal, a dev server, a watcher or a browser bridge, is ended next, before any worktree is returned, by the rule a pause proves a process the task's own with ([Pausing and automatic resume](../contract/dispatch.md#pausing-and-automatic-resume)), and the command names each one it ended.
What somebody else runs in the task's folders, under a program of theirs that still runs, is left.
Once its record is retired it releases every local services stack the task held, and the last holder's release stops what cfo started of it ([Project local services](../contract/services.md#project-local-services)).
It holds the task against every other command for as long as it runs: a `cfo pause`, `cfo resume`, `cfo kill`, `cfo switch` or the scheduler's own resume of the task that starts meanwhile is refused before it changes anything, with one line saying the task is being cleaned up, and a cleanup that finds one of those in flight is refused before it removes anything, to be run again once that has finished.
Whichever of the two started first goes on.
`cfo kill` runs this cleanup itself as its last step, under the hold its stop already has, after that stop has aborted any gate run the task had open.
