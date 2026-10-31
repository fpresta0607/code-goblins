---
name: stow
description: Sweep the CFO session for uncaptured knowledge and open work, curate warm and cold memory with preserved authority, and report capture and budget exceptions. Use when asked to stow, before reset or compaction, or when startup memory exceeds its allowance.
---

# Stow

Leave a compact current operating map and preserve everything this session knows that must survive a reset.
Adapted from First Mate Stow at bb3e6906548b5c03d3212e9f060bafde982e0cf1; its copyright and MIT permissions remain in the repository's LICENSE.
Read `docs/contract/memory.md`, or `cfo help contract memory` in an installed home, for source ownership, catalog format, commands and lossless planning.
The pass writes private home data, never tracked project files, skills or shared runtime configuration.

## Owners

| Knowledge | Owner |
| --- | --- |
| Dated verbatim rulings, preferences and authority | `data/overlord.md`, default pinned. |
| Fleet facts and feedback | One file per fact under `data/memory/`, with a relevance line in `MEMORY.md`, default aging. |
| Current project map | `data/projects.md`; project knowledge routes through the project's normal delivery owner. |
| Open work and task findings | The existing tasks-axi backlog item or task-owned report. |
| Retired unique facts and provenance | `data/memory-archive.md`, never automatically injected. |
| Warm ranges and cold routes | `data/memory/startup.json`; original bytes stay at their source owners. |

Honor catalog `layers` before local defaults.
Each declared tier owns its knowledge classes and specifies its existing capture and correction path.
An unreachable owner is an exception, not completed capture merely because a local copy exists.
The CFO captures a worker's reported learning on its behalf when that worker cannot reach the owner.
Keep credentials out of memory and private preferences and fleet operations out of public project files.

## Tiers and markers

Markers are trailing HTML comments, and their bytes count toward the combined allowance.

- `<!--a:YYYY-MM-DD-->` is aging, with its last-reinforced date.
- `<!--p:YYYY-MM-DD-->` is perishable, with its last-reinforced date.
- `<!--P-->` is pinned in a file that does not default to pinned.
- `<!--g-->` records that an unconfirmed legacy aging entry consumed its first grace pass.
- `<!--t:NAME-->` follows the tier marker while a declared tier still needs to capture the entry.

Pinned entries have no decay clock and are never evicted, archived or offloaded by an automatic reduction step.
Only the Overlord or verified changes in reality change their current meaning; retain original rulings and provenance.
Relocation requires approval of the individual named item under the flow below.
Aging is stale at 30 days since reinforcement; perishable is stale at 7 days.
A perishable entry must name its checkable expiry condition, such as a task, version or dated expectation, in its own prose; a durable fact without one is aging.
This home uses date horizons; old optional `/N` pass counters are preserved but neither read nor advanced.
Decay advances only during a pass, never during retrieval or startup.

An unmarked default-pinned entry is pinned.
An unmarked aging entry has unknown age, not evidence of staleness.
On its first unconfirmed pass append `<!--g-->` and retain it throughout that invocation; never stamp or archive it then.
Only an entry that already had that marker at invocation start takes the next-pass branch: reinforce from independent evidence or archive as `legacy-unvalidated`.
Every dated entry carries its marker, including in a default-pinned file.
Each editable memory file carries at most the one-line scheme pointer `<!-- memory tiers: see the stow skill -->`; clocks and procedures are owned here.

## Complete pass

Every invocation performs the whole pass, even with no new finding.

1. Run `cfo memory budget` and record combined bytes, estimated tokens and effective allowance before a write.
   Invalid settings or unreadable memory are unresolved exceptions, never inferred defaults or reset-safe results.
   Overall startup accounting also uses the full three-harness package report defined by the memory contract, not a hook preview.
2. Read every governed source completely before planning a change and note which declared tiers this session can reach.
   An absent file stays absent unless a genuinely new finding requires it.
   Before any history move run `cfo memory migration-plan` and verify its complete retained-byte proof.
3. Sweep this session for uncaptured rulings, preferences, facts, corrections, project knowledge and undone work.
   Check the authoritative owner first; file a concise pointer or nothing when that owner already holds the fact.
   Capture declared-tier knowledge through its declared rule even when a local copy exists.
   If unreachable, retain it locally and warm with `<!--t:NAME-->` and name the exception in the receipt.
4. Build a whole-file retention plan before editing, ordered by likely future use.
   Keep current authority, frequently relevant facts and expensive-to-rediscover pointers warm.
   Classify changes as new, duplicate, superseding or obsolete; inspect the destination and replace concise current statements rather than append a journal.
   Never paraphrase or shorten a dated ruling, infer consent, or retire an earlier ruling merely because a later entry names it as a supersession candidate.
5. Reinforce only from independent evidence exercised, confirmed or re-derived in this session, naming that evidence in the receipt.
   Importance, plausibility, prior familiarity and rereading memory are not evidence.
   Explicitly lacking evidence takes the no-evidence path, including the legacy grace transition above.
6. Evaluate every dated entry against its clock.
   Revalidate stale aging entries or archive with provenance.
   For stale perishable entries, check the named condition: still open means evidence-backed reinforcement; resolved, expired or uncheckable means archival.
   Retier repeatedly durable perishable facts to aging.
   Before any removal, confirm every declared owner of that knowledge class holds the fact or capture it there first.
   If unreachable, keep the entry warm with its pending-capture marker regardless of age or tier.
   For a renamed or retired tier, retarget the marker to its current owner or remove it only when no declared tier owns that class.
7. Consolidate every editable source, not just the one receiving a finding.
   Archive eligible completed chronology, expired workarounds and low-utility unique facts with provenance.
   A duplicate leaves only when its fact survives at a stronger live owner or in a preserving consolidation.
   Pending capture and pinned authority stay protected on every reduction rung.
   Revalidate the updated catalog against changed sources; checksum drift is an error, never permission to bypass coverage.
8. When still over allowance, first archive eligible stale, superseded and low-utility non-pinned entries, consolidate tighter, then offload eligible current conditional non-pinned facts to an already-existing allowed live owner.
   Evaluate staleness before scope.
   Perishable, grace and pending-capture entries cannot be offloaded.
   The destination must hold the fact before source removal; proposals, future owners and unlanded delivery are not relief.
9. Before oldest-reinforced-first budget eviction, total the eligible pool: only editable dated aging entries without pending capture or pending offload, never pins or grace entries.
   If evicting the entire pool cannot reach allowance, evict nothing for budget reasons and name the protected floor and shortfall.
   Otherwise archive only as many eligible oldest entries as needed with source, date and reason.
10. Persist important open work held in this session through its existing owner.
    Inspect an item with `tasks-axi show <id> --full`, then use `update --body-file` with a considered replacement, and `--archive-body` when recoverability matters.
    File undone steps with genuine dependencies; leave judgments you cannot make as explicit questions.
    This covers only records the session holds, never a fleet-wide reconciliation against repositories or GitHub.
11. Run the budget report again and finish within allowance or open one concrete owner decision before ending the pass.
    Exhaust safe non-pinned reduction before attributing the remainder to protected authority.
    Name the shortfall and individual protected items, with the actual options to raise allowance or explicitly approve offloading or trimming each named item.
    Never end over budget as an accepted exception or claim a proposal is completed relief.

## Pinned offload boundary

The pass never creates, stores or edits a skill as a destination.
It may propose pinned relocation, not execute it from general task approval.
For each candidate record its exact quote, id, source path/range/hash, size, trigger, destination, privacy and visibility verdict, expected relief and approval state.
File or refresh one durable held backlog item containing the individually named proposals and require explicit plain-chat approval for each item.
No answer means no move and no relief.

Non-pinned conditional facts may move only to an already-existing private on-demand note with an established trigger, or through normal project delivery to an already-existing conditional owner.
Tracked shared skills, always-loaded root AGENTS.md and docs alone are not offload destinations.
Never put private content on a committed surface.

Approved pinned migration is separate from this pass.
For a new private local skill, verify no tracked path or filesystem collision, resolve the active home's repository-local exclude file, add its exact destination rule and prove the future SKILL.md is ignored before writing private bytes.
Prove actual discovery in a fresh skill index before source removal.
On failure remove only content and exclude rules created by that attempt, preserving the original source.
Project destinations must land through normal delivery first.
Keep the original quote and provenance; the catalog records an approval already given and never fabricates one.
Leave no pointer by default, or one concise line only when discoverability requires it.

## Cold provenance and receipt

Archive under a dated pass heading, retaining original bytes or exact quoted text plus source path/hash/range, tier, reinforcement date and reason.
Reasons include `unreinforced <N>d`, `superseded by <entry>`, `completed`, `expired`, `budget oldest-first` and `legacy-unvalidated`.
Recovery is search and copy back; archive truncation belongs to the Overlord.

The completion receipt states:

- Effective allowance and combined bytes/estimated tokens before and after, capture boundaries and exceptions.
- Actions for every governed source: `unchanged`, `added`, `rewritten`, `pruned`, `routed`, `archived` or `proposed-offload`; adding grace is `rewritten`.
- Each finding and its authoritative owner, declared-tier capture or correction, and independent evidence for reinforcement.
- Every pending capture, ownership exception, archive reason, live offload destination, actual relief and named pinned proposal.
- Every open record filed, corrected or deliberately left pending a stated judgment.
- Every unresolved budget decision and whether reset is safe.

Reset-safe means nothing this session knew has been lost: all durable knowledge is captured, no declared-tier capture is pending, important open work is filed or explicitly held with a reason, and the final result is within allowance with no unresolved exception.
It does not claim the entire home was reconciled.
Do not hide an overrun or unreachable capture behind that claim.
