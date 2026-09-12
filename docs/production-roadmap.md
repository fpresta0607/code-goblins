# Production autonomy roadmap

Code Goblins optimizes for one metric: **human intervention minutes per accepted production change**, subject to correctness and safety constraints.

This roadmap deliberately borrows patterns, not product dependencies. Code Goblins stays a local-first Windows-native control plane.

## P0 — Proof-before-merge

**Problem:** a worker or supervisor can observe green CI and later merge a different head commit, or merge while checks are still pending.

**Implementation:**

1. Read PR state, draft state, mergeability, review decision, exact `headRefOid`, and `statusCheckRollup` through `gh pr view --json`.
2. Fail closed when mergeability is unknown, reviews request changes, checks are absent/pending/failing, or the PR is not open.
3. Preserve the verified head SHA as the delivery proof.
4. Merge with `gh pr merge --match-head-commit <sha>` so a push between proof and merge invalidates the operation.
5. Record the proof in task metadata: head SHA, check count, timestamp, and merge result.

The verifier and tests are introduced with the standalone-product PR. Wiring it into `cfo pr merge` is the next code change.

## P0 — Acceptance evidence as a first-class artifact

**Problem:** tests can pass while the requested behavior is still wrong. "Done" needs evidence tied to the brief.

**Implementation:** add an evidence manifest to each ship task:

```json
{
  "task_id": "checkout-fix",
  "head_sha": "...",
  "criteria": [
    {
      "id": "AC-1",
      "claim": "A declined card preserves the cart",
      "proof": ["test:payments/decline.spec.ts", "artifact:browser/decline.webm"],
      "status": "proven"
    }
  ]
}
```

The goblin proposes evidence; an independent reviewer verifies it. Delivery refuses any acceptance criterion without evidence or an explicit human waiver. UI tasks should support browser screenshots/video/console/network artifacts through the existing Chrome DevTools skill. Backend tasks should reference tests, commands, logs, or API traces.

## P1 — Dependency-aware task graph and merge train

**Pattern worth adopting:** Gas Town's separation between workers and integration/merge handling.

**Problem:** independent worktrees are safe while coding, but parallel branches can be individually green and collectively incompatible.

**Implementation:**

- Add `depends_on`, `touches`, and `integration_group` fields to task metadata.
- CFO constructs a DAG before dispatch when work is decomposed.
- Workers remain isolated and never merge sibling work themselves.
- Add an integration worktree owned by the CFO, not a goblin.
- Candidate branches enter a deterministic queue.
- Rebase/merge candidate N onto the latest integration head, rerun affected validation, then advance the integration head.
- On conflict or regression, return only the failed candidate to a repair goblin with the integration diff and failure evidence.
- Final PR comes from the proven integration head.

This turns parallelism from "several green PRs" into "one proven combined result."

## P1 — Automatic bounded recovery policy

**Pattern worth adopting:** persistent project coordinators that react to CI/review/session events without requiring the human to re-prompt them.

Code Goblins already has wake events and harness switching. Extend that into a typed recovery state machine:

```text
working
  -> blocked_auth       -> human
  -> blocked_question   -> CFO decision / human if ambiguous
  -> harness_erroring   -> switch according to routing policy
  -> validation_failed  -> repair attempt <= budget
  -> ci_failed          -> repair attempt <= budget
  -> conflict           -> integration repair
  -> proven             -> delivery decision
  -> exhausted          -> human with evidence bundle
```

Every automatic transition gets a bounded attempt count and durable episode ID. Restarts must not reset budgets. The CFO should wake the human only for `blocked_auth`, genuinely ambiguous product decisions, exhausted budgets, or policy-defined high-risk delivery.

## P1 — Risk-scoped autonomy instead of global YOLO

**Problem:** one broad autonomy flag is too coarse for unattended production work.

**Implementation:** replace the effective decision model with capabilities attached to task class and project policy:

```json
{
  "mechanical": {
    "merge_green_pr": true,
    "modify_dependencies": false,
    "run_migrations": false,
    "deploy_production": false
  },
  "ordinary": {
    "merge_green_pr": false,
    "modify_dependencies": true,
    "run_migrations": false,
    "deploy_production": false
  },
  "high-risk": {
    "merge_green_pr": false,
    "modify_dependencies": true,
    "run_migrations": false,
    "deploy_production": false
  }
}
```

Capabilities should be enforced by the CFO command boundary and credential availability, not merely written into prompts. Production credentials should not be injected into implementation workers unless the policy explicitly requires them.

## P2 — Project brain with provenance

**Pattern worth adopting:** Agent Orchestrator's persistent project-level coordination context.

Do not add a generic vector database first. Store compact structured project memory in the repository or CFO home:

- architecture decisions and invariants;
- known validation commands;
- service ownership and authentication manifest references;
- recurring failure signatures and successful repairs;
- component/file ownership hints;
- task outcomes and evidence manifests.

Every learned fact carries provenance (`commit`, `task`, `file`, or human decision) and a freshness boundary. The CFO retrieves this context when briefing workers. Workers do not silently rewrite canonical project rules.

## P2 — Fleet evaluation harness

Do not optimize harness routing from anecdotes.

Create a replayable benchmark corpus of real Code Goblins tasks with frozen briefs and acceptance criteria. Record per harness/model:

- accepted-change rate;
- human intervention minutes;
- wall-clock time;
- tokens/cost;
- repair cycles;
- CI failures;
- regressions after merge;
- percentage of acceptance criteria with independently verified evidence.

Routing policy should be updated from these outcomes. Validation-agent latency is not a proxy for implementation performance.

## Recommended adoption order

1. Wire proof-before-merge into `cfo pr merge` and pin the verified SHA.
2. Add acceptance-evidence manifests and require them for `no-mistakes` ship tasks.
3. Add the integration worktree + dependency-aware merge train.
4. Turn wake handling into the bounded recovery state machine.
5. Replace broad autonomy with capability-scoped project policy.
6. Add provenance-backed project memory.
7. Build the fleet evaluation corpus and tune routing from measured outcomes.

The first four materially improve autonomous delivery. The later items improve safety, continuity, and optimization once the core loop is trustworthy.
