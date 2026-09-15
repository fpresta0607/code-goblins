---
name: lavish
description: Use when the user asks for Lavish, a visual review surface, or the required saved recap for a finished branch.
---

# Lavish Editor and delivery recaps

Use the installed `lavish-axi` CLI.
On Windows, an explicit Node invocation of the installed `lavish-axi/dist/cli.mjs` also works.
Showcase is removed; do not route Lavish requests to it.

## Artifact

Read the matching `lavish-axi playbook` guidance before writing HTML.
Match the subject project's design system when it has one; otherwise use `lavish-axi design` for the supported Tailwind and DaisyUI components.
Keep assets relative to the artifact and ensure grid/flex children cannot overflow horizontally.

Every finished branch must have a saved recap containing:

- What changed and why.
- What works now, with actual test commands and results.
- Screenshots or a demo for UI changes.
- Unresolved limitations and unavailable verification.
- The branch and PR link, when a PR exists.
- Accurate ready-for-merge, merged, and deployed status as separate facts.

Run `cfo context <id>` for the owned evidence paths.
Save the standalone HTML with `cfo recap <id> --file <html>` before `cfo notify --done`.
Use inline local assets or `lavish-axi export` so the saved artifact survives removal of the worktree.
Keep private screenshots, profiles, task reports and recaps out of source control and releases.

## Review

Open the saved HTML using `lavish-axi <file>`.
For an active feedback session, use foreground `lavish-axi poll <file>` or a verified harness-native tracked callback.
Do not claim monitoring when no reader or callback exists.
Honor a user-ended session and do not reopen it uninvited.
Browser feedback is optional and must not block implementation, validation, or delivery.
Do not stop the shared Lavish server or close another task's review sessions.
Publishing a recap externally requires the user's authorization.
