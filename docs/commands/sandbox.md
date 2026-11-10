# ceo sandbox

`ceo sandbox run -- <program> [<argument>...]`

Run one command of a task whose brief carries `sandbox: needed` in a container, as [Credentials by need](../contract/authentication.md#credentials-by-need) says.
It runs from the task's terminal, which names the task in `CEO_TASK_ID`, from a folder of its worktree.
The container runs the task's image, the `image` its project's devcontainer named at the spawn.
It mounts the worktree at `/work`, with its `.git` link read-only, and the task's scratch folder at `/scratch`, which `TMPDIR` names, and nothing else of the host.
It is given none of the host's variables, so no credential reaches it, and it keeps no way to gain privileges.
The command runs in the folder of `/work` that matches the shell's, with the arguments after `--` unparsed, so a shell's command string is given as `sh -c '<command>'`.
The run takes its turn in the machine's heavy line as [`ceo gate turn`](gate/turn.md) runs a command, one at a time on the machine, with the docker client tracked and ended by pid.
Once the run ended, interrupted or not, the container is removed, which a `docker run` that was itself ended would otherwise leave running.
From before the container can start until it is removed, a record of it in the task's tasktmp folder, under `sandbox-runs`, holds the task's host git back, and a run first removes the container of each record whose run ended without removing its own.
The exit code is the command's.
Started in the background, it is `ceo gate turn --detach <log-file> -- ceo sandbox run -- <command>`.

Exit 125, with a last line on standard error that starts with `ceo sandbox run: the command ran nowhere:`, means it ran nothing.
That happens when the terminal names no task, the task's brief carries no `sandbox: needed`, its record names no tasktmp folder, the worktree's `.git` link is not the one its spawn recorded, the worktree holds a `.gitmodules` or its index names a submodule, which a sandbox task's project cannot use, the command runs from outside the worktree, or Docker is not running and cannot be started.
A `.git` the container made elsewhere in the worktree does not stop a run, so `ceo sandbox run -- rm -rf <path>/.git` removes it.
It never runs the command on the host instead.
A changed `.git` link is the CEO's to look at: the goblin reports it as blocked and never repairs it.
