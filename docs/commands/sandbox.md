# cfo sandbox

`cfo sandbox run -- <program> [<argument>...]`

Run one command of a task whose brief carries `sandbox: needed` in a container, as [Untrusted code](../contract/authentication.md#untrusted-code) says.
It runs from the task's terminal, which names the task in `CFO_TASK_ID`, from a folder of its worktree.
The container runs the task's image, the `image` its project's devcontainer named at the spawn.
It mounts the worktree at `/work`, with its `.git` link read-only, and the task's scratch folder at `/scratch`, which `TMPDIR` names, and nothing else of the host.
It is given none of the host's variables, so no credential reaches it, and it keeps no way to gain privileges.
The command runs in the folder of `/work` that matches the shell's, with the arguments after `--` unparsed, so a shell's command string is given as `sh -c '<command>'`.
The run takes its turn in the machine's heavy line as [`cfo gate turn`](gate/turn.md) runs a command, one at a time on the machine, with the docker client tracked and ended by pid.
Once the run ended, interrupted or not, the container is removed, which a `docker run` that was itself ended would otherwise leave running.
The exit code is the command's.
Started in the background, it is `cfo gate turn --detach <log-file> -- cfo sandbox run -- <command>`.

Exit 125, with a last line on standard error that starts with `cfo sandbox run: the command ran nowhere:`, means it ran nothing.
That happens when the terminal names no task, the task's brief carries no `sandbox: needed`, the worktree's `.git` link is not the one its spawn recorded, the folder it runs from, or one between that folder and the worktree, holds a `.git` of its own, or Docker is not running and cannot be started.
It never runs the command on the host instead.
A changed `.git` link is the CEO's to look at: the goblin reports it as blocked and never repairs it.
