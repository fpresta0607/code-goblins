# cfo gate turn

`cfo gate turn [--as <name>] [--wait <duration>] [--memory <size>] [--low-priority] [--detach <log-file>] -- <program> [<argument>...]`

Run any heavy command in the line `cfo gate test` and `cfo gate prepush` take their turns in, so one such run goes at a time on the machine whoever started it: another repository's test command under its gate, a goblin's own suite, or a run one of the Supreme Overlord's sessions starts.
It waits while another run holds the turn and, at the head of the line, while free physical memory or free commit is under the line's floor, with no limit of its own: the limits that end a run are its command's and its caller's.
While it waits it says on standard error, as it starts to, whenever its place in line changes and once a minute, which run holds the turn (its name or folder, its command, its task and goblin, and for how long) and where this run stands.
Then it starts the command with the arguments after `--`, unparsed and with no shell in front, and the command's standard output, standard input, working folder, environment and exit code are its own.
Every line of `cfo`'s goes to standard error and starts with `cfo gate turn: `, and with the turn free and memory to spare it writes none.
Exit 125 means the run gave no result of its command's, since it took no turn and ran nothing or it ended the command for memory, as below, and 126 that the command could not be started.
A wrapper that is ended leaves a turn the next run takes at once, and ends its command with everything the command started, since the command runs in a job of its own that nothing it starts can leave.
Once its command's own process has exited, interrupted or not, what the command left running may go on for ten seconds, after which the run ends it and names each process by name and pid, but a machine service the command started, such as Docker Desktop, is left running and named.
The run is a measured execution, kept in the report store with its wait, CPU time, memory and processes and none of its output, and while free memory or free commit stays under the desktop reserve, 2 GiB of each unless the store's `reserve.json` says otherwise, for ten seconds, it ends its command with everything the command started and exits 125 with a last line that says why, since what the command was ended at is no result of its own.
A command that waits on a no-mistakes gate (`no-mistakes axi run`, `respond` or `answer`, or `cfo pipeline run` or `respond`), named directly or in a shell's command string, takes no turn and exits 125 with a line saying to run the gate's command without `cfo gate turn`, and any heavy command beside it in a turn of its own, since the gate's test command takes its own turn and a turn held through the gate's agents' work only keeps every other run waiting.
A script the command runs is not read.
A command that takes turns itself, as `cfo gate test` and `cfo gate prepush` do, runs inside the turn its wrapper holds, which `CFO_VERIFY_TURN` names to it.
The floor is 4 GiB on a line of one slot, and on a line of two what each run holding a slot may still use, its expected memory less what it commits now, then this run's own expected memory and the desktop reserve, never under 4 GiB.
`--memory <size>`, as `--memory 7GiB`, is the most memory the run is expected to use, 2.5 GiB without it.
`--as` names the run to whoever reads the line, as a gate names its repository, branch and run.
`--wait` bounds the wait for the turn, as `--wait 30s`: a run that takes no turn within it runs nothing and exits 125, and once it has its turn the wait no longer counts.
`--low-priority` has the run wait at the lowest priority: it takes its turn only while no ordinary run waits, and an ordinary run that joins the line after it still goes first, unless the low-priority run is already taking its turn, as the desktop's CI jobs wait, and `cfo gate turns` marks it.
`--detach <log-file>` starts the same run in the background and returns at once: hidden, in a console with no window that its command shares, out of its caller's job, its output and errors appended to `<log-file>` with a last line that says how it ended, and its pid written to `<log-file>.pid`.
It is the one way a goblin starts a heavy run in the background, and the goblin of any harness is told when it ends, as a Codex or pi goblin is of its own background run.
See [docs/pipeline.md](../../pipeline.md#any-command-in-the-line)
