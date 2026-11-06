# cfo memory

`cfo memory retrieve [--project <name>] [--task <id>] [--topic <topic>]`

Print all exact warm ranges plus cold ranges matching any supplied project, task or topic key from the verified private catalog.
`cfo memory retrieve --id <id>` prints one exact entry; it cannot be combined with scope flags.
Source provenance, checksums and stale or pending-capture diagnostics accompany the original bytes.
`cfo memory routes` lists every cold trigger and routing key without loading its body into the response.

`cfo memory retrieve --search "cache port" --project <name> [--task <id>] [--topic <topic>] [--limit 5]`

Discover cold source ranges by case-insensitive whole-word keywords in their id, trigger, topics and exact body.
Every search word must match; supplied scope keys restrict candidates together.
The limit is 1 to 50 and applies only to cold candidates; warm authority is always selected.
Completed task entries are excluded from keyword discovery unless `--include-completed` or their exact `--task` is supplied.
Exact routing without `--search` retains its existing union-of-keys behavior.
Search is discovery, never a verdict on contradictory authority or automatic supersession.
No match is explicit; use routing keys or `--id` when wording differs.

`cfo memory retrieve [scope or search flags] --seen <file>`

The file is a JSON array of `delivery=` SHA256 fingerprints from ranges this conversation actually read and still holds.
Do not construct it from a prior composition, a saved receipt, another conversation or a completion marker alone.
Unchanged acknowledged bodies are omitted, with their ids and current diagnostics still shown.
Catalog edits, source edits, renames and deletions invalidate the old snapshot; reloading changed metadata generates different fingerprints.
The command checks current catalog and source hashes before and after rendering, stores no read marker and writes no receipt automatically.
An exact `--id` always replays its original bytes, even with `--seen`.

`cfo memory budget [--packages <file>]`

Report combined startup memory or the full supplied captures for Codex, Claude Code and Pi, including core and global rules, memory, skill discovery index, task brief and emitted digest.
The [memory contract](../contract/memory.md#budgets-and-exceptions) defines package input format, allowances, estimates and explicit exceptions.
An overrun is reported in JSON and never truncates authority.

`cfo memory migration-plan`

Write a repeatable JSON dry-run inventory to standard output with source and retained hashes, byte ranges, scope, proposed destinations and per-item approval requirements.
It writes no memory files and exposes no apply operation.
Keep any redirected inventory private because its source paths and provenance belong to the home.

`cfo memory retention-plan --manifest <file> --data <directory>`

Read a JSON array of proposals with `path`, `destination`, `sha256`, `kind` (`summary`, `index`, `artifact` or `authority`), `task_state` (`active`, `held` or `completed`), `is_held`, `is_landed` and `is_capture_complete`.
Source and destination paths are relative to the supplied private data directory, including subfolders; links outside it are refused.
Each item reports bytes, the proposed hash, current source hash, recovery hash and a reason to retain it.
A completed, landed, captured proposal requires an existing independent byte-identical local copy before it can report local recoverability.
Authority, held, active, unlanded and incompletely captured items remain protected.
Task state and capture fields are operator declarations, not verified custody or removal permission.
Every plan is read-only, reports zero reclaimed bytes and no remote verification, and exposes no apply operation.
The existing janitor retains its own custody rules; this command neither runs nor changes cleanup.
For a future remote offload, separately verify the repository's privacy, exact remote object and durable capture before seeking the owning process's removal approval.

Upgrade through the normal signed release or source installation procedure; no index, database or catalog migration is required.
Existing version-1 catalogs remain valid, and `task_state` is optional.
Before rolling back to an older binary, remove only the new optional `task_state` metadata from a backed-up catalog, since older decoders reject unknown fields; keep all canonical source bytes and original approvals.
Discard caller-created delivery lists across rollback or conversation reset.

Every operation accepts `--data <directory>` for an explicit private data source; otherwise it uses the resolved CFO home's `data` folder.
No operation grants consent, moves history, calls a model or alters task state.
