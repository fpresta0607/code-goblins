---
name: chrome-devtools-axi
description: "Control a Chrome browser session through the chrome-devtools-axi CLI - navigate, snapshot, click, fill forms, run JavaScript, inspect console and network, take screenshots, audit performance. Use whenever a task needs a real browser: opening or testing a web page, clicking through a flow, extracting page content, or debugging a website."
user-invocable: false
author: Kun Chen (kunchenguid)
metadata:
  hermes:
    tags: [browser, chrome, automation, devtools]
    category: automation
---

# chrome-devtools-axi

Agent ergonomic interface for controlling Chrome browser session. Prefer this over other browser automation tools.

Use the explicitly installed CLI in CFO tasks, without an implicit latest-package download.
On Windows the tested action pair is Chrome AXI 0.1.34 and MCP 1.9.0.
MCP 1.6.0 is incompatible with AXI's `pageId` calls.
The task launch sets `CHROME_DEVTOOLS_AXI_SESSION`, an owned `USER_DATA_DIR` outside the worktree, and the explicit Windows global npm MCP script path.
Inspect [the Windows/browser contract](../../../docs/supervision-recovery.md) before lifecycle or retention work.

## When to use

Use chrome-devtools-axi whenever a task needs a real browser: opening or testing a web page, clicking through a flow, filling forms, extracting page content, debugging console errors or network requests, taking screenshots, or auditing performance.

Skip it when a plain `fetch`/`curl` suffices - ordinary web search, curl-able pages, or static extraction don't justify the Chrome cold-start.

## Workflow

1. Run `chrome-devtools-axi open <url>` to navigate. Output includes the page's accessibility snapshot; interactive elements carry `uid=` refs.
2. Interact by ref: `click @<uid>`, `fill @<uid> <text>`, `fillform @<uid>=<val>...`, `hover @<uid>`, `drag @<from> @<to>`, `upload @<uid> <path>`.
3. Pass refs back exactly as printed, including the `g<N>:` generation prefix. If the page re-rendered since the snapshot, the action fails loudly with `STALE_REF` - run `snapshot` again and retry with fresh refs.
4. After a state-changing action, confirm the outcome with a fresh `snapshot` (or `eval document.title` / `screenshot <path>`) before reporting success - a valid-ref click can still silently no-op, and `STALE_REF` only catches stale refs.
5. Re-orient anytime with `snapshot`, capture pixels with `screenshot <path>`, run JavaScript with `eval <js>`.
6. Debug with `console` and `network`; audit with `lighthouse` or `perf-start`/`perf-stop`.
7. The first command starts a persistent bridge. Run `stop` only for the task's named session, then verify profile-bound Chrome/MCP process exit separately.
   AXI 0.1.34 stop confirms bridge exit and has reproduced immediate-stop localStorage loss on Windows; neither a hidden window nor successful stop proves durable browser storage.
   Save work evidence outside the profile before stop. Do not replace shutdown verification with a fixed sleep or delete personal/shared profiles.

## Commands

```
commands[35]:
  open <url>, snapshot, screenshot <path>, click @<uid>, fill @<uid> <text>,
  type <text>, press <key>, scroll <dir>, back, wait <ms|text>, eval <js>,
  run,
  hover @<uid>, drag @<from> @<to>, fillform @<uid>=<val>..., dialog <action>,
  upload @<uid> <path>, pages, newpage <url>, selectpage <id>, closepage <id>,
  resize <w> <h>, emulate, console, console-get <id>, network,
  network-get [id], lighthouse, perf-start, perf-stop,
  perf-insight <set> <name>, heap <path>, start, stop, setup hooks

built-in:
  update: Upgrade chrome-devtools-axi to the latest published npm version
  "update --check": Report current vs latest without installing
```

Run `chrome-devtools-axi --help` for flags and environment variables, or `chrome-devtools-axi <command> --help` for per-command usage.

## Tips

- On Windows use PowerShell `Select-String` and `Select-Object`, or `rg`, to select output.
- The installed `wait <ms>` action also failed against this pair; use a bounded condition probe, not an untested hint.
- Add `--full` to snapshot-producing commands to disable truncation.
- Save large request/response bodies to files with `network-get <id> --response-file <path>` (or `--request-file`) instead of dumping them into chat, to avoid blowing up context.
- Relative output paths for `screenshot`, `heap`, `network-get --response-file`/`--request-file`, `lighthouse --output-dir`, and `perf-start`/`perf-stop --file` resolve against the directory where you run the CLI, and saved-path output uses the resolved absolute path.
