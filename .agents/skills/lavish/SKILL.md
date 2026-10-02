---
name: lavish
description: Review surface for the CFO. Build an HTML artifact and open it with the third-party `lavish-axi` CLI so the Supreme Overlord reads it in the browser, annotates elements or selected text, queues prompts, and sends it all back to the CFO through the supervisor's poll. Use when several options, a structured report, a plan, a comparison, or a diagram is easier to judge visually than as prose. Not for a yes-or-no decision, and never a blocker: when lavish-axi is unavailable, deliver the same content as plain text.
argument-hint: <what the artifact should show>
---

# lavish

`lavish-axi` is a presentation-only dependency, exactly as it is for First Mate.
It is not part of the control plane: only a page named by its HTML file, a goblin's page wait (below) or a page review, uses it, and nonvisual work never waits for it.
`cfo doctor` reports it against its `0.1.79` floor and the Code Goblins build (`0.1.79-codegoblins.1` or newer) and stays healthy without it, printing `PRESENTATION_UNAVAILABLE`.

## Request

$ARGUMENTS

If the request above is non-empty, the Supreme Overlord invoked `/lavish` explicitly - build the artifact for that request now.
If it is empty, infer what to show from the conversation.

## When to use it

- **Plain chat** for a yes-or-no decision, a single recommendation, a status answer, or anything one paragraph carries.
- **A Scrawl page** when several options, trade-offs, a structured report, a plan, a comparison, a diagram, or a fleet-wide table justify a surface the Overlord can annotate.
  Scrawl is the Overlord's name for the review page `lavish-axi` serves, so call it Scrawl when naming it to him.
- **Neither, and say so** when lavish-axi is missing, below its floor, or not the Code Goblins build (the upstream package): name the unavailable state once ("visual review is unavailable, here it is as text"), deliver the content in plain text, and carry on.
  Never hang waiting for a surface that cannot open, and never hold unrelated dispatch for an install.
  When the Overlord wants the visual version, ask for consent to install the Code Goblins build of lavish-axi with the command `cfo doctor` prints, then confirm with `cfo doctor`.

## Workflow

1. Write the artifact as HTML under `.lavish/` in the working directory (for example `.lavish/dispatch-options.html`).
   Run `lavish-axi playbook` to list the playbooks, `lavish-axi playbook <id>` for the one that matches the content, and `lavish-axi design` for the design direction before writing.
   Start the page in the board's look and ask for a pick the way "The page" below says.
   Keep every other referenced asset beside the HTML and reference it with a relative path; apart from the two board stylesheets, a root-absolute path will not resolve.
2. Publish it with `cfo review --id <stable-id> --title "<what to look at>" --lavish <file>`, adding `--task <your id>` from a goblin's own terminal: the command opens the page without a browser and puts its link in the Command Center.
3. Keep working or end the turn: the supervisor polls the page, and what the Overlord sends reaches the CFO as a `review` wake, his feedback saved whole under `state/reviews/feedback/`.
   Never run `lavish-axi poll` yourself.
4. Apply every queued prompt, refresh the artifact, and publish it again under a new ID to keep the loop going.
5. Run `lavish-axi end <file>` when the review is done, or `lavish-axi export <file> [--out <path>]` for a portable single-file copy.

## The page

A Scrawl page starts in the board's look, so it reads as part of Code Goblins and not as another product.
This needs the Code Goblins build `0.1.79-codegoblins.3` or newer (`lavish-axi --version`); on an older build the page shows unstyled and its choices do not appear, so deliver the content as text instead.

Put these two lines in the `<head>` and write plain semantic HTML:

```html
<link rel="stylesheet" href="/design/board-tokens.css" />
<link rel="stylesheet" href="/design/board-page.css" />
```

The review server serves both, so the page always wears the board's current colours, its three fonts, edges and radii, and `lavish-axi export` carries them inline.
Add CSS of your own only for what the patterns below do not cover, and take every colour from the tokens (`var(--accent-green)`, `var(--mint)`, `var(--muted)`, `var(--glass-border)`, `var(--amber)`, `var(--red)`), never a new one.
A page that shows another product's UI, such as a PrecisionDocs mockup, wears that product's design instead, and any page may take the layout its content calls for.

Five patterns cover most pages.
Combine them freely inside `<main class="page">`; `lavish-axi design` prints each one's full markup under `board_look.patterns`.

| Pattern          | Use it for                                                      | Markup                                                                                                                                         |
| ---------------- | --------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| Report           | What was found or done: the conclusion first, then the findings | `<header class="page-head">` with `<p class="eyebrow">`, `<h1>`, `<p class="lead">`; `<section class="stats">` of `<div class="stat">`; `<div class="card">` |
| Comparison       | Options side by side, the recommended one lit                   | `<section class="compare">` of `<article class="card option">`, one with `recommended` and a `<span class="recommendation">`; `<ul class="pros">`, `<ul class="cons">` |
| Before and after | A picture review: the same view twice, each named               | `<section class="before-after">` of `<figure>` with `<figcaption><span class="chip">Before</span>` and `<span class="chip after">After</span>` |
| Evidence         | What was checked, what it showed, where to look                 | `<div class="table-wrap"><table class="evidence">`, results as `<span class="status ok">`, `warn` or `bad`                                     |
| Decision         | A pick is needed                                                | What is being decided and what each option means, a `<p class="callout">` for what he must know, then the declared choices below               |

### Choices

When the page asks him to pick, declare the question as data and Scrawl draws it where the declaration sits, as the Command Center draws a question: who asks, a plain radio list with the recommended option first and marked, Other for a written answer, and Send decision.

```html
<script type="application/json" data-lavish-choices>
  {
    "id": "poll-timeout",
    "asker": "your task id",
    "question": "Fix the poll timeout next, or keep 300 s?",
    "options": ["Fix it next", "Keep 300 s"],
    "recommended": "Fix it next"
  }
</script>
```

- Put the declaration at the end of the page, after what he needs to read first; a list of such objects asks several questions, each with its own `id`.
- Write each option as the answer itself, a short phrase, never a bare letter: two to four of them, with `recommended` equal to one.
- His pick reaches the CFO as the exact option text, or the words he wrote for Other; relay it to the goblin unchanged.
- Never build an answer form, a notes box, a dropdown or a send button of your own, and never ask the same thing again with `cfo question`: the page is the one place he answers.
  Everything else he wants to say goes in Scrawl's conversation box or as a comment on the page.

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
