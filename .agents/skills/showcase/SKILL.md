---
name: showcase
description: Serve an agent artifact (plan, report, Markdown doc, wireframe, frontend mock, diff, CSV, or Mermaid diagram) on localhost with the showcase-axi CLI so the user can review it in a clean page, annotate elements or selected text, and queue feedback back to you. Use when about to give a plan, comparison, diagram, table, code diff, report, or anything easier to review visually than as prose. When the user says "lavish", they mean showcase.
argument-hint: <what the artifact should show>
---

# showcase

showcase-axi turns an artifact file into a review surface: a localhost page where the user reads the artifact, clicks elements or selects text to attach comments, queues prompts in a conversation panel, and sends everything back to you through `showcase-axi poll`.

Vocabulary: when the user says "lavish", they mean showcase-axi.
The word is kept as an alias for the old bundled skill it replaced; the commands are all `showcase-axi`.

## Request

$ARGUMENTS

If the request above is non-empty, the user invoked `/showcase` explicitly - build the artifact for that request now, following the workflow below.
If it is empty, infer what to show from the conversation.

## When to use

Use showcase-axi when the user should review something visually: a product or technical plan, a written report, a Markdown document, a wireframe or frontend mock, a diff or patch, a CSV table, or a Mermaid diagram.
Pick the file type that fits the content: Markdown for documents, HTML for mocks and wireframes, and the native format for diffs, CSVs, and diagrams.
showcase-axi renders each type deliberately, so a plan reads like a document and a mock presents like a product.

## Workflow

1. Create the artifact in `.showcase/` under the working directory (for example `.showcase/plan.md` or `.showcase/mock.html`).
2. Run `showcase-axi <file>` to open or resume the review session in the user's browser.
3. Run `showcase-axi poll <file>` to wait for the user's annotations and queued prompts.
   On the first poll, prefer `--agent-reply "<one-line summary of what you built and what to review first>"` so the conversation panel opens with context.
4. When poll returns feedback as JSON, apply every prompt, then open or refresh the artifact and poll again with `--agent-reply "<what changed>"` to keep the loop going.
5. Run `showcase-axi end <file>` when the review is finished.

## Poll discipline

- The poll stays silent until the user sends feedback or ends the session, then prints one JSON payload and exits.
  Leave it running; never kill it.
- Keep the poll in the foreground by default so feedback returns directly to you.
  A background poll is allowed only through a harness-native tracked background facility whose completion is guaranteed to resume or notify you.
  Never use `nohup`, shell `&`, `disown`, or a detached terminal just to keep polling alive.
- If the poll is killed or times out anyway, simply re-run it.
  Queued feedback is persisted under `.showcase/` beside the artifact and is never lost across restarts.
- `Send & End` in the browser ends the session.
  Its final feedback is still delivered once; after that payload arrives, do not poll again and do not reopen the session uninvited.
  Deliver any remaining updates directly in the conversation.
- Reopening a session the user ended is refused unless you pass `--reopen`; do that only when the user asks for another look.

## Command reference

- `showcase-axi <file> [--reopen]` - open or resume a review session.
  Accepts `.md`, `.html`, `.diff`/`.patch`, `.csv`, and `.mmd` (Mermaid); other text files render as Markdown.
- `showcase-axi poll <file> [--agent-reply "<message>"]` - long-poll for queued feedback; prints JSON on delivery.
- `showcase-axi end <file>` - end the session from the agent side; a plain reopen is still allowed later.
- `showcase-axi export <file> [--out <path>]` - write one portable self-contained HTML file with local assets inlined and the conversation frozen in.
  Remote references such as the Mermaid CDN stay links, so those need network to render.
- `showcase-axi stop` - stop the background server.

## Artifact guidance

- For HTML mocks, keep every referenced asset (CSS, images, scripts) beside the artifact file and reference it with a relative path; root-absolute paths will not resolve.
- The artifact itself is never modified or injected by the server, so what you write is what renders.
- Give the artifact a clear point of view: visual hierarchy, deliberate spacing, and structure (sections, tables, diagrams) over long prose.
- Export inlines only the top-level local assets referenced directly by the artifact (stylesheets, scripts, images). Multi-file ES-module mocks whose scripts import sibling files (e.g. `import ... from './lib.js'`) keep those relative imports in the exported file, so they are not yet fully self-contained; recursive import inlining is a v1 follow-up. Keep mocks single-file for a fully portable export.
