# PrecisionDocs: production readiness audit of the 2026-09-08/09 merges

**Project:** `projects/precisiondocs`
**Kind:** audit, report-first; fix only what the report says is broken and small.
**Origin:** Overlord 2026-09-09: "Verify all CI issues and merges are safe and prod ready, no mistakes."

## What the CFO measured, 2026-09-09

Fourteen PRs merged to main since 2026-09-08: #1135 #1137 #1138 #1139 #1141 #1142 #1143 #1144 #1145 #1146 #1147 #1148 #1149 #1150.
Main head f4408cffc (#1150). The `Deploy to Production` workflow on that head: gate=success, build-and-push=success, deploy=success, failover=skipped. The two prior deploys (7e7a7e0b5, 4f83f1db1) also success.
`ci.yml` runs only on `pull_request`, so the deploy workflow's own `gate` job is the only backend verdict main gets; confirm what that job actually runs.
A `gh pr diff --name-only` over those fourteen PRs matched no `migrations/` or `alembic` path. Do not trust that grep: find where this repo keeps migrations and check every one of the fourteen diffs against that path.
Known defect today: no-mistakes reported `checks-passed` on #1151 for a head whose gate job was CANCELLED. So "the pipeline said green" is not evidence for any of the fourteen; the job conclusions on the merged head are.

## Verify, per PR

1. The gate that merged it: for the PR's final head, `gate` and `verify` both `conclusion: success` (not cancelled, not skipped, not a timeout that ended without a verdict). List any PR that merged without that.
2. Migrations: any migration it added is applied to production Supabase (project <supabase-project-ref>) and to nothing it should not be. Migration 270 is known to have never been applied anywhere and must NOT be applied by you; report its state only.
3. Config and secrets: any new env var, Fly secret, `fly.toml` change or worker/queue wiring it introduced exists in production. #1146 touched the nightly cool leg and #1142 the test worker cap; #1138 added an MCP compile lane.
4. Production health after the deploy: the health endpoints, the Celery queue canary, Sentry for new issue types since 2026-09-08 16:13 UTC, and Fly machine state for `precisiondocs-prod` (any machine in a crash loop or failed health check). Read-only. No unbounded live API spend: do not run report generation or chat turns to "smoke test"; a single cheap authenticated read is fine.
5. The open PRs #1136, #1140, #1151 are the steward goblin's (gb-pd-pr-review); do not touch them, do not merge anything.

## Report

`C:\dev\code-goblins\data\pd-prod-readiness-audit\report.md`: a table, one row per PR: gate verdict on merged head, migrations, config, plus a production-health section with timestamps and the exact commands or URLs read. Every "OK" cites the evidence; every gap says what is missing and the smallest fix. Notify the CFO when written. If you find something actively broken in production, notify immediately with the evidence before continuing.

## Constraints

- Read-only against production unless the CFO rules otherwise on a finding you escalate.
- No claim you did not check. "Not verified" is an acceptable cell; a guessed "OK" is not.
