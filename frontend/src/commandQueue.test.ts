import test from "node:test";
import assert from "node:assert/strict";
import { answeredBy, answeredLabel, chosenOption, documentFacts, failedSends, itemFor, nextOpenKey, outcomeIcon, questionOutcome, questionPage, sendState, settledIcon, settledItems, settledLabel, waitingItems } from "./commandQueue.ts";
import type { Action } from "./types.ts";
import { parseSnapshot, type BoardActivity } from "./types.ts";

const question = (id: string, task: string, created_at: string, status = "pending", extra: Record<string, unknown> = {}) => ({ id, identity: "i-" + id, task, created_at, status, options: ["A", "B"], ...extra });
const review = (id: string, task: string, created_at: string, state = "open", extra: Record<string, unknown> = {}) => ({ id, identity: "r-" + id, task, title: "Look at " + id, created_at, updated_at: created_at, state, ...extra });

test("questions and open review items share one stack: the CFO first, then goblins by longest wait", () => {
  const snapshot = parseSnapshot({ healthy: true,
    questions: [question("g-new", "billing", "2026-09-24T00:30:00Z"), question("cfo-late", "", "2026-09-24T00:40:00Z"), question("done", "billing", "2026-09-24T00:00:00Z", "queued"), question("g-undated", "notes", "")],
    reviews: [review("mockups", "steward", "2026-09-24T00:10:00Z"), review("cfo-report", "", "2026-09-24T00:20:00Z"), review("closed", "steward", "2026-09-24T00:05:00Z", "cleared")],
  });
  assert.deepEqual(waitingItems(snapshot).map((item) => item.key), ["review:cfo-report", "question:cfo-late", "review:mockups", "question:g-new", "question:g-undated"]);
  assert.deepEqual(waitingItems(snapshot, new Set(["question:done"])).map((item) => item.key),
    ["review:cfo-report", "question:cfo-late", "question:done", "review:mockups", "question:g-new", "question:g-undated"], "an item answered in this sitting keeps its place");
  assert.deepEqual(waitingItems(parseSnapshot({ healthy: true })), []);
  assert.equal(itemFor(snapshot, "review:mockups")?.kind, "review");
  assert.equal(itemFor(snapshot, "question:mockups"), undefined);
});

test("closed items are listed newest first with what became of them", () => {
  const snapshot = parseSnapshot({ healthy: true,
    questions: [
      question("a", "billing", "2026-09-24T00:10:00Z", "succeeded", { answer_id: "x", answer: "A", answer_kind: "option" }),
      question("b", "", "2026-09-24T00:20:00Z", "queued", { answer_id: "y", answer: "Ship it Friday", answer_kind: "other" }),
      question("c", "steward", "2026-09-24T00:30:00Z", "superseded"),
      question("e", "notes", "2026-09-24T00:00:00Z", "succeeded", { answer_id: "w", answer: "B", answer_kind: "option", answered_at: "2026-09-24T01:00:00Z" }),
      question("d", "notes", "2026-09-24T00:40:00Z"),
    ],
    reviews: [
      review("r1", "steward", "2026-09-24T00:15:00Z", "answered", { answer: "Go with B", answer_id: "z", delivered: true, updated_at: "2026-09-24T00:50:00Z" }),
      review("r2", "steward", "2026-09-24T00:16:00Z", "withdrawn", { reason: "the goblin found the answer", updated_at: "2026-09-24T00:25:00Z" }),
      review("r3", "", "2026-09-24T00:17:00Z", "cleared", { updated_at: "2026-09-24T00:18:00Z" }),
      review("r4", "steward", "2026-09-24T00:18:00Z"),
      review("r5", "steward", "2026-09-24T00:12:00Z", "cleared", { reason: "Cleared by the CFO: Decided: grid ships", updated_at: "2026-09-24T00:13:00Z" }),
    ],
  });
  assert.deepEqual(settledItems(snapshot).map((item) => item.key), ["question:e", "review:r1", "question:c", "review:r2", "question:b", "review:r3", "review:r5", "question:a"],
    "a question asked first but answered after a review was cleared sorts by when it was answered");
  const label = (key: string) => settledLabel(itemFor(snapshot, key)!, snapshot.actions);
  const cases: [string, string][] = [["question:a", "You chose A"], ["question:b", "You wrote: Ship it Friday"], ["question:c", "Superseded; the asker was replaced"],
    ["review:r1", "You wrote: Go with B"], ["review:r2", "Withdrawn: the goblin found the answer"], ["review:r3", "Cleared"],
    ["review:r5", "Cleared by the CFO: Decided: grid ships"]];
  for (const [key, text] of cases) assert.equal(label(key), text, key);
  assert.equal(parseSnapshot({ healthy: true }).reviews?.length, 0);
  assert.deepEqual(settledIcon(itemFor(snapshot, "review:r1")!, snapshot.actions), { icon: "check-double", tone: "succeeded" });
  assert.deepEqual(settledIcon(itemFor(snapshot, "question:c")!, snapshot.actions), { icon: "close", tone: "superseded" });
});

test("a closed question says what was chosen, by whom and when", () => {
  const at = "2026-09-24T01:00:00Z";
  const snapshot = parseSnapshot({ healthy: true, questions: [
    question("by-you", "billing", "2026-09-24T00:10:00Z", "succeeded", { answer_id: "x", answer: "B", answer_kind: "option", answered_option: "B", answered_by: "overlord", answered_at: at }),
    question("by-cfo", "billing", "2026-09-24T00:20:00Z", "succeeded", { answer_id: "", answer: "A. Ship it Friday", answer_kind: "option", answered_option: "A", answered_by: "cfo", answered_at: at }),
    question("older", "notes", "2026-09-24T00:30:00Z", "succeeded", { answer_id: "z", answer: "A", answer_kind: "option" }),
  ] });
  const [byYou, byCfo, older] = snapshot.questions ?? [];
  const cases: [typeof byYou, string, string, string][] = [
    [byYou, "B", "You chose B", "Answered by you"],
    [byCfo, "A", "The CFO chose A. Ship it Friday", "Answered by the CFO"],
    [older, "A", "You chose A", "Answered by you"],
  ];
  for (const [candidate, chosen, label, who] of cases) {
    assert.equal(chosenOption(candidate), chosen, candidate.id);
    assert.equal(settledLabel({ kind: "question", key: "question:" + candidate.id, question: candidate }, []), label, candidate.id);
    assert.equal(answeredBy(candidate), who, candidate.id);
  }
});

test("only an answer that reached its asker counts as answered", () => {
  const cases: [string, Record<string, unknown>, string, string, string][] = [
    ["a board answer", { status: "succeeded", answer_id: "x", answer: "A", answer_kind: "option", answered_option: "A", answered_by: "overlord" }, "answered", "You chose A", "check-double"],
    ["a board answer on its way", { status: "queued", answer_id: "x", answer: "A", answer_kind: "option" }, "answered", "You chose A", "check-double"],
    ["a cfo answer with an empty answer_id", { status: "succeeded", answer_id: "", answer: "B", answer_kind: "option", answered_option: "B", answered_by: "cfo" }, "answered", "The CFO chose B", "check-double"],
    ["a failed board answer", { status: "failed", answer_id: "x", answer: "A", answer_kind: "option", message: "the CFO already handled this question; nothing was sent" }, "failed", "Your answer did not reach the goblin", "warning"],
    ["an unconfirmed board answer", { status: "uncertain", answer_id: "x", answer: "A", answer_kind: "option" }, "uncertain", "Delivery unconfirmed", "warning"],
    ["a question cleared after a failure", { status: "cleared", answer_id: "x", answer: "A", answer_kind: "option" }, "cleared", "Closed without an answer", "close"],
    ["a superseded question without a message", { status: "superseded" }, "superseded", "Superseded; the asker was replaced", "close"],
    ["a question the CFO retired with --ack-blocking", { status: "superseded", message: "The CFO already handled this question." }, "superseded", "The CFO already handled this question.", "close"],
    ["a pending question", { status: "pending" }, "pending", "Waiting on you", "close"],
  ];
  for (const [name, fields, outcome, label, icon] of cases) {
    const [candidate] = parseSnapshot({ healthy: true, questions: [question("q", "billing", "2026-09-24T00:10:00Z", "pending", fields)] }).questions ?? [];
    assert.equal(questionOutcome(candidate), outcome, name);
    assert.equal(answeredLabel(candidate), label, name);
    assert.equal(outcomeIcon(questionOutcome(candidate)), icon, name);
  }
});

test("an answered review item is marked by whether its answer reached the asker", () => {
  const action = (status: string) => ({ id: "z", kind: "review_answer", status });
  const cases: [string, Record<string, unknown>, Record<string, unknown>[], string, string, string][] = [
    ["delivered to the goblin", { delivered: true }, [action("succeeded")], "You wrote: Go with B", "check-double", "succeeded"],
    ["an undelivered answer whose action aged out", { delivered: false }, [], "You wrote: Go with B (delivery no longer recorded)", "check", "queued"],
    ["a delivered answer whose action aged out", { delivered: true }, [], "You wrote: Go with B", "check-double", "succeeded"],
    ["queued for the goblin", { delivered: false }, [action("queued")], "You wrote: Go with B (not yet delivered to the goblin)", "check", "queued"],
    ["on its way to the goblin", { delivered: false }, [action("running")], "You wrote: Go with B (not yet delivered to the goblin)", "check", "queued"],
    ["an unconfirmed delivery", { delivered: false }, [action("uncertain")], "Delivery unconfirmed: inspect the goblin's pane before answering again", "warning", "uncertain"],
    ["handed to the CFO after the goblin was replaced", { delivered: false }, [action("succeeded")], "Sent to the CFO: Go with B", "check", "succeeded"],
    ["refused with no CFO to take it", { delivered: false }, [action("failed")], "Your answer did not reach the goblin", "warning", "failed"],
    ["the CFO's own item, not yet delivered", { task: "", delivered: false }, [action("running")], "You wrote: Go with B (not yet delivered to the CFO)", "check", "queued"],
  ];
  for (const [name, fields, actions, label, icon, tone] of cases) {
    const snapshot = parseSnapshot({ healthy: true, actions, reviews: [review("r", "steward", "2026-09-24T00:10:00Z", "answered", { answer: "Go with B", answer_id: "z", ...fields })] });
    const item = itemFor(snapshot, "review:r")!;
    assert.equal(settledLabel(item, snapshot.actions), label, name);
    assert.deepEqual(settledIcon(item, snapshot.actions), { icon, tone }, name);
  }
});

test("a run item waits in the stack while ready or running and settles with its exit code", () => {
  const run = (id: string, state: string, extra: Record<string, unknown> = {}) => ({ id, identity: "cfo-1", title: "Run " + id, shell: "powershell", command: "Get-Date", state, created_at: "2026-09-24T00:0" + id.length + ":00Z", ...extra });
  const snapshot = parseSnapshot({ healthy: true,
    questions: [question("g", "billing", "2026-09-24T00:00:30Z")],
    runs: [run("r", "ready"), run("rr", "running", { ran_at: "2026-09-24T00:10:00Z" }), run("rrr", "succeeded", { exit_code: 0, finished_at: "2026-09-24T00:20:00Z" }),
      run("rrrr", "failed", { exit_code: 3, reason: "The command exited with 3.", finished_at: "2026-09-24T00:30:00Z" }), run("rrrrr", "expired", { reason: "Nobody ran it within 24 hours.", finished_at: "2026-09-24T00:40:00Z" })],
  });
  assert.deepEqual(waitingItems(snapshot).map((item) => item.key), ["run:r", "run:rr", "question:g"], "the CFO's runs come before a goblin's question");
  const settled = settledItems(snapshot).filter((item) => item.kind === "run");
  assert.deepEqual(settled.map((item) => item.key), ["run:rrrrr", "run:rrrr", "run:rrr"]);
  const cases: [string, string, string][] = [["run:rrr", "Finished · exit 0", "check"], ["run:rrrr", "Failed · exit 3: The command exited with 3.", "warning"], ["run:rrrrr", "Expired: Nobody ran it within 24 hours.", "close"]];
  for (const [key, label, icon] of cases) {
    const item = itemFor(snapshot, key)!;
    assert.equal(settledLabel(item, snapshot.actions), label, key);
    assert.equal(settledIcon(item, snapshot.actions).icon, icon, key);
  }
});

test("after a send the stack moves on to the next open item, wrapping, and ends when nothing is left", () => {
  const snapshot = parseSnapshot({ healthy: true,
    questions: [question("a", "", "2026-09-24T00:00:00Z"), question("b", "billing", "2026-09-24T00:01:00Z"), question("c", "notes", "2026-09-24T00:02:00Z", "queued", { answer_id: "z" }), question("d", "steward", "2026-09-24T00:03:00Z")],
  });
  const stack = waitingItems(snapshot, new Set(["question:c"]));
  assert.deepEqual(stack.map((item) => item.key), ["question:a", "question:b", "question:c", "question:d"]);
  assert.equal(nextOpenKey(stack, "question:b"), "question:d", "an item already answered is passed over");
  assert.equal(nextOpenKey(stack, "question:d"), "question:a", "past the end it wraps to the first open item");
  assert.equal(nextOpenKey(stack, "question:d", new Set(["question:a", "question:b"])), null, "items sent in this sitting are done even before the snapshot says so");
  assert.equal(nextOpenKey(stack, "question:gone"), "question:a", "an item that left the stack starts from the top");
  assert.equal(nextOpenKey(stack, "question:c", new Set(["question:a", "question:c"])), "question:d", "from a sent card the next open item still shows, passing over a sent one the snapshot has not caught up with");
  assert.equal(nextOpenKey(waitingItems(parseSnapshot({ healthy: true, questions: [question("a", "", "2026-09-24T00:00:00Z")] })), "question:a"), null, "the only item is never its own next");
});

test("a question opens only its own asker's latest live review page", () => {
  const page = (id: string, extra: Partial<BoardActivity>): BoardActivity => ({ id, kind: "review", task_id: "", generation: "", source: "", target: "", state: "active", url: "http://127.0.0.1:4000/" + id, at: "", until: "", ...extra });
  const presentations = [
    page("old", { task_id: "billing", generation: "g2" }),
    page("walkthrough", { kind: "browser", task_id: "billing", generation: "g2" }),
    page("replaced", { task_id: "billing", generation: "g1" }),
    page("other-task", { task_id: "notes", generation: "g2" }),
    page("new", { task_id: "billing", generation: "g2" }),
    page("cfo-mine", { cfo_identity: "cfo-a" }),
    page("cfo-replaced", { cfo_identity: "cfo-b" }),
  ];
  const snapshot = parseSnapshot({ healthy: true, questions: [question("g", "billing", "2026-09-24T00:00:00Z", "pending", { generation: "g2" }), question("c", "", "2026-09-24T00:00:00Z", "pending", { identity: "cfo-a" })] });
  const [goblin, cfo] = snapshot.questions!;
  assert.equal(questionPage(presentations, goblin)?.id, "new", "the newest page of the same goblin session");
  assert.equal(questionPage(presentations, cfo)?.id, "cfo-mine", "the CFO's page from the registration that asked");
  assert.equal(questionPage(presentations.filter((event) => event.id !== "new" && event.id !== "old"), goblin), undefined, "never a walkthrough, a replaced session's page or another task's");
});

test("a review item says whether the supervisor watches its page for his answer", () => {
  const snapshot = parseSnapshot({ healthy: true, reviews: [
    review("watched", "steward", "2026-09-24T00:00:00Z", "open", { lavish: "http://127.0.0.1:4000/a", lavish_page: "C:/pages/a.html" }),
    review("linked", "steward", "2026-09-24T00:00:00Z", "open", { lavish: "http://127.0.0.1:4000/b" }),
  ] });
  assert.deepEqual(snapshot.reviews!.map((item) => item.watched), [true, false]);
});

test("a document item reads as its file: its type, its size and who sent it", () => {
  const snapshot = parseSnapshot({ healthy: true, reviews: [
    review("setbacks-doc", "", "2026-09-24T00:00:00Z", "open", { document: { name: "setbacks 1204 Oak St.pdf", size: 2516582, kind: "application/pdf" } }),
    review("parcels-doc", "steward", "2026-09-24T00:00:00Z", "open", { document: { name: "parcels.CSV", size: 900, link: "https://files.example.com/parcels" } }),
    review("notes-doc", "steward", "2026-09-24T00:00:00Z", "open", { document: { name: "README", size: 20480 } }),
    review("mockups", "steward", "2026-09-24T00:00:00Z"),
  ] });
  const [pdf, csv, plain, noDocument] = snapshot.reviews!;
  assert.deepEqual(pdf.document, { name: "setbacks 1204 Oak St.pdf", size: 2516582, kind: "application/pdf", link: "" });
  assert.equal(csv.document?.link, "https://files.example.com/parcels");
  assert.equal(noDocument.document, null);
  assert.equal(documentFacts(pdf.document!, "the CFO"), "PDF · 2.4 MB · from the CFO");
  assert.equal(documentFacts(csv.document!, "steward"), "CSV · 900 B · from steward");
  assert.equal(documentFacts(plain.document!, "steward"), "File · 20 KB · from steward");
});

const action = (id: string, kind: string, status: string) => ({ id, kind, status, question_id: "", answer_kind: "", task_id: "", generation: "", message: "", text: "", file: "", line: 0, side: "", updated_at: "" }) as Action;
const submitted = (id: string, payload: Record<string, unknown>) => ({ id, payload: JSON.stringify(payload) });

test("a send shows as done at once, confirmed once delivered, and failed only when refused or not delivered", () => {
  const answer = submitted("a1", { kind: "goblin_answer", text: "SQLite" });
  const opened = submitted("c1", { kind: "review_clear", review_id: "doc", text: "Downloaded" });
  const cleared = submitted("c2", { kind: "review_clear", review_id: "look" });
  const cases: [string, Parameters<typeof sendState>, ReturnType<typeof sendState>][] = [
    ["nothing sent", [{ submission: null, error: "" }, []], undefined],
    ["just clicked, no receipt yet", [{ submission: answer, error: "" }, []], { failed: false, confirmed: false, heading: "Sent", cleared: false }],
    ["queued behind the goblin's turn", [{ submission: answer, error: "", receipt: action("a1", "goblin_answer", "queued") }, []], { failed: false, confirmed: false, heading: "Sent", cleared: false }],
    ["delivered", [{ submission: answer, error: "" }, [action("a1", "goblin_answer", "succeeded")]], { failed: false, confirmed: true, heading: "Sent", cleared: false }],
    ["the request refused", [{ submission: answer, error: "that review is not open" }, []], { failed: true, confirmed: false, heading: "Sent", cleared: false }],
    ["delivery failed", [{ submission: answer, error: "" }, [action("a1", "goblin_answer", "failed")]], { failed: true, confirmed: false, heading: "Sent", cleared: false }],
    ["delivery unconfirmed", [{ submission: answer, error: "" }, [action("a1", "goblin_answer", "uncertain")]], { failed: true, confirmed: false, heading: "Sent", cleared: false }],
    ["a document downloaded", [{ submission: opened, error: "" }, []], { failed: false, confirmed: false, heading: "Downloaded", cleared: true }],
    ["an item cleared", [{ submission: cleared, error: "" }, []], { failed: false, confirmed: false, heading: "Cleared", cleared: true }],
  ];
  for (const [name, args, want] of cases) assert.deepEqual(sendState(...args), want, name);
});

test("an item sent and moved past comes back when its send fails, and only then", () => {
  const drafts = {
    "question:ok": { submission: submitted("ok", { kind: "goblin_answer" }), error: "" },
    "question:refused": { submission: submitted("refused", { kind: "goblin_answer" }), error: "that question is not open" },
    "question:lost": { submission: submitted("lost", { kind: "goblin_answer" }), error: "" },
    "question:unsent": { submission: null, error: "" },
  };
  const actions = [action("ok", "goblin_answer", "succeeded"), action("lost", "goblin_answer", "uncertain")];

  const failed = failedSends(new Set(["question:ok", "question:refused", "question:lost", "question:unsent", "question:gone"]), drafts, actions);

  assert.deepEqual(failed, ["question:refused", "question:lost"]);
});
