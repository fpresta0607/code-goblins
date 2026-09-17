---
name: lavish
description: Review surface for the CFO. Build an HTML artifact and open it with the third-party `lavish-axi` CLI so the Supreme Overlord reads it in the browser, annotates elements or selected text, queues prompts, and sends it all back through `lavish-axi poll`. Use when several options, a structured report, a plan, a comparison, or a diagram is easier to judge visually than as prose. Not for a yes-or-no decision, and never a blocker: when lavish-axi is unavailable, deliver the same content as plain text.
argument-hint: <what the artifact should show>
---

# lavish

`lavish-axi` is a presentation-only dependency, exactly as it is for First Mate.
It is not part of the control plane: no `cfo` command needs it, no goblin is told about it, and nonvisual work never waits for it.
`cfo doctor` reports it with its `0.1.71` floor and stays healthy without it, printing `PRESENTATION_UNAVAILABLE`.

## Request

$ARGUMENTS

If the request above is non-empty, the Supreme Overlord invoked `/lavish` explicitly - build the artifact for that request now.
If it is empty, infer what to show from the conversation.

## When to use it

- **Plain chat** for a yes-or-no decision, a single recommendation, a status answer, or anything one paragraph carries.
- **Lavish** when several options, trade-offs, a structured report, a plan, a comparison, a diagram, or a fleet-wide table justify a surface the Overlord can annotate.
- **Neither, and say so** when lavish-axi is missing or below its floor: name the unavailable state once ("visual review is unavailable, here it is as text"), deliver the content in plain text, and carry on.
  Never hang waiting for a surface that cannot open, and never hold unrelated dispatch for an install.
  When the Overlord wants the visual version, ask for consent to run `npm install -g lavish-axi@latest`, then confirm with `cfo doctor`.

## Workflow

1. Write the artifact as HTML under `.lavish/` in the working directory (for example `.lavish/dispatch-options.html`).
   Run `lavish-axi playbook` to list the playbooks, `lavish-axi playbook <id>` for the one that matches the content, and `lavish-axi design` for the design direction before writing.
   Keep every referenced asset beside the HTML and reference it with a relative path; a root-absolute path will not resolve.
2. Run `lavish-axi <file>` to open or resume the session.
3. Run `lavish-axi poll <file>` and leave it in the foreground until it returns.
4. Apply every queued prompt, refresh the artifact, and poll again to keep the loop going.
5. Run `lavish-axi end <file>` when the review is done, or `lavish-axi export <file> [--out <path>]` for a portable single-file copy.

## Poll discipline

- The poll stays silent until feedback arrives, then prints one JSON payload and exits.
  Leave it running; never kill it.
- Keep it in the foreground.
  A background poll is allowed only through a harness-native tracked background facility whose completion is guaranteed to resume or notify you - never `nohup`, shell `&`, `disown`, or a detached terminal.
  This matters more for the CFO than for anyone else: a turn that ends while a detached poll holds the Overlord's feedback is feedback nobody reads.
- If the poll is killed or times out, re-run it. Queued feedback is not lost.
- `Send & End` in the browser ends the session and its final feedback is delivered once.
  After that payload, do not poll again and do not reopen the session uninvited; `--reopen` is for when the Overlord asks for another look.

## Sharing

`lavish-axi share` publishes the artifact to a third-party host (ht-ml.app), **public by default**.
Share only when the Supreme Overlord asks for it, and confirm public or `--private` with them first; a private share's password is a shared secret, so hand it over with the URL.

## Ownership

This skill is owned by this repo, not synced down from user scope: it carries the CFO's rules around the tool, while the tool's own command reference lives in `lavish-axi --help`.
Consult that help rather than memorizing flags.
