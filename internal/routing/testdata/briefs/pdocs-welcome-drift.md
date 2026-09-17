# Brief pdocs-welcome-drift

## Project

projects/precisiondocs

## Task

Resolve GitHub issue #1049 on fpresta0607/PrecisionDocs-AI, filed by gcaruso-precisiondocs:
"fix(welcome-briefing): drift detector never converges - 4 paid regenerations/project/day, 25% of Anthropic spend".

Read the issue in full first: `gh issue view 1049 --repo fpresta0607/PrecisionDocs-AI`. It contains
production evidence, a two-part root cause, a fix spec, and verification criteria. Treat the spec as the
design to implement, and the evidence as claims to CONFIRM in code before changing anything.

Root cause as filed (confirm each against the source):
1. `projects.welcome_briefing_evidence_revision` is a bump counter advanced by ten DB triggers (migration
   257, inventoried in `scripts/verify_site_plan_reliability_schema.py:170-179`) that fire on ANY write,
   including no-op rewrites. Background maintenance (`refresh_foundation_regulatory` every ~7s) keeps it
   moving, so the counter says "a row was written", never "the briefing content changed".
2. `welcome_briefing.py:711` captures `source_evidence_revision` at task START, before the Sonnet call, and
   `persist_current_welcome_briefing` stamps that pre-work value - so any trigger during generation makes
   the new briefing stale at birth.
Net: `regenerate_if_evidence_drifted` (`welcome_repair.py:367`) can essentially never see
`current == stored`, so the 15-min settle and 6-h claim TTL act as a rate limiter on an infinite loop
rather than a circuit breaker. 132 of 176 projects regenerated repeatedly producing byte-identical text.

Fix spec (implement all three parts):
- PRIMARY: gate the paid call on the rendered brief HASH, not the counter. `project_brief.py` already has
  `project_brief_hash(brief)` and the 60s-cached `_current_project_brief_hash`. Keep the counter as a cheap
  pre-filter (counter unchanged -> skip hash compute entirely) but NEVER let the counter alone authorize
  spend. Preserve the existing fail-open asymmetry: an uncomparable hash means "we don't know", and
  "we don't know" must not buy a Sonnet call.
- SECONDARY: close the birth-stale race. Stamp the baseline at PERSIST time: re-read the revision and
  recompute the hash inside `persist_current_welcome_briefing`, in the same transaction as the message
  update, so the row records the state it actually converged to.
- HARDENING: add a hard ceiling on auto-regenerations per project (e.g. max N per week) with a logged
  reason when it trips, so a future detector regression is bounded instead of a permanent bleed. Emit a
  metric/log counter for the `_MAX_TOKENS_RETRY` truncation retry so its frequency is visible.

The trigger is a GET route (`routes_projects.py:9559`) - opening Project Home schedules paid work. Keep
that in mind: the fix must make a read genuinely free when nothing changed.

## Acceptance criteria

- Regression test: a foundation patch that changes NO briefing-visible field must not dispatch a
  regeneration. Red before the fix, green after.
- Regression test: a revision bump landing DURING generation must not leave the persisted row stale (no
  birth-stale row). Red before, green after.
- Regression test: the per-project regeneration ceiling trips and logs its reason.
- Every existing welcome-briefing / welcome-repair / project-brief test still passes.
- The module docstring in `welcome_briefing.py` states "Detection only: nothing here regenerates on drift"
  - reconcile the docstring with the shipped behavior so it is true.
- Local verification run by you, in your worktree, and listed in the PR body: ruff_changed against
  origin/main, pytest_changed, the scoped welcome/brief suites.
- PR opened against main, referencing "Closes #1049", with the production evidence summarized and the
  post-deploy expectation stated (regeneration events ~667/week -> near zero; briefing_version stops
  climbing; 94% stale-on-read -> near zero).
- Report with `cfo notify pdocs-welcome-drift --done --pr <url>`.

## Constraints

- NEVER run the no-mistakes daemon or any `no-mistakes`/`axi` command. It is deliberately DOWN. Verify
  locally instead. This is a standing Overlord order after a cost incident.
- NEVER read, print, or use ANTHROPIC_API_KEY or any provider API key. Your harness runs on the Overlord's
  subscription. If any tool errors with a usage or quota limit, STOP and `cfo notify --blocked` - never retry.
- Do not make live LLM calls in tests. Mock the generator. The whole point of this issue is that paid
  calls were happening when they should not; do not add more.
- Read-only against production. Do not run migrations against prod, do not touch prod data. A schema change
  ships as a migration file in the PR.
- NEVER touch `C:\dev\PrecisionDocs-AI` - that is the Overlord's own checkout. Work only in your worktree.
- Do not merge your own PR. The CFO merges.
- Rebase onto current origin/main before opening the PR; main moved several times today.
- Interactive git (`git rebase -i`, `git add -i`) is unavailable.
- Keep the diff focused on the three spec parts. If you find adjacent defects, note them in the PR body as
  follow-ups rather than widening the branch.

## Authentication

Services this task needs are declared in data\projects\precisiondocs\auth.json. No provider API key is
needed or permitted.

## Delivery

kind: ship
mode: direct-PR
