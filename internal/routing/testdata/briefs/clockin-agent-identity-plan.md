# Brief clockin-agent-identity-plan

Delivery contract: kind=scout, mode=no-mistakes, effort=xhigh

## Project

projects/clock-in (Clock-In: pnpm monorepo - apps/api, apps/web, apps/desktop (Tauri/Rust), packages/{shared,database})

## Task

Design task, not code: write the implementation plan for **agent identity v2** in Clock-In. Produce the plan as a document (report at the repo-root-level location per house convention, or `docs/plans/` - follow what the repo already does) and commit it on a branch. Do not change application code.

## Verified prod evidence (CFO gathered today - design from this, do not re-litigate)

- `agents` identity today is `(organization, source, project)` with NO operator dimension. Prod has exactly 2 rows, both `claude_code`, both owner_user_id = Gianluca (minted first 2026-08-15), named `Claude Code @ General` and `Claude Code @ unassigned` (project nullable).
- Francesco has 199 `agent_sessions` rows; the 16 with `agent_id` set are all stamped onto Gianluca's 2 agent rows. Whoever mints first owns the identity; every other member's shifts accrue to it. This is why the roster appears "only under Gianluca".
- Session capture already carries `cwd` per session and `shift_commits.repo_root` per commit; attribution resolves cwd -> project with a General/default fallback. Token capture (`agent_usage`) and model heartbeats shipped today (effort-v1).
- Most terminal agentic sessions on the fleet PC (kimi, pi, and cfo-spawned goblins) never record at all because only Claude Code's hooks are wired there - the plan must assume hook coverage expands (snippets exist in agent-runtimes.json) but identity must not depend on it.

## The Overlord's direction (design within it, challenge it only with evidence)

- Agent identity should be shaped by the **repo/codebase the agent works on**, not only the Clock-In project; projects are org-wide containers, repos are what agents actually touch. `shift_commits.repo_root` and session `cwd` are the raw material.
- The **operator** (which org member is running the harness) must be first-class: visible per agent row and per shift, so the roster answers "who is running what, where" for every member, and one member's shifts never mint or merge into another member's agent identity.
- The roster must organize the currently-invisible terminal sessions (cfo-spawned goblins, ad-hoc CLI runs) once their hooks are wired - group by agent identity, session, project/repo.
- Track per agent: hours, session (shift) count, and work quality (held rate from shift_commits verification exists - merged/reverted/orphaned, terminal states; token totals per agent now exist too).
- The Overlord wants to **wipe the current roster agents and re-mint under the new identity** - the plan must include a safe reset: what happens to the 2 existing agent rows, the 45 sessions pointing at them, their `agent_usage` and `shift_commits` rows (FK constraints!), and how re-minting/backfill works afterward. Prefer re-attribution over deletion where possible; if deletion, enumerate exactly what is deleted and what is preserved, with the migration/ops steps.

## The plan must answer

1. The identity key: exact columns/uniqueness for agent v2 (propose `(org, operator_user, source, repo_root-normalized)` or better - weigh repo-vs-project, nulls, renames, and the "unassigned" bucket). How identity interacts with the existing `(org, source, project)` table: migrate in place or new table?
2. Minting and re-attribution rules: when a shift starts with a cwd but no repo match yet; when repo identity is discovered late via shift_commits; how an agent "moves" when a directory is mapped to a project later.
3. Roster/UX changes: roster grouped by operator and/or repo; Agents tab and paystub implications; naming (repo-derived display names); how retired/rename survive.
4. The reset + backfill procedure, step by step, safe to run on prod.
5. Effort/quality surfacing: hours, shift count, held rate, tokens per agent - which existing endpoints change shape (contracts are `.strict()` - call out every contract change).
6. Migration sequence (drizzle-kit generate, inspect emitted SQL - meta snapshots have drifted), deploy order (API before/with web; desktop via installer), and what happens to old rows during the transition.

## Acceptance criteria

- A committed, concrete plan document with the six answers above, with file:line references into the current code for every change site.
- No application code, schema, or migration changes in this task.
- Drive no-mistakes to checks-passed for the docs commit; report the PR URL. Do not merge.

## Constraints

- Read AGENTS.md first and obey it. Respect the product model section: agents are durable identities, a model is an attribute of a shift, browser spans stay off the roster.
- Keep it clean-cut: the Overlord explicitly does not want over-engineering. Prefer the smallest identity change that fixes operator attribution and repo-based organization.
- Deploy/migration steps are described, not executed.

## Delivery

kind: scout
mode: no-mistakes
