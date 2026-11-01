## Startup memory and retrieval

This is the shared contract for the CFO and goblins in Claude Code, Codex and Pi.
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

`data/memory/startup.json` is an explicitly curated, versioned map of exact source ranges.
It never replaces the original ruling bytes with a summary.
The digest delivers warm ranges, a bounded cold routing index and a bounded fleet/backlog projection.
State bounds also apply without a catalog, independently of the full legacy authority bytes.
One current-state sample is reused by all startup renderings, with a shared two-second terminal-read budget and explicit unknown state when it expires.
It reports omitted state and directs current-state reads to `cfo fleet-view`, `cfo drain` and the existing recovery owners.
Displayed status tails are events, not evidence of present state, and omitted wakes remain unacknowledged.
Read-only startup retains its existing custody boundary and writes no digest or marker.

Claude's hook preview validates current sources on every start, resume, reload and fork before pointing to the newly composed `state/session-digest.md`.
Codex and Pi run `cfo session-start` at startup and after resume or compaction; a saved digest alone never validates current authority.
An unchanged resumed conversation skips source bytes only if it actually read and still holds them; a prior composition or completion marker is not proof of delivery.
Changed authority must be read again before dependent action, and current state still requires reconciliation.
Every role retrieves cold memory relevant to the project, task and topic before dependent action, even when the bounded routing index omitted its line.
Only the exact displayed ranges count as read; a selected range is never described as its whole source file.

An absent catalog is explicitly uncurated legacy memory, with the directives and index still loaded in full.
That is an authority-preservation boundary until the owner approves curation, not consent to migrate.
A present but invalid catalog never selects a silent legacy fallback.
Missing sources, checksum changes, uncovered bytes, overlapping ranges, conflicting ids, duplicate JSON keys, unknown fields and cyclic or missing supersession references are errors.
Do not act on affected authority until its diagnostic is resolved.
Stale facts remain visible with a Stow diagnostic; reading never reinforces, archives or advances a grace cycle.

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
| `location` | `warm` or `cold`; pending capture, legacy grace and perishable entries stay warm. |
| `trigger` | Required relevance cue for a cold item. |
| `projects`, `tasks`, `topics` | Exact nonblank routing-key arrays; a cold item needs at least one key. |
| `expiry` | A checkable condition quoted in a perishable entry's own text. |
| `supersedes` | Earlier item ids; they remain retrievable and are candidates for review, not automatically retired. |
| `approval` | Required per-item approval for a pinned cold item or any cold range from `overlord.md`. |

`approval` holds `by: "Overlord"`, the verbatim `quote` naming this entry's exact, separately delimited id, a `date` in `YYYY-MM-DD` format and `destination` equal to the live source-range owner `path#id`.
The catalog records an approval already given; it cannot create one.
Existing history already owned by `memory-archive.md` may remain cold without inventing a new offload; this grants no permission to move a ruling into that archive.
Broad approval, an uncreated destination or a proposed project change is insufficient.
The original file remains the live retrieval owner in this catalog; migration to a different owner needs a new approved plan and discoverability proof.
No command here moves or deletes history.

Tier markers in source text are interpreted by the shared `stow` skill, the single owner of their spellings, clocks and curation procedure; the [load map](../load-map.md) owns its discovery across harnesses.
Only trailing annotations carry tier metadata; literal examples in prose and code remain source content.
Use separate catalog entries for separately tiered facts.
Unmarked rulings and history are pinned; unmarked facts are legacy aging entries of unknown age.
Future dates, malformed or conflicting markers are explicit errors.
Only the native date markers are accepted; unsupported marker bytes remain unchanged in the read-only inventory with an explicit classification error.

Each optional `layers` entry declares a unique `name`, nonoverlapping knowledge `classes`, `owner`, `capture` and `correction` instructions.
It declares ownership through existing tools, not a connector or a new service.
The CFO captures a worker's reported knowledge on its behalf when that worker cannot reach a declared owner.
An unreachable tier leaves `<!--t:NAME-->` on the local warm entry and is an unresolved exception, never completed capture.
Corrections follow that tier's declared rule and preserve provenance.
If a tier is renamed or retired, Stow retargets the pending marker to the new owner or removes it only when no declared tier owns that class.

### Budgets and exceptions

`cfo memory budget` measures the combined always-loaded memory, including bookkeeping and cold routing lines.
Before a catalog exists it counts the legacy directives, project map and fact index and says which are absent.
`config/startup-memory-budget`, when present, is a positive integer estimated-token allowance; otherwise it is 10,000.
An invalid setting is an exception, never an inferred default.
An overrun retains all authority and requires a concrete budget or named-offload decision.
It is never budget relief or a reset-safe result.

`cfo memory budget --packages <file>` measures complete supplied startup captures for all three harnesses, not only a hook preview.
The file is a JSON array with one object per `codex`, `claude` and `pi`, each having `harness` and `inputs`.
Each input has `kind` and `path`, with paths relative to this file unless absolute.
All six categories must be present for each harness: `core`, `global`, `memory`, `skills-index`, `brief` and `digest`.
Use the actual delivered global rules and skill discovery index, including user or plugin skills, and both hook preview and disk digest when both are read.
Count repeated delivery as repeated inputs; an empty capture requires an explicit `reason` in that input declaring actual absence or containment in another named input.
Do not substitute a skill's full body for its discovery entry or omit a harness-local global source because the shared rules are the same.
Record the capture method and any inaccessible provider-owned inputs; the report measures the supplied text, not undisclosed system prompts.

The combined package allowance is `config/startup-context-budget`, a positive integer defaulting to 20,000 estimated tokens.
Reports retain the hash, byte size and category of every supplied input and explicitly mark overruns.
The estimate is `ceil(bytes/4)`, a stable local approximation, not provider billing, a tokenizer measurement or actual RAM usage.
Report before and after using the same capture boundaries, and distinguish synthetic consumer fixtures from measurements of an installed home.

### Lossless planning and pinned offload

Run `cfo memory migration-plan` before proposing any live history move.
It is a read-only inventory with no apply operation.
It reports source hashes, complete byte ranges, proposed destinations, authority classification, scope, each pinned approval boundary and the hash of reconstructed retained bytes.
An existing valid catalog supplies the exact item identities, ranges, authority, tiers, routing and declared layer owners; an invalid catalog is an error rather than permission to infer legacy ownership.
Legacy index pins also protect their linked fact files, and unclassified legacy ranges remain protected with an explicit diagnostic.
Repeated runs over unchanged sources produce identical plans.
Legacy `overlord.md` is conservatively all ruling authority; the tool does not infer spent consent from prose.
Proposed headings and bullet ranges are inventory units for human review, not automatic curation decisions.
An archive remains recoverable, with its original provenance.

The CFO names the concrete items proposed for relocation, their exact quote, source hash/range, trigger, destination, privacy and visibility, estimated relief and approval state.
Each pinned item stays at its current owner until its own explicit approval and live-destination proof exist.
Moving a current pinned item into a local conditional skill requires checking its private untracked destination, resolved repository-local exclude rule and actual discovery in a fresh skill index before removal.
An approved project destination ships through its normal project task and must land before removal.
Broad implementation approval cannot satisfy this narrower per-item boundary.
This implementation task performs no live migration.
