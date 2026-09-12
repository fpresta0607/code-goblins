# Orchestrator patterns adopted in Code Goblins

This V1 borrows mechanisms, not control planes. Code Goblins remains a local, Windows-native Go program using Herdr and git worktrees.

| System | Mechanism inspected | Code Goblins decision |
|---|---|---|
| OpenAI Symphony | Explicit orchestration/run states, deterministic workspaces, bounded retries, reconciliation after restart | Adopt explicit route/budget/evidence state and fail-closed delivery; keep filesystem task state instead of a cloud scheduler. |
| Gas Town | Reality reconciliation, persistent work identity, integration-focused supervision | Adopt “discover, don’t trust stale state” as the operating rule; retain git/Herdr/GitHub as authorities rather than duplicating their state. |
| Agent Orchestrator | Project-aware coordinator with PR/CI/review visibility and worktree-scoped workers | Keep CFO as the durable coordinator and make PR proof and task evidence machine-readable. |
| Maestro | Fresh sessions, repeatable runs/playbooks, isolated worktrees, visible feedback loops | Durable task/runtime capsules are the fresh-session handoff contract; no transcript replay by default. |
| Paperclip | Heartbeats, budget hard-stops, structured cost/token events and audit trails | Add per-class repair/context budgets and outcome telemetry; retain Code Goblins' lightweight local watcher rather than an organization server. |
| Ruflo / Claude Flow | Routing, memory, audit/background patterns | Adopt deterministic lane routing and provenance-oriented project artifacts; avoid opaque global LLM memory. |
| OpenHands | Sandboxed execution lifecycle and event-triggered automation | Preserve isolation and explicit execution lifecycle; Code Goblins uses Windows worktrees/Herdr rather than requiring Docker or a cloud sandbox. |

The guiding filter is whether a mechanism lowers intervention or raises delivery confidence without adding a distributed control plane. Features whose primary value is large-scale organization management, hosted dashboards, or Kubernetes-style scheduling are intentionally out of scope.
