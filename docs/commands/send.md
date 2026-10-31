# cfo send

`cfo send <target> <text>`

A `<target>` is a task id or `gb-<id>`.

Steer a goblin: every steer is delivered with the `CFO: ` prefix, which the harness-fault detector ignores along with the wrapped lines under it, so what you write about a rate limit is never read as the goblin's harness hitting one; type `Overlord: ` before a line you write into a terminal yourself for the same exemption.
The text goes through the task's own native terminal: typed, submitted once its composer shows it, and reported delivered (`sent <id>`) only once the goblin has it: an idle goblin once its own native hooks report it took the prompt or, where no hook has, once its screen shows it working when it was not before; a goblin in a turn once its harness's own record of the conversation (Claude Code's transcript, the Codex rollout, pi's session) shows the text handed to its model.
Each harness holds text submitted during a turn and hands it over at the next tool call, never interrupting the call that runs, so a send to a working goblin not taken within five seconds prints `queued for <id>`, saying it lands at its next tool call, or as its turn ends if it calls no tool first, and exits 0; a prompt hook proves nothing then, since Claude Code runs it when it queues the text.
It is submitted once, so an unconfirmed send is checked with `cfo peek` and repeated deliberately rather than automatically; only a Claude Code goblin that took the Enter as a new line, leaving it in its composer over an empty row that Enter added, gets Enter again, once, as that row shows and no sooner than a second after the first, and not one that has only not taken the Enter yet, and a pi goblin that puts it back there as still starting gets Enter every second while it says so, neither of which can deliver it twice.
A queued steer is never sent again; a `/` or `$` command waits out the completion popup before Enter and is reported submitted once but unconfirmed, since `/exit` ends the harness and `/model` opens a picker, so check it with `cfo peek` rather than resending.
A task an older build recorded in Herdr, and a name that is no task, are refused by name with nothing typed

`cfo send <target> --key <key>`

Send a key: Enter, Escape, Ctrl-C, Ctrl-U
