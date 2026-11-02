# cfo runtime

`cfo runtime [--json]`

What is running on this machine and who owns it.
Every container, running or exited, grouped by owner: a live goblin's bench stack, a project's own local stack, the Overlord's, or unowned - each with the evidence behind the attribution, a restart loop named with its count, and the named volumes nothing mounts.
The local services stacks `cfo services` holds for tasks: who holds each, what cfo started of it and the memory it was measured to take.
Every listening dev server with the directory it is actually running in (read from the process itself, because a dev server's command line does not carry it), whether that directory is a live worktree, a retired one or a main checkout, and a plain verdict on stopping it.
What the machine has left: memory, commit (memory plus page file), disk, Docker's share, the WSL virtual machine's footprint, and the memory limits each running stack declared, with a Dispatch line saying whether the next goblin fits, which needs 5 GB of both memory and commit and the home's drive at or above the disk floor, or naming what is short.
What the home holds on disk by bucket (each task's worktrees and scratch, each shared cache, the binaries, state and data), what the verification store holds outside the home (its execution records and logs, kept passes and reports), or that its folder could not be named, and the janitor's last sweep: when it ran, what it removed and freed, what it reports, and the processes it ended as a terminal's own or left for you, each with its pid, its memory and its command.
Where each project deploys, read from its own manifests and credential notes, and the commands that run and tear down its stack locally.
Read-only throughout: where it finds something that should be retired it names `cfo reap` rather than acting
