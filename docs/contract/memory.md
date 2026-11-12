## Startup memory and retrieval

This is the shared contract for the CEO and goblins in Claude Code, Codex and Pi.
It adapts First Mate's [Stow](https://github.com/kunchenguid/firstmate/blob/bb3e6906548b5c03d3212e9f060bafde982e0cf1/.agents/skills/stow/SKILL.md), knowledge routing, Bearings, session-start-recovery and operational-home-layout at the reviewed revision.
The upstream copyright and MIT permissions are retained in [LICENSE](../../LICENSE).
No memory service, external model call, new board, scheduler or hook-trust change is part of this contract.

### Owners and startup

`data/overlord.md` records dated rulings verbatim.
`data/memory/MEMORY.md` indexes facts, one link and relevance cue per fact.
A fact file starts with frontmatter `name`, `description` and `metadata.type`, where type is `user`, `feedback`, `project` or `reference`, followed by the fact and, for feedback or project facts, **Why:** and **How to apply:**.
`data/projects.md` is the project map, `data/backlog.md` is the tasks-axi-owned work record, and `data/memory-archive.md` is recoverable cold history.
Do not copy history into task briefs or duplicate facts already owned by a project or conditional procedure.
Keep private preferences and fleet delivery posture out of public project memory.

`data/memory/startup.json` is a versioned map of exact source ranges, derived from the memory files by `goblins memory sync` and curated by `goblins memory scope` or by hand.
It never replaces the original ruling bytes with a summary.
The digest delivers warm ranges, each after its id, a bounded count of what each scope holds cold and a bounded fleet/backlog projection.
State bounds also apply without a catalog, independently of the full legacy authority bytes.
One current-state sample is reused by all startup renderings, with a shared two-second terminal-read budget and explicit unknown state when it expires.
It reports omitted state and directs current-state reads to `goblins fleet-view`, `goblins drain` and the existing recovery owners.
Displayed status tails are events, not evidence of present state, and omitted wakes remain unacknowledged.
Read-only startup retains its existing custody boundary and writes no digest or marker.

Claude's hook preview validates current sources on every start, resume, reload and fork before pointing to the newly composed `state/session-digest.md`.
Codex and Pi run `goblins session-start` at startup and after resume or compaction; a saved digest alone never validates current authority.
An unchanged resumed conversation skips source bytes only if it actually read and still holds them; a prior composition or completion marker is not proof of delivery.
Changed authority must be read again before dependent action, and current state still requires reconciliation.
Every role retrieves cold memory relevant to the project, task and topic before dependent action, even when the bounded scope counts omitted its line.
Only the exact displayed ranges count as read; a selected range is never described as its whole source file.

An absent catalog is explicitly uncurated legacy memory, with the directives and index still loaded in full.
That is an authority-preservation boundary until the owner approves curation, not consent to migrate.
A present but invalid catalog never selects a silent legacy fallback.
Missing sources, checksum changes, uncovered bytes, overlapping ranges, conflicting ids, duplicate JSON keys, unknown fields and cyclic or missing supersession references are errors.
Do not act on affected authority until its diagnostic is resolved.
Memory that changed after the catalog was written is such an error until `goblins memory sync` derives the catalog again; no startup and no retrieval rewrites the catalog.
Stale facts remain visible with a Stow diagnostic; reading never reinforces, archives or advances a grace cycle.

### Rulings by scope

Startup loads the rulings of the whole fleet, the fact index and the warm facts.
A ruling ruled for a project is cold: startup leaves it out, counts it under that project's scope, and `goblins memory retrieve --project <name> --cold` prints it.
Every ruling stays verbatim in `overlord.md` and retrievable by its id, whatever its scope.
A ruling no scope has been ruled for loads at every start, and startup says how many there are, so nothing is left out for want of a decision.

`goblins memory sync` derives the catalog from the memory files, which stay the source of truth.
Each line of `overlord.md` that is not a heading, a comment or blank is one entry, under the id `goblins memory recall` prints for that ruling; a repeated line takes `-2`, `-3` after its id.
A heading and what follows it belong to the entry of the first ruling below, and blank lines and comments to the ruling above, so every byte is in one entry.
Each line of `memory/MEMORY.md` is one warm entry, a fact file is one cold fact found by its description and its `metadata.type`, `memory-archive.md` is one cold history range and `projects.md` one warm entry.
A fact file that ends in a pinned, pending-capture, grace or perishable marker is warm instead, when it is new to the catalog and when the one entry it had was cold, since those tiers stay loaded and a pinned item leaves startup only on its own approval.
A pinned fact whose entry carries its own approval keeps the location the catalog gave it, the archive's history stays cold whatever its lines carry, and ruling a fact kept warm this way for a project is still refused, with the entry named.
A fact the index pins loads unless that entry carries its own approval.
A fact file loads only for a reason a sync can read: one of those markers, an index pin, an active or held task state, or a ruling for the whole fleet.
When its words change and none of them holds, it comes by scope again with the routing it had, so a fact a marker loaded leaves startup once the marker is gone; `goblins memory scope --fleet <id>` is the way to keep a fact loaded.
A fact with no trigger or no routing key stays loaded, since no scope would find it, and an entry set by hand as anything but a fact stays as it was set.
A file that has not changed keeps its entries as they were set, a line that is still there keeps its scope by its id, and words the catalog has not seen load until a scope is ruled for them.
A scope whose words are no longer in memory is named in the command's report.
A catalog that would not load is never written: it is checked against the memory files before it replaces the one on disk, so a refusal leaves the catalog file as it was, or absent.
A perishable entry with no expiry named is such a refusal, which names every one of them and the single `goblins memory scope --expiry <id>="<words>"` command, one pair for each, that names them all.

`goblins memory scope --project <name>` rules the entries it names for that project and `goblins memory scope --fleet` for the whole fleet; neither changes a byte of a memory file.
Writing the catalog is the CEO's alone, since it decides what a CEO session reads at its start.
Every writer of the catalog, a decision on a lesson included, holds one lock, so two never write at once.

### Catalog schema

The top-level fields are `version: 1`, `sources` and optional `layers`.
Every Markdown source under `data/memory/`, plus each present `overlord.md`, `projects.md` and `memory-archive.md`, must be accounted for.
Each source has `path`, `sha256` and `entries`.
Paths use forward slashes relative to the private `data` directory; traversal, duplicate sources and links outside that directory are refused.
SHA256 values describe the original bytes, including CRLF and Unicode.
Entries must cover every source byte once, in source order, with no gap or overlap and no split UTF-8 character.

Each entry has:

| Field | Meaning |
| --- | --- |
| `id` | Unique stable ASCII name starting with a letter or digit, followed only by letters, digits, underscores or hyphens; also the exact retrieval key. |
| `start`, `end`, `sha256` | Half-open byte range and hash of exactly those bytes. |
| `authority` | `ruling`, `fact`, `project` or `history`; classification never authorizes discarding a ruling. |
| `location` | `warm` or `cold`; pending capture, legacy grace and perishable entries stay warm, and so does an unmarked fact in `overlord.md`, `projects.md` or `memory/MEMORY.md`, which startup loaded whole before a catalog. |
| `trigger` | Required relevance cue for a cold item. |
| `projects`, `tasks`, `topics` | Exact nonblank routing-key arrays; a cold item needs at least one key. |
| `expiry` | A checkable condition quoted in a perishable entry's own text. |
| `supersedes` | Earlier item ids; they remain retrievable and are candidates for review, not automatically retired. |
| `task_state` | Optional `active`, `held` or `completed` classification; active and held entries must remain warm. |
| `approval` | Required per-item approval for a pinned cold item or any cold range from `overlord.md`, except a range of `overlord.md` routed to at least one project. |

`approval` holds `by: "Overlord"`, the verbatim `quote` naming this entry's exact, separately delimited id, a `date` in `YYYY-MM-DD` format and `destination` equal to the live source-range owner `path#id`.
The catalog records an approval already given; it cannot create one.
A range of `overlord.md` routed to a project needs no approval of its own, by the Overlord's ruling that project rulings come by scope: leaving startup moves no byte, and the ruling stays verbatim in `overlord.md` and retrievable by its id.
That ruling covers the selection alone; a cold ruling routed only by task or topic, a pinned item of any other file, and any move or deletion of a ruling still need their own approval.
Existing history already owned by `memory-archive.md` may remain cold without inventing a new offload; this grants no permission to move a ruling into that archive.
The archive's tier markers are history as well and tier nothing now, so its range is cold whatever its lines carry.
Broad approval, an uncreated destination or a proposed project change is insufficient.
The original file remains the live retrieval owner in this catalog; migration to a different owner needs a new approved plan and discoverability proof.
No command here moves or deletes history.
The optional keyword discovery, ranked recall, task context packs, caller-declared delivery fingerprints and local retention proposals are defined in [the memory command reference](../commands/memory.md).
They preserve this approval boundary, never promote a search match to authority and never establish task custody from a proposal.
The recall index is disposable and rebuilt from these files; it is never a second owner of any fact.
A lesson from a done report reaches memory only once the CEO accepts it, as a new fact file, listed in the catalog, when the home has one, as one cold fact routed to its repository; a fact it supersedes stays as it is, and a ruling is never superseded this way.

Tier markers in source text are interpreted by the shared `stow` skill, the single owner of their spellings, clocks and curation procedure; the [load map](../load-map.md) owns its discovery across harnesses.
Only trailing annotations carry tier metadata; literal examples in prose and code remain source content.
Use separate catalog entries for separately tiered facts.
Unmarked rulings and history are pinned; unmarked facts are legacy aging entries of unknown age.
An unmarked fact file was never loaded at startup, only its index line was, so it may be cold and keeps its Stow diagnostic wherever it is counted or printed.
Future dates, malformed or conflicting markers are explicit errors.
Only the native date markers are accepted; unsupported marker bytes remain unchanged in the read-only inventory with an explicit classification error.

Each optional `layers` entry declares a unique `name`, nonoverlapping knowledge `classes`, `owner`, `capture` and `correction` instructions.
It declares ownership through existing tools, not a connector or a new service.
The CEO captures a worker's reported knowledge on its behalf when that worker cannot reach a declared owner.
An unreachable tier leaves `<!--t:NAME-->` on the local warm entry and is an unresolved exception, never completed capture.
Corrections follow that tier's declared rule and preserve provenance.
If a tier is renamed or retired, Stow retargets the pending marker to the new owner or removes it only when no declared tier owns that class.

### Budgets and exceptions

`goblins memory budget` measures the combined always-loaded memory: with a catalog, exactly the text startup prints, its ids, scope counts and count of unclassified rulings included.
Before a catalog exists it counts the legacy directives, project map and fact index and says which are absent.
`config/startup-memory-budget`, when present, is a positive integer estimated-token allowance; otherwise it is 15,000.
An invalid setting is an exception, never an inferred default.
An overrun retains all authority and requires a concrete budget or named-offload decision.
It is never budget relief or a reset-safe result.

`goblins memory budget --packages <file>` measures complete supplied startup captures for all three harnesses, not only a hook preview.
The file is a JSON array with one object per `codex`, `claude` and `pi`, each having `harness` and `inputs`.
Each input has `kind` and `path`, with paths relative to this file unless absolute.
All six categories must be present for each harness: `core`, `global`, `memory`, `skills-index`, `brief` and `digest`.
Use the actual delivered global rules and skill discovery index, including user or plugin skills, and both hook preview and disk digest when both are read.
Count repeated delivery as repeated inputs; an empty capture still needs an existing file and an explicit `reason` declaring actual absence or containment in another named input.
Do not substitute a skill's full body for its discovery entry or omit a harness-local global source because the shared rules are the same.
Record the capture method and any inaccessible provider-owned inputs; the report measures the supplied text, not undisclosed system prompts.

The combined package allowance is `config/startup-context-budget`, a positive integer defaulting to 20,000 estimated tokens.
Both allowance files are resolved in `config/` beside the absolute, normalized data directory selected by `--data` or the CEO home.
Reports retain the hash, byte size and category of every supplied input and explicitly mark overruns.
The estimate is `ceil(bytes/4)`, a stable local approximation, not provider billing, a tokenizer measurement or actual RAM usage.
Report before and after using the same capture boundaries, and distinguish synthetic consumer fixtures from measurements of an installed home.

### Lossless planning and pinned offload

Run `goblins memory migration-plan` before proposing any live history move.
It is a read-only inventory with no apply operation.
It reports source hashes, complete byte ranges, proposed destinations, authority classification, scope, each pinned approval boundary and the hash of reconstructed retained bytes.
An existing valid catalog supplies the exact item identities, ranges, authority, tiers, routing and declared layer owners; an invalid catalog is an error rather than permission to infer legacy ownership.
An index pin protects every catalog entry in its linked fact file and the same source in legacy inventory, using Windows filesystem identity after resolving its inline Markdown destination.
Fragments do not change the target file; percent escapes, angle-bracketed paths with spaces and balanced or escaped parentheses are resolved before lookup.
Fenced and indented code examples, including within blockquotes and lists, remain literal; a genuine local pin that cannot resolve to a governed source is an explicit error.
Conflicting dated tiers are errors, and unclassified legacy ranges remain protected with an explicit diagnostic.
Repeated runs over unchanged sources produce identical plans.
Legacy `overlord.md` is conservatively all ruling authority; the tool does not infer spent consent from prose.
Proposed headings and bullet ranges are inventory units for human review, not automatic curation decisions.
An archive remains recoverable, with its original provenance.

The CEO names the concrete items proposed for relocation, their exact quote, source hash/range, trigger, destination, privacy and visibility, estimated relief and approval state.
Each pinned item stays at its current owner until its own explicit approval and live-destination proof exist.
Moving a current pinned item into a local conditional skill requires checking its private untracked destination, resolved repository-local exclude rule and actual discovery in a fresh skill index before removal.
An approved project destination ships through its normal project task and must land before removal.
Broad implementation approval cannot satisfy this narrower per-item boundary.
This implementation task performs no live migration.
