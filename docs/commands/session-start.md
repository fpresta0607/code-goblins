# goblins session-start

`goblins session-start`

Print the long session-start digest.
With a verified `data/memory/startup.json`, it prints exact warm ranges, each after its id, a bounded count of what each scope holds cold and a state summary using the shared current-state resolver and structured backlog task counts; retrieve relevant cold authority before dependent work with `goblins memory retrieve --cold` and the scope.
Run it again after resume or compaction to validate current authority; a saved digest is not validation.
Without a curated catalog it retains legacy authority in full and explicitly reports overruns.
The current-state projection stays bounded in either case and reuses one sample under a shared two-second terminal-read budget.
The SessionStart hook prints a brief one, within what Claude Code hands a session whole, and writes this long one to `state/session-digest.md` for the CEO to read next.
Run by hand it takes the session lock only in [the CEO's own session](../contract/supervision.md#the-ceos-own-session), and anywhere else prints the read-only digest and takes nothing
