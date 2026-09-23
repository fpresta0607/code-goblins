---
name: stow
description: Sweep the CFO session for durable knowledge, file it to the fleet's memory homes, persist open work to the backlog, and curate the tiered, decaying startup memory the session-start digest prints. Use when the Supreme Overlord invokes /stow, before a context reset or compaction, or when the startup memory has outgrown its budget.
---

# stow

Leave the next CFO session a compact, current operating map instead of an accumulating journal.
Every durable finding lands on disk, every memory file this pass touches comes out more accurate rather than longer, and stale material retires to a cold archive instead of being deleted.
Adapted from First Mate's stow pass for the Code Goblins homes.

## The homes

| Home | What belongs there | Default tier | Loaded |
| --- | --- | --- | --- |
| `data/overlord.md` | The Supreme Overlord's standing directives, rulings and authority grants, each with its date and the Overlord's own words | `pinned` | every session, printed in full by the session-start digest |
| `data/learnings.md` | Fleet operating facts: gotchas, workarounds, verified tool behaviour | `aging` | every session, printed in full by the session-start digest |
| The CFO harness's own memory index, when it keeps one (Claude Code's auto-memory `MEMORY.md` for this checkout) | The same kind of operating facts, one line per entry | `aging` | every session, by the harness |
| `data/backlog.md` | Open work, held operations, follow-ups; its items carry no markers, because `tasks-axi` owns their state | `perishable` | its first queued rows, by the digest |
| `data/memory-archive.md` | Everything retired from the homes above | cold | never |

`data/` is the private fleet-state repository, so nothing this skill writes ever lands in a tracked file of the public code-goblins repository.
The backlog is owned by `tasks-axi`: read an item with `tasks-axi show <id> --full` and change it with `tasks-axi add`, `update --body-file`, `hold` or `done`, never by editing the file.

## Tiers and markers

Markers are compact trailing HTML comments, because every marker byte is paid for in every session:

- `<!--a:YYYY-MM-DD-->` - an `aging` entry; the date is its last-reinforced date.
- `<!--p:YYYY-MM-DD-->` - a `perishable` entry; the date is its last-reinforced date.
- `<!--P-->` - an explicitly `pinned` entry in a file whose default tier is not `pinned`.
- `<!--g-->` - migration only: an unconfirmed legacy entry in its one grace cycle, which lasts until the next pass.

The tiers:

- `pinned` never decays and is never dropped to shorten a file.
  It changes only when the Supreme Overlord or reality changes it.
- `aging` must re-prove itself: at 30 days or more since its last-reinforced date it is stale, and a stale entry is re-validated (date refreshed) or archived, never kept by inertia.
- `perishable` is written to be thrown out: at 7 days or more it is stale, and its text must name a checkable expiry condition, such as a backlog id, a version, a PR, or a dated expectation.
  An entry that cannot name one is `aging`.

Marking rules:

- A `pinned` entry in a file whose default is `pinned` carries no marker; every `aging` and `perishable` entry always carries its dated marker, whatever its file's default.
- Each governed file carries one header line naming this skill as the scheme owner: `<!-- memory tiers: see the stow skill -->`.
  Tier semantics, marker spellings and clocks live only in this skill; no file restates them.
- Decay advances only when a pass runs.

**The evidence rule.** Refresh a last-reinforced date only on independent evidence from this session that you can name in the receipt: the fact was used, confirmed or re-derived.
Plausibility, importance, the entry's own text and re-reading memory are not evidence.

## Directives are the Overlord's words

`data/overlord.md` is a record of rulings, not a summary of them.
Never paraphrase, merge or shorten a standing directive: it keeps its date and its exact wording.
What leaves the file is history around the directives: merge logs, incident chronology, spent one-time authority, pending approvals that were answered, and evidence paragraphs.
A directive that a later directive supersedes stays until the Overlord or the later directive's own text retires it; list it in the receipt as a supersession candidate instead of removing it.
Open work found in a memory file moves to the backlog as a queued or held item, and leaves memory only once that item exists.

## The pass

Every invocation runs the whole pass, even when the session produced no new finding.

1. **Measure.** Report the size of each always-loaded memory file and their total, in bytes and estimated tokens (bytes / 4, a deliberately conservative local estimate, not provider accounting):

   ```powershell
   Get-Item data\overlord.md, data\learnings.md -ErrorAction SilentlyContinue | Select-Object Name, Length
   ```

   Add the harness memory index when there is one.
   The budget is **10,000 estimated tokens** across the always-loaded memory files combined: `data/overlord.md`, `data/learnings.md` and the harness memory index.
   The backlog does not count, because the digest prints only its first queued rows and `tasks-axi` owns its size.
   That is about 5% of a 200k context paid by every CFO session before it does anything.
2. **Read every governed file completely** before planning a write.
   An absent file is absent, not an invitation to manufacture content.
3. **Sweep the session** for uncaptured durable knowledge: a directive or preference the Overlord stated, an operating fact, a gotcha, a standing decision, and undone next steps.
   Before filing anything, check whether it already lives authoritatively somewhere (AGENTS.md, docs, a project's own files, the code).
   If it does, record a one-line pointer to that owner or nothing, never a copy.
4. **Route each finding.**
   A directive, preference, authority grant or ruling goes to `data/overlord.md`.
   A fleet operating fact goes to `data/learnings.md` or the harness memory index, whichever the CFO already uses.
   Open work goes to the backlog through `tasks-axi`.
   Knowledge intrinsic to one project goes to that project through a normal ship task, never straight into its files.
   Knowledge general to every Code Goblins user goes into this repository's tracked docs through a goblin and the gate.
5. **Inspect, then update.**
   Classify each finding against what the destination already holds: new, duplicate, superseding, or evidence that an entry is obsolete.
   Fold a duplicate into the entry that carries it, rewrite what a finding supersedes (outside `data/overlord.md`, see above), and prefer a one-sentence rewrite to a second near-identical entry.
   Stamp each new entry with today's date and its tier.
6. **Reinforce and decay.**
   Refresh dates only under the evidence rule, then evaluate every dated entry against its clock: re-validate or archive stale `aging` entries, and re-check stale `perishable` entries against their named condition (still open: refresh; resolved, expired or uncheckable: archive).
7. **Consolidate every governed file**, not only the one a finding touched.
   Archive completed chronology, stale versions and paths, transient task state, resolved alternatives, old metrics and report-sized procedures.
   Never plainly remove a unique current fact: it leaves only by archiving with provenance, by moving to a live owner that already holds it, or by a merge that preserves it.
8. **Over budget after consolidation**, first total what this pass may not archive: `pinned` entries, entries still in their grace cycle, and every line of an always-loaded memory file that is not an entry (its header, headings and blank lines).
   None of it is ever archived or moved for budget.
   When that floor alone exceeds the budget, archive nothing for budget reasons, name the floor and the shortfall in the receipt, and ask the Overlord whether to raise the budget or approve named moves.
   Otherwise reduce in this order: archive every eligible stale, superseded or low-value entry; consolidate tighter; propose moving conditional entries (true, but relevant only in a nameable situation, such as one project) to an on-demand owner such as that project's `AGENTS.md` or a skill; then archive the remaining `aging` and `perishable` entries oldest-reinforced first.
   Everything outside the floor is archivable, so this ladder always reaches the budget or the floor question.
   A proposal is not relief: the pass ends within budget or with the floor question open, never with an accepted overrun.
9. **Measure again** and write the receipt.

## The cold tier

Archiving is a move, not a delete.
Append to `data/memory-archive.md` under a dated heading, keeping provenance verbose because the archive is never loaded:

```markdown
## 2026-09-23 stow
- (from overlord.md, tier: pinned, reason: merge log) Merged under the 10-minute rule: ...
- (from learnings.md, tier: aging, reinforced: 2026-08-02, reason: unreinforced 52d) ...
```

Reasons include `unreinforced <N>d`, `superseded by <entry>`, `completed`, `expired`, `budget oldest-first` and `legacy-unvalidated`.
Recovery is search plus copy back.
Truncating the archive is the Overlord's decision.

## First pass on legacy files

Unmarked legacy entries are their file's default tier with unknown age, and unknown age is not guilt.

- In `data/overlord.md` every unmarked entry is simply pinned; consolidation still moves history out.
- In `data/learnings.md` and the harness memory index, stamp each entry this session can confirm with today's `aging` marker; mark the rest `<!--g-->` and keep them for this pass.
  A `<!--g-->` entry is in its grace cycle until the next pass: it is never archived for budget, and it counts toward the floor in step 8, so an oversized legacy file ends its first pass with the floor question open rather than with unvalidated archiving.
  On the next pass, an entry still carrying `<!--g-->` is either confirmed and stamped, or archived as `legacy-unvalidated`.

## What this skill never does

- It never writes into a tracked file of this repository or of a project; those changes ship through a goblin and the gate.
- It never creates a skill as a destination for a finding.
- It never files credentials or secrets anywhere.
- It never commits: `data/` is committed and pushed at the end of the session with the rest of the fleet state.

## Receipt

Report in plain language:

- the budget and the always-loaded memory files' estimated token total before and after, and, when step 8 stopped at its floor, the floor and the shortfall;
- one action per governed file: `unchanged`, `added`, `rewritten`, `archived`, `routed` or `proposed-offload`;
- every finding filed outside memory and where it went;
- every archived entry's reason, every supersession candidate left in `data/overlord.md`, and every open item moved to the backlog;
- whether the session is safe to reset: only when every durable finding is on disk, every open item this session held is filed, and the always-loaded memory total is within budget with no question pending.
