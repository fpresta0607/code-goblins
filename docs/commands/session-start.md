# cfo session-start

`cfo session-start`

Print the long session-start digest.
The SessionStart hook prints a brief one, within what Claude Code hands a session whole, and writes this long one to `state/session-digest.md` for the CFO to read next.
Run by hand it takes the session lock only in [the CFO's own session](../contract/supervision.md#the-cfos-own-session), and anywhere else prints the read-only digest and takes nothing
