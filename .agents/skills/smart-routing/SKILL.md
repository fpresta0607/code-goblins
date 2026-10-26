---
name: smart-routing
description: Use when the CFO selects or revisits a worker's model and reasoning effort before dispatch, after failed verification or new complexity, or for an independent helper assignment, in any supported CFO harness.
---

# Smart routing

Choose for reliable completion and total time through verification, including retries and handoffs.
The CFO's own harness does not determine the worker's harness.
Use the existing lane table and commands; this skill is decision guidance, not another router.

## Establish usable choices

Read the user's current harness, model and effort choices first, including standing provider restrictions.
Explicit choices win; an unsupported explicit choice needs a corrected choice, not a silent substitution.
Do not switch live tasks as part of tuning new starts.

Read the effective `data/routing.json` and the project's `routing` override, then preview the actual task with `cfo route --project <project> "<short task>"`.
This command takes task text, not a brief filename; include material complexity and risk from the brief.
Use `--attempts <count>` before the task text for prior failures.
The preview checks classification and quota, not model entitlement or model-specific effort support.

Consult the installed harness and account's current catalog, using the configuration roots recorded in `state/harnesses.json`:

- Codex: `models_cache.json`, including its timestamp, visible model descriptions and `supported_reasoning_levels`.
- Claude Code: its local account model catalog under `cache/model-catalog`; additional options alone do not prove supported efforts.
- Pi: its model listing and provider configuration, plus the installed `pi --help` thinking options.

Intersect model support with the installed Code Goblins adapter's supported efforts.
The board's `/api/engines` view also includes policy entries and generic defaults, so presence there alone proves neither entitlement nor support.
If local evidence is missing, stale or contradictory, refresh through the harness's supported discovery or consult official provider documentation; do not guess from a model name.
Resolve `default` before comparing models or assigning independent verification.

Read fresh quota evidence through the existing preview and quota-axi.
Missing or stale evidence means unknown, never unlimited.
Keep allowance floors and paid-credit guards; do not buy credits or run benchmarks to decide.
Use only capable, available fallbacks permitted by the user's choices, naming why the wanted lane cannot run.
When none qualifies, wait for allowance or ask for the necessary choice.

## Match the work

Choose model capability from current catalog descriptions and comparable successful outcomes.
These are starting judgments, not claims of measured speed; use only supported effort levels.

| Work | Starting choice | Raise the choice when |
| --- | --- | --- |
| Mechanical edit, exact transformation | Small capable model, low effort | Scope or semantics become uncertain |
| Bounded implementation, known interfaces and tests | General coding model, medium effort | Dependencies or design choices multiply |
| Investigation | Small model, low for retrieval; general model, medium for synthesis | Competing hypotheses or cross-system reasoning appear |
| Uncertain bug | Strong reasoning model, high effort | Reproduction or a hypothesis fails; consider xhigh for unresolved deep reasoning |
| High-risk architecture or security | Strong reasoning model, high effort with independent review | Interacting invariants warrant xhigh; max needs a concrete unresolved difficulty |
| Independent helper | Match its own bounded task | Verification requires a capable model different from its parent's resolved model |

Do not lower effort to compensate for an incapable model.
Reassess on new complexity or failed verification before spending another attempt; retain the task's existing repair budget.

## Express and verify the decision

If current lanes collapse these distinctions, name the proposed model and effort and tune the existing lane entries within the user's authority; otherwise present that concrete policy change for decision.
Delegated model selection authorizes tuning inferred lanes within the user's choices, without asking again; it does not authorize replacing an explicit choice.
Do not silently launch an unsuitable heavy default while describing a lighter recommendation.
Preserve explicit overrides, fallback ordering, helper roles and unrelated policy fields.
Preview representative briefs against an isolated copy before applying a policy change; installation preserves the live table.
Dispatch through `cfo spawn`; per-dispatch flags remain reserved for explicit user choices.
Use the configured helper roles and existing independent-verification checks.

Record the task class and risk, chosen lane/model/effort, capability and quota evidence with age, any fallback reason, and the condition for escalation in the brief or dispatch note.
Compare success rate and time to verified completion for comparable tasks and roles, including failed attempts in that total.
`cfo doctor` telemetry measures validation invocations only: successful test time is not implementation time, and failed or cancelled latency is not successful speed.
Absent implementation evidence stays unmeasured.
