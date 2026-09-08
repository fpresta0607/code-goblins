# Pipeline policy implementation plan

Goal: correct validation telemetry and implement the CFO-approved Option A without changing a running gate.
Architecture: keep no-mistakes as the validation and delivery engine, version the small policy in JSON, snapshot task policy at spawn, and guard explicit AXI review responses with durable round evidence.
Stack: existing Go CLI, SQLite CLI read-only queries, and YAML v3.0.1 for safely preserving unrelated global settings without the old checksummed parser's known panic vulnerability.

## Approved boundaries

Ordinary and mechanical tasks allow two review repair cycles; high-risk tasks allow three, then remain unresolved.
Reviewer is Claude Opus high.
The global automatic review limit is zero; test, lint, rebase and CI allow one automatic follow-up.
Configuration applies only through an explicit command with the daemon stopped and no active runs, backup and drift report.
No automatic approval, new judge, upgrade or other repository change belongs in this PR.

## Execution

1. Add real SQLite regression fixtures in `internal/telemetry/telemetry_test.go`, demonstrate that failed-fast calls pollute the current mean, then split successful/failed/cancelled samples by model and role in `internal/telemetry/telemetry.go` and doctor output.
2. Add strict policy loading and class validation in `internal/pipeline/policy.go`, with executable parser tests and `config/pipeline.json` as the policy source.
3. Add global config drift/render/apply behavior and tests in `internal/pipeline/config.go`, preserving unrelated YAML and refusing non-idle application before writing a restricted backup.
4. Add read-only run/round inspection and bounded AXI driving in `internal/pipeline/driver.go`; test exhausted budgets, live automatic review overrides, unresolved approvals, explicit fixes, and tool failures.
5. Route CLI commands through `cmd/cfo/pipeline.go`, attach the resolved policy to spawned tasks and document actual supported commands in `docs/pipeline.md`.
6. Run focused regressions, Go vet and the repository suite, review the resulting change, then run the no-mistakes gate without blanket auto-approval.
7. Inspect exact-head required GitHub checks, merge only this goblin-authored PR under the granted authority, verify main content, and notify the CFO.

Each behavior is exercised through its public Go or CLI interface, not assertions over implementation source text.
The Phase 0 report remains task-local evidence rather than always-loaded project memory.
