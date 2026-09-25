---
name: lavish
description: Review surface for the CFO. Build an HTML artifact and open it with the third-party `lavish-axi` CLI so the Supreme Overlord reads it in the browser, annotates elements or selected text, queues prompts, and sends it all back to the CFO through the supervisor's poll. Use when several options, a structured report, a plan, a comparison, or a diagram is easier to judge visually than as prose. Not for a yes-or-no decision, and never a blocker: when lavish-axi is unavailable, deliver the same content as plain text.
argument-hint: <what the artifact should show>
---

# lavish

`lavish-axi` is a presentation-only dependency, exactly as it is for First Mate.
It is not part of the control plane: only a goblin's page wait uses it (below), and nonvisual work never waits for it.
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
2. Publish it with `cfo review --id <stable-id> --title "<what to look at>" --lavish <file>`, adding `--task <your id>` from a goblin's pane: the command opens the page without a browser and puts its link in the Command Center.
3. Keep working or end the turn: the supervisor polls the page, and what the Overlord sends reaches the CFO as a `review` wake, his feedback saved whole under `state/reviews/feedback/`.
   Never run `lavish-axi poll` yourself.
4. Apply every queued prompt, refresh the artifact, and publish it again under a new ID to keep the loop going.
5. Run `lavish-axi end <file>` when the review is done, or `lavish-axi export <file> [--out <path>]` for a portable single-file copy.

## A goblin's page

Nobody polls a page themselves, and a goblin least of all: the poll takes the Overlord's feedback where only that goblin sees it, and nobody else learns that a question is waiting.
When a goblin needs his answer on a page, it opens the page with `lavish-axi <file> --no-open` and registers the wait with `cfo notify <id> --waiting-on overlord "<why>" --lavish <file>`.
The page's link goes on the wait's Command Center item, the supervisor polls the page, and his feedback reaches the CFO as a `review` wake with the whole reply saved under `state/reviews/feedback/`; relay it to the goblin with `cfo send`.
A goblin's page that needs no answer is reported with `cfo present` instead.

## The supervisor's poll

- `cfo serve` runs one bounded `lavish-axi poll` at a time for each open item that names a page, for as long as the item is open, and stops it when the item closes.
- A poll's feedback is delivered once, so nobody else may poll the page: a second poll would take the Overlord's answer where no wake reaches it.
- `Send & End` in the browser ends the session, and its final feedback reaches the CFO the same way.
  After that, do not reopen the session uninvited; `--reopen` is for when the Overlord asks for another look.

## Sharing

`lavish-axi share` publishes the artifact to a third-party host (ht-ml.app), **public by default**.
Share only when the Supreme Overlord asks for it, and confirm public or `--private` with them first; a private share's password is a shared secret, so hand it over with the URL.

## Ownership

This skill is owned by this repo, not synced down from user scope: it carries the CFO's rules around the tool, while the tool's own command reference lives in `lavish-axi --help`.
Consult that help rather than memorizing flags.
