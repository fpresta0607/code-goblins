# cfo memory

`cfo memory retrieve [--project <name>] [--task <id>] [--topic <topic>]`

Print all exact warm ranges plus cold ranges matching any supplied project, task or topic key from the verified private catalog.
`cfo memory retrieve --id <id>` prints one exact entry; it cannot be combined with scope flags.
Source provenance, checksums and stale or pending-capture diagnostics accompany the original bytes.
`cfo memory routes` lists every cold trigger and routing key without loading its body into the response.

`cfo memory budget [--packages <file>]`

Report combined startup memory or the full supplied captures for Codex, Claude Code and Pi, including core and global rules, memory, skill discovery index, task brief and emitted digest.
The [memory contract](../contract/memory.md#budgets-and-exceptions) defines package input format, allowances, estimates and explicit exceptions.
An overrun is reported in JSON and never truncates authority.

`cfo memory migration-plan`

Write a repeatable JSON dry-run inventory to standard output with source and retained hashes, byte ranges, scope, proposed destinations and per-item approval requirements.
It writes no memory files and exposes no apply operation.
Keep any redirected inventory private because its source paths and provenance belong to the home.

Every operation accepts `--data <directory>` for an explicit private data source; otherwise it uses the resolved CFO home's `data` folder.
No operation grants consent, moves history, calls a model or alters task state.
