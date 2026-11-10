# ceo memory

`ceo memory retrieve [--project <name>] [--task <id>] [--topic <topic>]`

Print all exact warm ranges plus cold ranges matching any supplied project, task or topic key from the verified private catalog.
`ceo memory retrieve --id <id>` prints one exact entry; it cannot be combined with scope flags.
Source provenance, checksums and stale or pending-capture diagnostics accompany the original bytes.
`ceo memory routes` lists every cold trigger and routing key without loading its body into the response.

`ceo memory retrieve --search "cache port" [--project <name>] [--task <id>] [--topic <topic>] [--limit 5] [--include-completed]`

Discover cold source ranges by case-insensitive whole-word keywords in their id, trigger, topics and exact body.
Every search word must match; supplied scope keys restrict candidates together.
The limit is 1 to 50 and applies only to cold candidates; warm authority is always selected.
Completed cold task entries are excluded from keyword discovery unless `--include-completed` or their exact `--task` is supplied.
Exact routing without `--search` retains its existing union-of-keys behavior.
Search is discovery, never a verdict on contradictory authority or automatic supersession.
No match is explicit; use routing keys or `--id` when wording differs.

`ceo memory retrieve [scope or search flags] --seen <file>`

The file is a JSON array of `delivery=` SHA256 fingerprints from ranges this conversation actually read and still holds.
Do not construct it from a prior composition, a saved receipt, another conversation or a completion marker alone.
Unchanged acknowledged bodies are omitted, with their ids and current diagnostics still shown.
Catalog edits, source edits, renames and deletions invalidate the old snapshot; reloading changed metadata generates different fingerprints.
The command checks current catalog and source hashes before and after rendering, stores no read marker and writes no receipt automatically.
An exact `--id` always replays its original bytes, even with `--seen`.

`ceo memory recall "<question>" [--project <name>] [--repo <owner/name>] [--task <id>] [--role ceo|goblin] [--since <YYYY-MM-DD>] [--until <YYYY-MM-DD>] [--history] [--limit 5]`

Rank the fleet's memory against a question and print the best items, each with its id, kind, date, source path and line and a short excerpt.
The items are every `overlord.md` ruling line under its heading, every memory fact file except the index, the `memory-archive.md` lines and each task's latest done report from the status logs and `done-archive.md`.
A ruling's id is `ruling-` and the first 12 hex digits of the SHA256 of its heading and line, so it stays the same until its words change.
A fact's id is its file name; its frontmatter `metadata` may name `projects`, `task`, `roles` and the ids it `supersedes`, and `modified` dates it, otherwise the file's own time does.
Ranking is BM25 over whole lowercase words cut to their stems, so plurals and -ed and -ing endings match, with common words and lone letters ignored.
A fact's description and a ruling's heading weigh four times its text, and an item's length counts only lightly, since a ruling is one line and a fact a whole file.
A scope flag drops items scoped to another project, repository, task or role and keeps fleet-wide ones; a date flag drops undated items.
Superseded facts and archive lines are hidden and counted unless `--history` is given.
Incidents fade: a done report, and a fact whose `metadata` says `incident: true`, counts half for every 30 days of its age when the index was built, while rulings and every other fact are rules, which never fade and are only superseded.
An excerpt is never the authority: open the source at its line to read it in full.
The supervisor holds the index, built from the memory files at its start and rebuilt within ten seconds of any of them changing, and answers recall over its pipe, with long titles cut and at most ten index problems listed.
With no supervisor running, or with `--data`, the command builds the index in its own process and says so.
A memory file that cannot be read fails the recall, one removed while the index is built is skipped, and a frontmatter that cannot be parsed is indexed as text and named as an index problem.

`ceo memory recall --eval <file> [--limit 5] [--data <directory>]`

Score a JSON array of `{"question", "project", "expect"}` cases against a freshly built index and print a JSON report with hits at one and at the limit, mean reciprocal rank, build time, query times and every miss.
An `expect` entry is an item id, or `text:` and a phrase the item's text must hold; a case is a hit when any of them ranks within the limit.

`ceo memory lesson accept <id> [--incident] [--supersedes <fact-id>]...` and `ceo memory lesson reject <id> --reason "<why>"`

Decide a lesson a done report proposed, as [`ceo notify`](notify.md) records it and `ceo drain` lists it.
The decision is the CEO's alone: a goblin, a gate agent and an agent session that is not [the CEO's own](../contract/supervision.md#the-ceos-own-session) are refused, and nothing is decided.
Accepting writes a new fact file in `data/memory`, named from the lesson's first words with a number added when that name is taken, and records the decision.
The fact is scoped to the task's repository and holds the lesson as its description and text, the status lines it cites, the task and pull request it came from and the lesson's id.
The cited lines, the goblin's own words, stand in a code block, and the file ends with an aging marker for the day it was accepted, so nothing a goblin wrote is read as the fact's tier.
Without `--incident` it is a rule, which never fades; with it, it records something that happened and fades as recall says above.
`--supersedes` names a memory fact the lesson replaces, which stays as it is and drops out of recall; a ruling is never superseded this way.
Nothing is rewritten in place, and no background process reads or rewrites memory.
When the home has a [startup catalog](../contract/memory.md#catalog-schema), the fact joins it as one cold fact routed to its repository and triggered by the lesson's words, so a CEO session's startup memory leaves it out and a retrieval for that repository carries it; an acceptance made while the catalog does not load, or one that would leave it unreadable, writes nothing.
Two decisions made at once take turns, and one that waits 30 seconds for another decides nothing.
Rejecting records the reason and writes no memory, and a lesson once decided is not decided again.
Recall has an accepted lesson within ten seconds, and the next context pack for its repository carries it.

`ceo spawn` and a harness switch write `context-pack.md` beside the brief and tell the goblin to read it.
The pack holds the rulings and current facts in scope for the task and the goblin role that recall ranks highest for the task's title and project, then for its brief, the repository's other live tasks with their branches, the last pull request their reports name and their last reports, main's commits since the task's base, and the repository's recent done reports.
Its facts lead with up to 10 lessons accepted for the task's repository, newest first and those accepted at the same moment in the order of their fact names, whatever the title and brief say, so an accepted lesson reaches the repository's next pack; every other fact is ranked by recall.
Each entry names its source and date, and rulings are quoted verbatim.
It fits a budget of 6000 estimated tokens unless `config/context-pack-budget` names another, and is cut entry by entry, never inside one.
A conversation resumed in place is given only the entries that are new or changed since the pack it holds, recorded in `context-pack.json` only once the goblin has been given it; any other start drops that record and is given the whole pack, and so is a resume whose record cannot be read, with a note saying so.
A pack whose memory cannot be read says so in its place, and a repository state that cannot be read says why.

`ceo memory budget [--packages <file>]`

Report combined startup memory or the full supplied captures for Codex, Claude Code and Pi, including core and global rules, memory, skill discovery index, task brief and emitted digest.
The [memory contract](../contract/memory.md#budgets-and-exceptions) defines package input format, allowances, estimates and explicit exceptions.
An overrun is reported in JSON and never truncates authority.

`ceo memory migration-plan`

Write a repeatable JSON dry-run inventory to standard output with source and retained hashes, byte ranges, scope, proposed destinations and per-item approval requirements.
It writes no memory files and exposes no apply operation.
Keep any redirected inventory private because its source paths and provenance belong to the home.

`ceo memory retention-plan --manifest <file> --data <directory>`

Read a JSON array of proposals with `path`, `destination`, `sha256`, `kind` (`summary`, `index`, `artifact` or `authority`), `task_state` (`active`, `held` or `completed`), `is_held`, `is_landed` and `is_capture_complete`.
Source and destination paths are relative to the supplied private data directory, including subfolders; links outside it are refused.
Each item reports bytes, the proposed hash, current source hash and a reason to retain it; the recovery hash is included when recovery bytes are checked.
A completed, landed, captured proposal requires an existing independent byte-identical local copy before it can report local recoverability.
Symlinks, hardlinks and case aliases of the source do not count as independent recovery copies, and duplicate proposals for the same file identity are rejected.
Every [governed source](../contract/memory.md#catalog-schema) is protected as authority by filesystem identity, even when proposed through an alias or classified as another kind.
Authority, held, active, unlanded and incompletely captured items remain protected.
Task state and capture fields are operator declarations, not verified custody or removal permission.
Every plan is read-only, reports zero reclaimed bytes and no remote verification, and exposes no apply operation.
The existing janitor retains its own custody rules; this command neither runs nor changes cleanup.
For a future remote offload, separately verify the repository's privacy, exact remote object and durable capture before seeking the owning process's removal approval.

Upgrade through the normal signed release or source installation procedure; no index, database or catalog migration is required.
Existing version-1 catalogs remain valid, and `task_state` is optional.
Before rolling back to an older binary, remove only the new optional `task_state` metadata from a backed-up catalog, since older decoders reject unknown fields; keep all canonical source bytes and original approvals.
Discard caller-created delivery lists across rollback or conversation reset.

Every operation accepts `--data <directory>` for an explicit private data source; otherwise it uses the resolved CEO home's `data` folder.
No operation grants consent, moves history, calls a model or alters task state.
