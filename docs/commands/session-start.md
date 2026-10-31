# cfo session-start

`cfo session-start`

Print the long session-start digest.
With a verified `data/memory/startup.json`, it prints exact warm ranges, bounded cold routes and a state summary using the shared current-state resolver and structured backlog task counts; retrieve relevant cold authority before dependent work with `cfo memory retrieve`.
Run it again after resume or compaction to validate current authority; a saved digest is not validation.
Without a curated catalog it retains legacy authority in full and explicitly reports overruns.
The SessionStart hook prints a brief one, within what Claude Code hands a session whole, and writes this long one to `state/session-digest.md` for the CFO to read next.
Run by hand it takes the session lock only in [the CFO's own session](../contract/supervision.md#the-cfos-own-session), and anywhere else prints the read-only digest and takes nothing
