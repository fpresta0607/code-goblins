# cfo hook

`cfo hook <name>`

Claude Code hook entry points (session-start, pretool-bash, pretool-powershell, pretool-subagent, turnend-guard, stop-autoarm; pretool-arm and pretool-cd each apply one of pretool-bash's two guards). pretool-bash guards a call of Claude Code's Bash tool and pretool-powershell a call of its PowerShell tool, with the same two guards read as each shell writes a command: neither runs the watcher, which the Stop hook arms, and neither relocates, since both tools keep their working directory between calls.
A Claude Code CFO's terminal starts with them, and no settings file of the user holds them.
They run without a shell: Claude Code starts the home's `cfo.exe` directly with `hook <name>`, a Bash call runs pretool-bash alone and a PowerShell call pretool-powershell alone, and pretool-subagent's matcher selects only the tools its guard can refuse, so the reading and editing tools start no hook at all.
A goblin (`CFO_ROLE=goblin`) or gate agent (`NO_MISTAKES_GATE`) leaves every hook on its environment alone, before reading anything, and so does every session outside native terminal `cfo`, such as one the Overlord opens himself; in that terminal each hook then proves it runs in [the CFO's own session](../contract/supervision.md#the-cfos-own-session) and does nothing otherwise.
No hook starts a child process on the path a tool call takes.
`pretool-subagent` also refuses a native question prompt (Claude Code's `AskUserQuestion`) in the registered primary CFO session and names `cfo question` and `cfo run-request` instead; every other session, and every goblin, keeps it
