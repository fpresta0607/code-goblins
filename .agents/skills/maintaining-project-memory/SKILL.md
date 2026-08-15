---
name: maintaining-project-memory
description: Use when adding, revising, pruning, or extracting durable project instructions in AGENTS.md or CLAUDE.md, especially after the user says to remember something or when conditional guidance bloats always-loaded context.
---

# Maintaining Project Memory

## Principle

Project memory is concise, always-relevant repository context.
Skills hold procedures that are useful only for certain tasks.

## Choose the Destination

Put information in project memory when it is durable and broadly useful across sessions:

- repository purpose and layout
- domain terminology
- important component boundaries and data flows
- canonical build, test, and verification entry points
- project-wide conventions
- verified gotchas learned from corrected agent behavior

Put information in a project skill when it is conditional or procedural:

- end-to-end testing workflows
- deployment and release procedures
- migrations and data repair
- specialized debugging or review checklists
- tool-specific sequences and reference material

Do not store session summaries, temporary task state, speculative conclusions, command catalogs, or instructions already owned by another skill, rule, hook, or agent.

## Shared Project Layout

When a repository supports multiple agents, prefer one canonical source:

```text
<repo>/.agents/AGENTS.md
<repo>/AGENTS.md                    -> .agents/AGENTS.md
<repo>/CLAUDE.md                    -> .agents/AGENTS.md
<repo>/.agents/skills/<name>/SKILL.md
<repo>/.claude/skills/<name>        -> .agents/skills/<name>
```

Use links only after inspecting existing files and preserving any unique content.
If links are impractical, keep small runtime-specific routing files and designate one canonical file explicitly.

## Extraction Procedure

1. Read the current project memory and the relevant project files.
2. Confirm the guidance is accurate and still needed.
3. Identify sections that are useful only under a clear task condition.
4. Create one narrowly scoped project skill with a trigger-only `description`.
5. Move the complete procedure into that skill without duplicating it.
6. Leave at most one short routing sentence in project memory when automatic discovery is insufficient.
7. Verify each runtime resolves the intended instruction file and discovers the skill.

## Updating Memory from Feedback

Convert a correction into a reusable fact, not a transcript of the mistake.
Keep the smallest rule that would have prevented the failure.
Reference concrete project paths or commands when they are stable.
Remove obsolete guidance when the codebase changes.

## Safety

Treat downloaded skills as code-adjacent supply-chain inputs.
Read every instruction and bundled script before enabling it.
Do not install a skill based only on popularity or claims of better performance.
