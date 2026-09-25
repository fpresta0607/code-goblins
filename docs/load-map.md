# Where agents look for their rules

This page maps every file the CFO, its goblins and the no-mistakes gate read for instructions, rules, skills, commands, hooks and MCP servers, in the order each harness reads them.
It exists because the same knowledge can reach an agent through several doors, and a rule placed behind the wrong door either never loads or loads twice.

The facts below were read from the installed harnesses' own source and documentation and checked against live sessions.
Versions checked: Claude Code 2.1.281, Codex CLI 0.154.0, Pi 0.85.1, Kimi Code 0.38.0 and no-mistakes v1.75.1.
Recheck a row when you upgrade a harness.

## At a glance

| | Claude Code | Codex | Pi | Kimi Code |
| --- | --- | --- | --- | --- |
| Global instructions | `~/.claude/CLAUDE.md` | `~/.codex/AGENTS.override.md`, else `~/.codex/AGENTS.md` | `~/.pi/agent/AGENTS.md` (or `CLAUDE.md`) | `~/.kimi-code/AGENTS.md`, then `~/.agents/AGENTS.md` |
| Project instructions | `CLAUDE.md` (not `AGENTS.md`) | `AGENTS.md`, capped at 32 KiB | `AGENTS.md`, else `CLAUDE.md` | `AGENTS.md` (never `CLAUDE.md`) |
| Where the project walk stops | the git root, including a worktree's own | the git root, including a worktree's own | the filesystem root | the git root, including a worktree's own |
| Always-loaded rules | `~/.claude/rules/*.md`, `.claude/rules/*.md` | none | none | none |
| Skills, user | `~/.claude/skills` | `~/.codex/skills`, `~/.agents/skills` | `~/.pi/agent/skills`, `~/.agents/skills` | `~/.kimi-code/skills`, `~/.agents/skills` |
| Skills, project | `.claude/skills` | `.agents/skills`, `.codex/skills` | `.agents/skills`, `.pi/skills` | `.agents/skills`, `.kimi-code/skills` |
| Same skill name in both | user copy wins, listed once | **both listed** | project copy wins | project copy wins |
| Commands or prompts | `~/.claude/commands`, `.claude/commands` | none (`~/.codex/prompts` is not read) | `~/.pi/agent/prompts`, `.pi/prompts` | none |
| Hooks | settings files, merged, plus plugins | `hooks.json` and `[hooks]`, per trusted layer, each hook hash-trusted | extensions | `[[hooks]]` in `~/.kimi-code/config.toml` |
| MCP | `~/.claude.json`, `.mcp.json`, plugins | `config.toml` only | none | `~/.kimi-code/mcp.json`, `.mcp.json` when trusted |

## Claude Code: the CFO and Claude goblins

**Instructions.**
Claude reads a managed policy file if one exists, then `~/.claude/CLAUDE.md`, then `CLAUDE.md` and `.claude/CLAUDE.md` in each directory from the repository root down to the working directory, each followed by its `CLAUDE.local.md`.
`@path` imports expand up to four levels deep.
A `CLAUDE.md` in a subdirectory loads when Claude first reads a file there.
Claude never reads `AGENTS.md` on its own: this repository's `CLAUDE.md` links to `AGENTS.md`, so the CFO reads the contract with a file read rather than having it injected.

A goblin's worktree sits inside the project checkout at `<project>/.worktrees/gb-<id>` and has its own `.git` file.
Claude stops its upward walk there: a live probe showed that a nested worktree loads neither the enclosing checkout's `CLAUDE.md` nor its `.claude/skills`.

**Rules.**
Every file in `~/.claude/rules/` and then `.claude/rules/` loads at startup unless its frontmatter names `paths:`, in which case it loads when Claude reads a matching file.
Rules sit beside `CLAUDE.md` with no precedence between them, so a rule that repeats or contradicts `CLAUDE.md` is paid for twice or followed arbitrarily.
Only Claude reads these directories.

**Skills.**
Claude lists skills from `~/.claude/skills`, the project's `.claude/skills`, nested `.claude/skills` below the working directory, enabled plugins (namespaced `plugin:skill`) and skills synced from claude.ai.
It does not read `.agents/skills`; `install.cmd -Dev` makes `.claude/skills` a junction to `.agents/skills` so the CFO sees this repository's skills.
When a user skill and a project skill share a name, the user copy wins and the listing shows one entry, so a project skill can be silently shadowed.
`skillOverrides` in settings sets a skill to `on`, `name-only`, `user-invocable-only` or `off`.
Every listed description is paid for in every session; a skill body loads only when used.

**Commands and subagents.**
`~/.claude/commands` and `.claude/commands` are listed with the skills.
Subagents come from `.claude/agents`, `~/.claude/agents` and plugins.

**Hooks.**
Hooks merge across every settings scope: managed settings, `--settings`, `.claude/settings.local.json`, `.claude/settings.json`, `~/.claude/settings.json`, and enabled plugins.
A `SessionStart` hook's output is injected into the session; output larger than about 10 KB is saved to a file and only a short preview is injected.
`cfo install` merges the CFO's hooks into `~/.claude/settings.json`.
Every goblin pane carries `CFO_ROLE=goblin`, and the CFO's hooks do nothing when they see it.

**MCP.**
The CFO's session gets servers from `~/.claude.json` (user and per-project), the project's `.mcp.json` once approved, enabled plugins, and claude.ai connectors.
A Claude goblin is launched with `--strict-mcp-config --mcp-config <file>`, so it gets exactly the servers in that file and none of the user, plugin or claude.ai servers.

**Auto-memory.**
Claude keeps a per-repository memory index (`MEMORY.md` under `~/.claude/projects/<repository>/memory/`) and loads it every session.
It is keyed to the repository, so every worktree of that repository shares it: a Claude goblin working on code-goblins loads the CFO's memory index too.

## Codex

**Instructions.**
The global file is `~/.codex/AGENTS.override.md` if it is non-empty, otherwise `~/.codex/AGENTS.md`.
Project files load from the project root (the nearest `.git`, which a worktree's `.git` file satisfies) down to the working directory, one per directory: `AGENTS.override.md`, else `AGENTS.md`, else any configured fallback name.
`CLAUDE.md` is not read unless it is configured as a fallback.
All project files share one budget, `project_doc_max_bytes`, 32 KiB by default; the global file does not count against it.
Past the budget the text is cut off.

This repository's `AGENTS.md` is well over that budget, so a Codex session loses roughly the last third of the file, which includes dispatch policy, delivery, supervision, the board question contract and escalation.
Until the contract is shorter, run a Codex CFO with `project_doc_max_bytes = 65536` in `~/.codex/config.toml`.

**Skills.**
Codex scans, in order: the project's `.codex/skills` directories (nearest first), `~/.codex/skills`, `~/.agents/skills`, its system skills, plugins, and the project's `.agents/skills` from the root down to the working directory.
It removes duplicates by `SKILL.md` path, never by name, so the same skill at two paths is listed twice, and a bare `$name` mention of an ambiguous name is ignored.
Codex already reads the project's `.agents/skills`, so a `.codex/skills` junction to it only adds a second route to the same skills; `install.cmd -Dev` no longer creates one and removes the one an earlier bootstrap made.

**Prompts.**
Codex 0.154 has no loader for `~/.codex/prompts`; commands reach Codex as skills or plugins.

**Hooks.**
Codex reads `hooks.json` and the `[hooks]` table of `config.toml` from each enabled layer: system, user, and each trusted project `.codex`.
A worktree takes its project hooks from the main checkout's `.codex`.
A hook runs only when its hash is trusted in the user configuration (review it in `/hooks`).
`cfo hooks install codex` writes the CFO's native hooks into `~/.codex/hooks.json`.

**MCP.**
Servers come only from `[mcp_servers]` in the `config.toml` layers; a project `.mcp.json` is never read.
A Codex goblin therefore uses the operator's own Codex servers, not the filtered project configuration spawn prepares.

**Trust.**
Project `.codex` configuration and hooks load only for a trusted project.
Trust is looked up by exact path, and a worktree resolves to its main checkout.

## Pi

**Instructions.**
Pi reads one context file per directory, the first of `AGENTS.override.md`, `AGENTS.md`, `AGENTS.MD`, `CLAUDE.md` and `CLAUDE.MD`: first `~/.pi/agent/`, then every directory from the filesystem root down to the working directory.
It does not stop at the git root, but in a nested worktree it skips the enclosing checkout's file when it has the same name as the worktree's own.
There is no size cap.
`SYSTEM.md` and `APPEND_SYSTEM.md` replace or extend the system prompt; `--no-context-files` turns context files off.

**Skills.**
Pi ranks project skills first (`.pi/skills`, then `.agents/skills` from the working directory up to the git root, both only in a trusted project), then `~/.pi/agent/skills`, then `~/.agents/skills`, then packages.
The first skill with a name wins and later ones raise a collision warning; a linked duplicate of the same directory is dropped silently.

**Prompts, hooks and MCP.**
Prompt templates come from `~/.pi/agent/prompts` and a trusted `.pi/prompts`.
Pi has no hooks file: extensions play that role, from `~/.pi/agent/extensions`, a trusted `.pi/extensions`, settings and packages.
`cfo hooks install pi` writes the CFO's extension to `~/.pi/agent/extensions/cfo-native.ts`.
Pi has no MCP support.

**Trust.**
Pi asks only when the project has `.pi` resources or an `.agents/skills` directory in the working directory or an ancestor; a trusted parent directory covers its worktrees.

## Kimi Code

**Instructions.**
Kimi reads `~/.kimi-code/AGENTS.md`, then `~/.agents/AGENTS.md`, then, in each directory from the git root (a worktree's own) down to the working directory, `.kimi-code/AGENTS.md` and `AGENTS.md`.
It never reads `CLAUDE.md`.
Above 32 KiB it warns but does not cut.

**Skills.**
Project skills come from the git root only: `.kimi-code/skills`, then `.agents/skills`.
User skills come from `~/.kimi-code/skills`, then `~/.agents/skills`.
A project skill replaces a user skill of the same name.

**Hooks and MCP.**
Hooks are the `[[hooks]]` entries in `~/.kimi-code/config.toml` only.
MCP servers come from `~/.kimi-code/mcp.json`, then the git root's `.mcp.json` and `.kimi-code/mcp.json` when the folder is trusted.
Kimi has no flag for an MCP file, so spawn copies the filtered configuration to the worktree's `.mcp.json` when that path is free.

**Trust.**
The trust dialog appears in every new working directory and gates only project MCP.

## What `cfo spawn` adds to a goblin

Everything in the user rows above still loads for a goblin; spawn adds the following on top.

1. **The worktree.** `<project>/.worktrees/gb-<id>`, detached from the default branch, with its own `.git` file, so project instructions and skills come from the worktree's checkout of the project.
   A goblin working on code-goblins itself therefore reads this repository's `CLAUDE.md` or `AGENTS.md`, which both say it is a contributor, not the CFO.
2. **The pane environment.** `CFO_ROLE=goblin`, `CFO_HOME`, `CFO_STATE_OVERRIDE`, `GOTMPDIR`, the shared cache roots, and the project's declared credentials, sourced from a file rather than typed.
3. **The launch.** Claude: `--dangerously-skip-permissions --strict-mcp-config [--mcp-config <file>]`.
   Codex: `--dangerously-bypass-approvals-and-sandbox`.
   Pi: `--tui-mode regular`.
   Kimi: no extra flags.
   Model and effort flags follow the lane table in `data/routing.json`.
   Each harness's trust dialog is confirmed automatically.
4. **MCP.** The token-authenticated subset of the project's `.mcp.json`, written under the task's temporary directory: Claude receives it by flag, Kimi reads the worktree copy, and Codex and Pi do not use it.
5. **The first message.** "Read the brief at <path> and follow it exactly," followed by how to report with `cfo notify` and, in `no-mistakes` mode, the task's frozen pipeline policy.

## The no-mistakes gate

**Why this repository has a `.no-mistakes.yaml`.**
It is this repository's own settings file for the no-mistakes gate, the local pipeline (review, test, document, lint, push, pull request, CI) every change passes before it merges.
It does two things.
It sets `disable_project_settings: true`, so the gate's reviewers and fixers never read this repository's `AGENTS.md` or `CLAUDE.md`: those files turn an agent into the CFO, and a reviewer that believes it runs the fleet would start driving it.
It also sets how many automatic fix rounds each step may take.
The gate reads it from the default branch, and `cfo pipeline run` refuses to start a run in a repository without it.

**What a gate agent reads.**

1. `~/.no-mistakes/config.yaml`: the agent chain, each agent's arguments, automatic fix limits and timeouts.
2. The repository's `.no-mistakes.yaml`, from the trusted default branch.
3. For a CFO-managed task, `cfo pipeline run` first checks the task's frozen policy snapshot (`state/tasktmp/<id>/pipeline.json`, taken from `config/pipeline.json` at spawn) against the live configuration and refuses on drift.
4. The agent then runs in the gate's own worktree with `NO_MISTAKES_GATE=1`, and with project instructions neutralized: Claude through `--setting-sources`, Codex through `project_doc_max_bytes=0`, and Pi through `--no-context-files`.
   With the setting on, the gate refuses any other agent, because it cannot neutralize one.
5. User-level files still load: the agent's global instructions, rules, skills and hooks.

## The CFO's memory

The `cfo session-start` digest prints the first queued rows of `data/backlog.md`, every task's metadata and recent status, and `data/projects.md`, `data/overlord.md` and `data/learnings.md` in full.
Apart from the shipped lane table, `data/routing.json`, `data/` is the operator's private fleet state and never part of this repository.
A Claude CFO also loads its auto-memory index for this checkout.
The `stow` skill keeps `data/overlord.md`, `data/learnings.md` and the harness memory index inside a startup budget, and the backlog current: directives stay word for word, operating facts decay unless re-confirmed, and stale history moves to `data/memory-archive.md`, which no session loads.

## Third-party skills

The tools the fleet drives publish their own skills, and this repository does not copy them.
Install each once at user scope with `npx skills add kunchenguid/<tool> --skill <tool> -g`, so Codex, Pi and Kimi find it in `~/.agents/skills` and Claude Code in `~/.claude/skills`; the three exact commands are in the [README's Quick start](../README.md#quick-start).

A copy inside this repository would reach only sessions opened in this checkout, never a goblin working on another project, and would compete with the user copy under the collision rules above.
`cfo doctor` checks that these tools are installed but not yet that their skills are; checking for each skill at user scope is a natural next addition to it.

## Keeping it clean

- One skill, one directory: a skill this repository owns lives in `.agents/skills/` and nowhere else, and a third-party skill is installed once at user scope from its owner.
- A rule every harness should follow belongs in the global instruction file each harness reads (keep those files identical with hardlinks), not in `~/.claude/rules`, which only Claude reads.
- `SessionStart` output is paid for in every session and every goblin; a hook that repeats a skill description adds cost and nothing else.
- Keep `AGENTS.md` under 32 KiB so Codex reads all of it.
