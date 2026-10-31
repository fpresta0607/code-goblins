---
name: cfo-worktrees
description: Use when provisioning or changing a worktree, dependencies, env files, package caches, release cutoffs, Python start hooks or cleanup custody.
---

# CFO worktrees

In a source checkout, read `docs/contract/worktrees.md` at the repository root before acting at this trigger.
In an installed home, run `cfo help contract worktrees` to retrieve this build's complete procedure from the same policy source.
The installed file is also at `docs/contract/worktrees.md` under `CFO_HOME`.
A legacy tracked-checkout home keeps its own contract; do not infer activation from a newer binary or shared skill alone.
Follow only the steps applicable to the current role and authorized task.
For exact command syntax, use `cfo help <command>` or a listed subcommand topic.
Run `cfo help` to list available topics without loading their bodies.
If a required reference is missing or help fails, report the failure and stop the dependent action; do not infer the missing rules.
