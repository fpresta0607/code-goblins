import test from "node:test";
import assert from "node:assert/strict";
import { answerMark, answerReason, answeredBy, answeredElsewhere, answeredLabel, canChange, cardKey, chosenOption, closedElsewhere, documentFacts, holdsUnsent, itemFor, newestItemOf, nextOpenKey, notSent, openKeys, outcomeIcon, questionOutcome, questionPage, reviewLine, sendState, settledIcon, settledItems, settledLabel, waitingItems, waitReason, waitsOnOverlord, waitTarget } from "./commandQueue.ts";
import type { Action, Review } from "./types.ts";
import { parseSnapshot, type BoardActivity } from "./types.ts";

const question = (id: string, task: string, created_at: string, status = "pending", extra: Record<string, unknown> = {}) => ({ id, identity: "i-" + id, task, created_at, status, options: ["A", "B"], ...extra });
const review = (id: string, task: string, created_at: string, state = "open", extra: Record<string, unknown> = {}) => ({ id, identity: "r-" + id, task, title: "Look at " + id, created_at, updated_at: created_at, state, ...extra });

test("a goblin's wait on the Overlord is a status card, anything else under review is answered", () => {
  const cases: [string, Partial<Review>, boolean][] = [
    ["a goblin waiting on him", { id: "waiting-billing-7", task: "billing" }, true],
    ["another goblin's wait id", { id: "waiting-billing-7", task: "notes" }, false],
    ["a goblin's review page", { id: "plan-billing", task: "billing" }, false],
    ["the CFO's own item", { id: "waiting--7", task: "" }, false],
  ];
  for (const [name, fields, want] of cases) assert.equal(waitsOnOverlord(fields as Review), want, name);
});

test("a goblin's wait says what it waits on and opens it: its page, a file it delivered, or the link it gave, never its question to the CFO", () => {
  const wait = (title: string, extra: Record<string, unknown> = {}) => review("waiting-billing-7", "billing", "2026-09-27T10:00:00Z", "open", { title: "Waiting on you: " + title, ...extra });
  const target = (item: ReturnType<typeof wait>, others: { questions?: unknown[]; reviews?: unknown[] } = {}) => {
    const snapshot = parseSnapshot({ healthy: true, questions: others.questions || [], reviews: [item, ...(others.reviews || [])] });
    return waitTarget((snapshot.reviews || []).find((candidate) => candidate.id === "waiting-billing-7")!, snapshot);
  };
  const page = "http://127.0.0.1:4387/p/plan";
  assert.deepEqual(target(wait("pick a plan (page " + page + ")", { lavish: page })), { kind: "page", url: page, label: "Open review", says: "It waits on your answer on its review page." });
  assert.equal(target(wait("answer my question"), { questions: [question("notify-billing-5", "billing", "2026-09-27T09:00:00Z"), question("notify-billing-6", "billing", "2026-09-27T09:30:00Z")] }), null, "its own question is the CFO's to answer, so the wait opens nothing");
  assert.deepEqual(target(wait("read the report"), { reviews: [review("report-billing", "billing", "2026-09-27T09:00:00Z", "open", { document: { name: "report.pdf", size: 10, kind: "pdf", link: "" } }), review("report-notes", "notes", "2026-09-27T09:00:00Z", "open", { document: { name: "notes.pdf", size: 10, kind: "pdf", link: "" } })] }),
    { kind: "item", key: "review:report-billing", label: "Open the file", says: "It waits on you to open report.pdf." });
  assert.deepEqual(target(wait("sign in to Stripe, then tell me", { link: "https://dashboard.stripe.com/login" })), { kind: "page", url: "https://dashboard.stripe.com/login", label: "Open the link", says: "It waits on you at dashboard.stripe.com." });
  assert.equal(target(wait("log in to Stripe")), null, "a wait that names nothing to open offers nothing");
  // The Overlord, 2026-09-28: a card offered Open the link for a hostname the
  // goblin only named in its prose. Only the link the goblin gave opens.
  assert.equal(target(wait("so https://mcp.precisiondocs.ai serves the connector")), null, "a web address in the prose is not the link");
  assert.equal(target(wait("open it", { link: "file:///C:/secret.txt" })), null, "only a web link opens");
});

test("a wait's card reads the goblin's reason, without the queue's prefix or the page it already opens", () => {
  const page = "http://127.0.0.1:4387/p/plan";
  const reason = (title: string, lavish = "") => waitReason(review("waiting-billing-7", "billing", "2026-09-27T10:00:00Z", "open", { title, lavish }) as unknown as Review);
  assert.equal(reason("Waiting on you: log in to Stripe"), "log in to Stripe");
  assert.equal(reason("Waiting on you: pick a plan (page " + page + ")", page), "pick a plan");
  assert.equal(reason("A title without the prefix"), "A title without the prefix");
  assert.equal(reason("Waiting on you: Add the records\n| Type | Name |\n| --- | --- |\n| CNAME | `mcp` |"), "Add the records\n| Type | Name |\n| --- | --- |\n| CNAME | `mcp` |");
});

// The Overlord, 2026-10-02: "The Command Center should only give me questions
// that the CFO has for the Overlord, for me." A goblin's question is the
// CFO's to answer, so it is never offered to him: it does not wait in the
// stack, is not announced, is not what its goblin has waiting on him, and no
// wait points at it. Once answered it is in History like any other.
test("a goblin's question is the CFO's to answer: it never waits on the Overlord, and is in History once answered", () => {
  const snapshot = parseSnapshot({ healthy: true,
    tasks: [{ id: "billing", phase: "blocked", generation: "g1", verified: false, archived: false, merged: false }],
    questions: [question("g-asks", "billing", "2026-10-02T19:14:14Z"), question("cfo-asks", "", "2026-10-02T19:20:00Z"),
      question("g-answered", "billing", "2026-10-02T19:00:00Z", "succeeded", { answered_by: "cfo", answered_option: "A", answered_at: "2026-10-02T19:01:00Z" }),
      question("g-on-its-page", "billing", "2026-10-02T19:10:00Z", "pending", { page: "plan-billing" })],
    reviews: [review("plan-billing", "billing", "2026-10-02T19:09:00Z"), review("waiting-billing-9", "billing", "2026-10-02T19:15:00Z", "open", { title: "Waiting on you: answer my question" })],
  });
  assert.deepEqual(waitingItems(snapshot).map((item) => item.key), ["question:cfo-asks", "review:plan-billing", "review:waiting-billing-9"], "only what is for him waits");
  assert.deepEqual([...openKeys(snapshot)].sort(), ["question:cfo-asks", "review:plan-billing", "review:waiting-billing-9"], "a goblin's question is never announced; its page made for his eyes is");
  assert.equal(newestItemOf(snapshot, "billing")?.key, "review:waiting-billing-9", "what its goblin has waiting on him is never its question to the CFO");
  assert.equal(waitTarget((snapshot.reviews || []).find((candidate) => candidate.id === "waiting-billing-9")!, snapshot), null, "a wait never opens a question that is the CFO's to answer");
  assert.deepEqual(settledItems(snapshot).map((item) => item.key), ["question:g-answered"], "answered, it is in History; still waiting on the CFO, it is not");
});

test("goblins' items follow the In progress order, then unplaced goblins', each by longest wait", () => {
  const snapshot = parseSnapshot({ healthy: true, attention: ["notes", "billing"],
    tasks: ["notes", "billing", "alpha", "zeta"].map((id) => ({ id, phase: "working", generation: "g1", verified: false, archived: false, merged: false })),
    questions: [question("cfo", "", "2026-09-24T00:40:00Z")],
    reviews: [review("billing-old", "billing", "2026-09-24T00:00:00Z"), review("notes-new", "notes", "2026-09-24T00:30:00Z"), review("gone", "gone", "2026-09-23T00:00:00Z"),
      review("alpha-late", "alpha", "2026-09-24T02:00:00Z"), review("zeta-early", "zeta", "2026-09-24T00:05:00Z"), review("notes-old", "notes", "2026-09-24T00:10:00Z")],
  });
  assert.deepEqual(waitingItems(snapshot).map((item) => item.key), ["question:cfo", "review:notes-old", "review:notes-new", "review:billing-old", "review:gone", "review:zeta-early", "review:alpha-late"]);
});

test("the CFO's questions and open review items share one stack: the CFO first, then goblins by longest wait", () => {
  const snapshot = parseSnapshot({ healthy: true,
    questions: [question("cfo-late", "", "2026-09-24T00:40:00Z"), question("done", "", "2026-09-24T00:00:00Z", "queued")],
    reviews: [review("mockups", "steward", "2026-09-24T00:10:00Z"), review("cfo-report", "", "2026-09-24T00:20:00Z"), review("closed", "steward", "2026-09-24T00:05:00Z", "cleared"), review("g-new", "billing", "2026-09-24T00:30:00Z"), review("g-undated", "notes", "")],
  });
  assert.deepEqual(waitingItems(snapshot).map((item) => item.key), ["review:cfo-report", "question:cfo-late", "review:mockups", "review:g-new", "review:g-undated"]);
  assert.deepEqual(waitingItems(snapshot, new Set(["question:done"])).map((item) => item.key),
    ["question:done", "review:cfo-report", "question:cfo-late", "review:mockups", "review:g-new", "review:g-undated"], "an item answered in this sitting keeps its place");
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
  const cases: [string, string][] = [["question:a", "You chose A"], ["question:b", "You wrote: Ship it Friday (not yet delivered to the CFO)"], ["question:c", "Superseded; the asker was replaced"],
    ["review:r1", "You wrote: Go with B"], ["review:r2", "Withdrawn: the goblin found the answer"], ["review:r3", "Cleared"],
    ["review:r5", "Cleared by the CFO: Decided: grid ships"]];
  for (const [key, text] of cases) assert.equal(label(key), text, key);
  assert.equal(parseSnapshot({ healthy: true }).reviews?.length, 0);
  assert.deepEqual(settledIcon(itemFor(snapshot, "review:r1")!, snapshot.actions), { icon: "check-double", tone: "succeeded" });
  assert.deepEqual(settledIcon(itemFor(snapshot, "question:c")!, snapshot.actions), { icon: "close", tone: "superseded" });
  assert.deepEqual(settledIcon(itemFor(snapshot, "question:a")!, snapshot.actions), { icon: "check-double", tone: "succeeded" });
  assert.deepEqual(settledIcon(itemFor(snapshot, "question:b")!, snapshot.actions), { icon: "check", tone: "queued" }, "an answer on its way has one check until it is delivered");
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
    [byCfo, "A", "The CFO chose A", "Answered by the CFO"],
    [older, "A", "You chose A", "Answered by you"],
  ];
  for (const [candidate, chosen, label, who] of cases) {
    assert.equal(chosenOption(candidate), chosen, candidate.id);
    assert.equal(settledLabel({ kind: "question", key: "question:" + candidate.id, question: candidate }, []), label, candidate.id);
    assert.equal(answeredBy(candidate), who, candidate.id);
  }
});

test("History marks who answered: you, the CFO, or the CFO while you were away", () => {
  const cases: [string, Record<string, unknown>, string][] = [
    ["his board answer", { status: "succeeded", answer_id: "x", answer: "A", answer_kind: "option", answered_option: "A", answered_by: "overlord" }, "you"],
    ["his board answer on its way, which keeps its single check", { status: "queued", answer_id: "x", answer: "A", answer_kind: "option" }, ""],
    ["his answer in chat, recorded by the CFO", { status: "succeeded", answer: "A", answer_kind: "option", answered_option: "A", answered_by: "overlord", answered_in: "chat" }, "you"],
    ["his change to the CFO's answer", { status: "succeeded", answer: "B", answer_kind: "option", answered_option: "B", answered_by: "overlord", replaced_answer: "A" }, "you"],
    ["the CFO's answer", { status: "succeeded", answer: "A", answer_kind: "option", answered_option: "A", answered_by: "cfo" }, "cfo"],
    ["the CFO's answer while he was away", { status: "succeeded", answer: "A", answer_kind: "option", answered_option: "A", answered_by: "cfo", answered_away: true }, "away"],
    ["a failed board answer", { status: "failed", answer_id: "x", answer: "A", answer_kind: "option" }, ""],
    ["a superseded question", { status: "superseded" }, ""],
    ["a question he dismissed", { status: "cleared" }, ""],
  ];
  for (const [name, fields, want] of cases) {
    const [candidate] = parseSnapshot({ healthy: true, questions: [question("q", "billing", "2026-09-24T00:10:00Z", "pending", fields)] }).questions ?? [];
    assert.equal(answerMark(candidate), want, name);
  }
});

test("History gives the CFO's reason, or the CFO's answer his change replaced, on its own line", () => {
  const cases: [string, Record<string, unknown>, string][] = [
    ["the CFO's answer with a reason", { status: "succeeded", answer: "A. Ship it Friday", answer_kind: "option", answered_option: "A", answered_by: "cfo" }, "Reason: Ship it Friday"],
    ["the CFO's answer without one", { status: "succeeded", answer: "A", answer_kind: "option", answered_option: "A", answered_by: "cfo" }, ""],
    ["his change to the CFO's answer", { status: "succeeded", answer: "B", answer_kind: "option", answered_option: "B", answered_by: "overlord", replaced_answer: "A" }, "Replaced the CFO's answer: A"],
    ["his written answer that reads like one", { status: "succeeded", answer_id: "x", answer: "A. but slower", answer_kind: "other", answered_by: "overlord" }, ""],
  ];
  for (const [name, fields, want] of cases) {
    const [candidate] = parseSnapshot({ healthy: true, questions: [question("q", "billing", "2026-09-24T00:10:00Z", "pending", fields)] }).questions ?? [];
    assert.equal(answerReason(candidate), want, name);
  }
});

test("he can change only the CFO's answer to a goblin still on it that has reported nothing since", () => {
  const answered = "2026-10-06T02:00:00Z";
  const cfo = { status: "succeeded", answer: "A", answer_kind: "option", answered_option: "A", answered_by: "cfo", answered_at: answered, generation: "g1", change_id: "" };
  const cases: [string, Record<string, unknown>, Record<string, unknown> | null, Record<string, unknown>[], boolean][] = [
    ["the CFO's answer, nothing reported since", cfo, { reported_at: "2026-10-06T01:59:00Z" }, [], true],
    ["the CFO's answer, a goblin that never reported", cfo, { reported_at: "0001-01-01T00:00:00Z" }, [], true],
    ["the goblin reported since", cfo, { reported_at: "2026-10-06T02:00:01Z" }, [], false],
    ["the goblin restarted", cfo, { generation: "g2", reported_at: "2026-10-06T01:59:00Z" }, [], false],
    ["the goblin is gone", cfo, null, [], false],
    ["his own answer", { ...cfo, answered_by: "overlord", answer_id: "x" }, { reported_at: "2026-10-06T01:59:00Z" }, [], false],
    ["the CFO's own question", { ...cfo, task: "" }, { reported_at: "2026-10-06T01:59:00Z" }, [], false],
    ["his change on its way", { ...cfo, change_id: "c1" }, { reported_at: "2026-10-06T01:59:00Z" }, [{ id: "c1", kind: "answer_change", status: "running" }], false],
    ["his change that failed", { ...cfo, change_id: "c1" }, { reported_at: "2026-10-06T01:59:00Z" }, [{ id: "c1", kind: "answer_change", status: "failed" }], true],
  ];
  for (const [name, fields, task, actions, want] of cases) {
    const snapshot = parseSnapshot({ healthy: true, actions, tasks: task ? [{ id: "billing", phase: "working", generation: "g1", verified: false, archived: false, merged: false, ...task }] : [], questions: [question("q", "billing", "2026-10-06T01:50:00Z", "pending", fields)] });
    assert.equal(canChange(snapshot.questions![0], snapshot), want, name);
  }
});

test("only an answer that reached its asker counts as answered", () => {
  const cases: [string, Record<string, unknown>, string, string, string][] = [
    ["a board answer", { status: "succeeded", answer_id: "x", answer: "A", answer_kind: "option", answered_option: "A", answered_by: "overlord" }, "answered", "You chose A", "check-double"],
    ["a board answer on its way", { status: "queued", answer_id: "x", answer: "A", answer_kind: "option" }, "answered", "You chose A (not yet delivered to the goblin)", "check-double"],
    ["a cfo answer with an empty answer_id", { status: "succeeded", answer_id: "", answer: "B", answer_kind: "option", answered_option: "B", answered_by: "cfo" }, "answered", "The CFO chose B", "check-double"],
    ["a failed board answer", { status: "failed", answer_id: "x", answer: "A", answer_kind: "option", message: "the CFO already handled this question; nothing was sent" }, "failed", "Your answer did not reach the goblin", "warning"],
    ["an unconfirmed board answer", { status: "uncertain", answer_id: "x", answer: "A", answer_kind: "option" }, "uncertain", "Not confirmed: check the goblin's terminal", "warning"],
    ["a question cleared after a failure", { status: "cleared", answer_id: "x", answer: "A", answer_kind: "option" }, "cleared", "Closed without an answer", "close"],
    ["a superseded question without a message", { status: "superseded" }, "superseded", "Superseded; the asker was replaced", "close"],
    ["a question the CFO answered and retired with --ack-blocking", { status: "succeeded", answered_by: "cfo", message: "Answered by the CFO." }, "answered", "The CFO answered it", "check-double"],
    ["a pending question", { status: "pending" }, "pending", "Waiting on you", "close"],
    ["an answer he gave in chat, recorded by the CFO", { status: "succeeded", answer: "Stop them", answer_kind: "option", answered_option: "Stop them", answered_by: "overlord", answered_in: "chat" }, "answered", "You answered in chat · recorded by the CFO", "check-double"],
    ["a question he dismissed", { status: "cleared", message: "You dismissed it: answered elsewhere or no longer needed." }, "cleared", "You dismissed it: answered elsewhere or no longer needed.", "close"],
  ];
  for (const [name, fields, outcome, label, icon] of cases) {
    const [candidate] = parseSnapshot({ healthy: true, questions: [question("q", "billing", "2026-09-24T00:10:00Z", "pending", fields)] }).questions ?? [];
    assert.equal(questionOutcome(candidate), outcome, name);
    assert.equal(answeredLabel(candidate), label, name);
    assert.equal(outcomeIcon(questionOutcome(candidate)), icon, name);
  }
});

test("an answer that never arrived reads in History as what to do about it, in the supervisor's words", () => {
  // Arrange
  const advice = "Your answer was typed for the CFO, which has not picked it up. Open its terminal and press Enter if your answer is waiting in its box; if it is not there, type it to the CFO.";
  const snapshot = parseSnapshot({ healthy: true,
    questions: [question("lost", "", "2026-10-01T16:54:00Z", "uncertain", { answer_id: "a-lost", answer: "Lift it", answer_kind: "option" }),
      question("aged", "", "2026-10-01T16:54:00Z", "uncertain", { answer_id: "a-aged", answer: "Lift it", answer_kind: "option" })],
    reviews: [review("plan", "gb-a", "2026-10-01T16:54:00Z", "answered", { answer_id: "a-plan", answer: "Go with B" })],
    actions: [{ id: "a-lost", kind: "cfo_answer", status: "uncertain", message: advice, advice }, { id: "a-plan", kind: "review_answer", status: "uncertain", message: advice, advice }] });

  // Act
  const labels = Object.fromEntries(settledItems(snapshot).map((item) => [item.key, settledLabel(item, snapshot.actions)]));

  // Assert
  assert.equal(labels["question:lost"], advice);
  assert.equal(labels["review:plan"], advice);
  assert.equal(labels["question:aged"], "Not confirmed: check the CFO's terminal", "an answer whose action is no longer recorded keeps the plain warning");
});

test("an answered review item is marked by whether its answer reached the asker", () => {
  const action = (status: string) => ({ id: "z", kind: "review_answer", status });
  const cases: [string, Record<string, unknown>, Record<string, unknown>[], string, string, string][] = [
    ["delivered to the goblin", { delivered: true }, [action("succeeded")], "You wrote: Go with B", "check-double", "succeeded"],
    ["an undelivered answer whose action aged out", { delivered: false }, [], "You wrote: Go with B (delivery no longer recorded)", "check", "queued"],
    ["a delivered answer whose action aged out", { delivered: true }, [], "You wrote: Go with B", "check-double", "succeeded"],
    ["queued for the goblin", { delivered: false }, [action("queued")], "You wrote: Go with B (not yet delivered to the goblin)", "check", "queued"],
    ["on its way to the goblin", { delivered: false }, [action("running")], "You wrote: Go with B (not yet delivered to the goblin)", "check", "queued"],
    ["sent behind the goblin's turn", { delivered: false }, [{ ...action("running"), awaiting: { task: "steward", generation: "g1", since: "2026-10-01T00:00:00Z" } }],
      "You wrote: Go with B (not yet delivered to the goblin)", "check", "queued"],
    ["handed to a busy CFO after the goblin was replaced", { delivered: false }, [{ ...action("running"), awaiting: { host: "cfo-host", since: "2026-10-01T00:00:00Z" } }],
      "You wrote: Go with B (not yet delivered to the CFO)", "check", "queued"],
    ["an unconfirmed delivery", { delivered: false }, [action("uncertain")], "Not confirmed: check the goblin's terminal before answering again", "warning", "uncertain"],
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

test("a review he answered on its own page reads as answered by him there, with a check, never as withdrawn", () => {
  const snapshot = parseSnapshot({ healthy: true, reviews: [
    review("waiting-theme-7", "theme", "2026-09-30T23:20:00Z", "answered", { lavish: "http://127.0.0.1:4387/session/f26e", lavish_page: "C:\\data\\theme\\review.html",
      answered_by: "overlord", answered_in: "page", reason: "You answered on its page; the CFO relays it to the goblin.", updated_at: "2026-09-30T23:29:38Z" }),
    review("cfo-plan", "", "2026-09-30T23:21:00Z", "answered", { answered_by: "overlord", answered_in: "page", reason: "You answered on its page; the CFO has it." }),
    review("waiting-notes-2", "notes", "2026-09-30T23:00:00Z", "withdrawn", { reason: "notes reported again: working: tests" }),
    review("r1", "steward", "2026-09-24T00:15:00Z", "answered", { answer: "Go with B", answer_id: "z", delivered: true }),
  ] });
  const cases: [string, string, boolean][] = [
    ["review:waiting-theme-7", "You answered on its page; the CFO relays it to the goblin.", true],
    ["review:cfo-plan", "You answered on its page; the CFO has it.", true],
  ];
  for (const [key, label, elsewhere] of cases) {
    const item = itemFor(snapshot, key)!;
    assert.equal(settledLabel(item, snapshot.actions), label, key);
    assert.deepEqual(settledIcon(item, snapshot.actions), { icon: "check-double", tone: "succeeded" }, key);
    assert.equal(answeredElsewhere(item), elsewhere, key);
  }
  assert.equal(answeredElsewhere(itemFor(snapshot, "review:waiting-notes-2")!), false, "a withdrawn item was not answered");
  assert.equal(answeredElsewhere(itemFor(snapshot, "review:r1")!), false, "an answer sent from its card is not an answer given elsewhere");
});

test("a question asked with its review page open is that page's card, never a second one", () => {
  const page = "http://127.0.0.1:4387/session/ec2e";
  const snapshot = parseSnapshot({ healthy: true,
    questions: [question("notify-polish-3585", "polish", "2026-09-30T23:43:02Z", "pending", { page: "waiting-polish-3584" }), question("pick-a-theme", "", "2026-09-30T23:44:00Z")],
    reviews: [review("waiting-polish-3584", "polish", "2026-09-30T23:42:53Z", "open", { lavish: page, lavish_page: "C:/data/review-kanban/index.html", question: "notify-polish-3585" })],
  });
  assert.deepEqual(waitingItems(snapshot).map((item) => item.key), ["question:pick-a-theme", "review:waiting-polish-3584"], "one card for the page and its question");
  assert.equal(cardKey(snapshot, "question:notify-polish-3585"), "review:waiting-polish-3584", "the question is shown by its page's card");
  assert.equal(cardKey(snapshot, "question:pick-a-theme"), "question:pick-a-theme");
});

test("a question answered on the page that carried it says so", () => {
  const [onPage, cleared] = parseSnapshot({ healthy: true, questions: [
    question("notify-polish-3585", "polish", "2026-09-30T23:43:02Z", "succeeded", { answer: "Build as drawn", answer_kind: "other", answered_by: "overlord", answered_in: "page", answered_at: "2026-09-30T23:50:00Z" }),
    question("notify-polish-3586", "polish", "2026-09-30T23:43:02Z", "succeeded", { answer: "", answer_kind: "other", answered_by: "overlord", answered_in: "page", answered_at: "2026-09-30T23:50:00Z" }),
  ] }).questions ?? [];
  assert.equal(questionOutcome(onPage), "answered");
  assert.equal(answeredLabel(onPage), "You answered on its page: Build as drawn");
  assert.equal(answeredLabel(cleared), "You answered on its page");
});

test("a goblin's command waits with its goblin's items, after the CFO's own", () => {
  // Arrange
  const run = (id: string, task: string, created_at: string) => ({ id, identity: "i-" + id, task, title: "Run " + id, shell: "powershell", command: "Get-Date", state: "ready", created_at });
  const snapshot = parseSnapshot({ healthy: true, attention: ["notes", "billing"],
    reviews: [review("waiting-notes-3", "notes", "2026-10-02T01:00:00Z")],
    runs: [run("run-billing-7", "billing", "2026-10-02T00:50:00Z"), run("install-main", "", "2026-10-02T01:10:00Z")] });

  // Act
  const order = waitingItems(snapshot).map((item) => item.key);

  // Assert
  assert.deepEqual(order, ["run:install-main", "review:waiting-notes-3", "run:run-billing-7"]);
});

test("a run item waits in the stack while ready or running and settles with its exit code", () => {
  const run = (id: string, state: string, extra: Record<string, unknown> = {}) => ({ id, identity: "cfo-1", title: "Run " + id, shell: "powershell", command: "Get-Date", state, created_at: "2026-09-24T00:0" + id.length + ":00Z", ...extra });
  const snapshot = parseSnapshot({ healthy: true,
    reviews: [review("g", "billing", "2026-09-24T00:00:30Z")],
    runs: [run("r", "ready"), run("rr", "running", { ran_at: "2026-09-24T00:10:00Z" }), run("rrr", "succeeded", { exit_code: 0, finished_at: "2026-09-24T00:20:00Z" }),
      run("rrrr", "failed", { exit_code: 3, reason: "The command exited with 3.", finished_at: "2026-09-24T00:30:00Z" }), run("rrrrr", "expired", { reason: "Nobody ran it within 24 hours.", finished_at: "2026-09-24T00:40:00Z" })],
  });
  assert.deepEqual(waitingItems(snapshot).map((item) => item.key), ["run:r", "run:rr", "review:g"], "the CFO's runs come before a goblin's item");
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
    questions: [question("a", "", "2026-09-24T00:00:00Z"), question("b", "", "2026-09-24T00:01:00Z"), question("c", "", "2026-09-24T00:02:00Z", "queued", { answer_id: "z" }), question("d", "", "2026-09-24T00:03:00Z")],
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

test("a send shows as done at once, Sent once delivered, and failed only when refused or not delivered", () => {
  const answer = submitted("a1", { kind: "goblin_answer", text: "SQLite" });
  const opened = submitted("c1", { kind: "review_clear", review_id: "doc", text: "Downloaded" });
  const cleared = submitted("c2", { kind: "review_clear", review_id: "look" });
  const dismissed = submitted("c3", { kind: "question_clear", question_id: "herdr-strays-20260929" });
  const cases: [string, Parameters<typeof sendState>, ReturnType<typeof sendState>][] = [
    ["nothing sent", [{ submission: null, sending: false, error: "" }, []], undefined],
    ["just clicked, no receipt yet", [{ submission: answer, sending: true, error: "" }, []], { failed: false, heading: "Sending", cleared: false }],
    ["queued behind the goblin's turn", [{ submission: answer, sending: false, error: "", receipt: action("a1", "goblin_answer", "queued") }, []], { failed: false, heading: "Queued", cleared: false }],
    ["delivered", [{ submission: answer, sending: false, error: "" }, [action("a1", "goblin_answer", "succeeded")]], { failed: false, heading: "Sent", cleared: false }],
    ["the request refused", [{ submission: answer, sending: false, error: "that review is not open" }, []], { failed: true, heading: "Sent", cleared: false }],
    ["edited after a refusal", [{ submission: answer, sending: false, error: "" }, []], undefined],
    ["an ambiguous error, then delivered", [{ submission: answer, sending: false, error: "network error" }, [action("a1", "goblin_answer", "succeeded")]], { failed: false, heading: "Sent", cleared: false }],
    ["delivery failed", [{ submission: answer, sending: false, error: "" }, [action("a1", "goblin_answer", "failed")]], { failed: true, heading: "Sent", cleared: false }],
    ["delivery unconfirmed", [{ submission: answer, sending: false, error: "" }, [action("a1", "goblin_answer", "uncertain")]], { failed: true, heading: "Sent", cleared: false }],
    ["a document downloaded", [{ submission: opened, sending: true, error: "" }, []], { failed: false, heading: "Downloaded", cleared: true }],
    ["an item cleared", [{ submission: cleared, sending: true, error: "" }, []], { failed: false, heading: "Cleared", cleared: true }],
    ["a question dismissed", [{ submission: dismissed, sending: true, error: "" }, []], { failed: false, heading: "Dismissed", cleared: true }],
  ];
  for (const [name, args, want] of cases) assert.deepEqual(sendState(...args), want, name);
});

test("a CFO reply is queued until transport confirms it was submitted", () => {
  // Arrange
  const submission = { id: "answer", payload: JSON.stringify({ kind: "cfo_answer", text: "Reply received" }) };
  const draft = { submission, sending: false, error: "" };
  const queued = parseSnapshot({ healthy: true, actions: [{ id: "answer", kind: "cfo_answer", status: "queued", message: "The CFO is typing. Your answer will wait." }] }).actions;
  const submitting = parseSnapshot({ healthy: true, actions: [{ id: "answer", kind: "cfo_answer", status: "running" }] }).actions;
  const submitted = parseSnapshot({ healthy: true, actions: [{ id: "answer", kind: "cfo_answer", status: "running", awaiting: { host: "cfo", since: "2026-10-03T06:20:00Z" } }] }).actions;

  // Act and assert
  assert.equal(sendState(draft, queued)?.heading, "Queued");
  assert.equal(sendState(draft, submitting)?.heading, "Sending");
  assert.equal(sendState(draft, submitted)?.heading, "Sent");
});

test("every open item of his is one the board has announced, never a goblin's question, and nothing closed", () => {
  // Arrange
  const snapshot = parseSnapshot({ healthy: true,
    questions: [question("carried", "gb-a", "2026-09-26T10:00:00Z", "pending", { page: "plan" }), question("alone", "", "2026-09-26T10:00:00Z"), question("sent", "", "2026-09-26T10:00:00Z", "uncertain"), question("for-the-cfo", "gb-b", "2026-09-26T10:00:00Z")],
    reviews: [review("plan", "gb-a", "2026-09-26T10:00:00Z"), review("cleared", "gb-a", "2026-09-26T10:00:00Z", "cleared")],
    runs: [{ id: "install", identity: "cfo-1", title: "Install", state: "ready", created_at: "2026-09-26T10:00:00Z" }] });

  // Act
  const open = openKeys(snapshot);

  // Assert
  assert.deepEqual([...open].sort(), ["question:alone", "review:plan", "run:install"]);
  assert.deepEqual(waitingItems(snapshot).map((item) => item.key).sort(), ["question:alone", "review:plan", "run:install"], "the carried question shows as its page's card");
});

test("a choice or written text on an item still waiting stays unsent until its send finishes successfully; one on a closed item does not", () => {
  const blank = { selection: "", written: "", submission: null, sending: false, error: "" };
  const answer = submitted("a1", { kind: "goblin_answer", text: "SQLite" });
  const waiting = parseSnapshot({ healthy: true,
    questions: [question("q", "gb-a", "2026-09-26T10:00:00Z"), question("done", "gb-a", "2026-09-26T10:00:00Z", "superseded")],
    reviews: [review("r", "gb-a", "2026-09-26T10:00:00Z"), review("cleared", "gb-a", "2026-09-26T10:00:00Z", "cleared")] });
  const cases: [string, Parameters<typeof holdsUnsent>[0], Action[], boolean][] = [
    ["no drafts", {}, [], false],
    ["an empty draft", { "question:q": blank }, [], false],
    ["only spaces written", { "review:r": { ...blank, written: "  " } }, [], false],
    ["a choice not sent", { "question:q": { ...blank, selection: "option:A" } }, [], true],
    ["written text not sent", { "review:r": { ...blank, written: "Looks good" } }, [], true],
    ["one unsent draft among sent ones", { "question:q": { ...blank, selection: "option:A", submission: answer, sending: true }, "review:r": { ...blank, written: "Later" } }, [], true],
    ["a send in flight", { "question:q": { ...blank, selection: "option:A", submission: answer, sending: true } }, [], true],
    ["a send delivered", { "question:q": { ...blank, selection: "option:A", submission: answer } }, [action("a1", "goblin_answer", "succeeded")], false],
    ["a send refused", { "question:q": { ...blank, selection: "option:A", submission: answer, error: "that question is not open" } }, [], true],
    ["a delivery that failed", { "question:q": { ...blank, selection: "option:A", submission: answer } }, [action("a1", "goblin_answer", "failed")], true],
    ["a choice on an item that left the snapshot", { "question:gone": { ...blank, selection: "option:A" } }, [], false],
    ["a choice on a question that closed", { "question:done": { ...blank, selection: "option:A" } }, [], false],
    ["written text on a review item that was cleared", { "review:cleared": { ...blank, written: "Looks good" } }, [], false],
  ];
  for (const [name, drafts, actions, want] of cases) assert.equal(holdsUnsent(drafts, { ...waiting, actions }), want, name);
});

test("History's changed-answer drafts protect choices and text until sent or cleared", () => {
  const blank = { selection: "", written: "", submission: null, sending: false, error: "" };
  const change = submitted("change-1", { kind: "answer_change", question_id: "q", text: "Use PostgreSQL", answer_kind: "other" });
  const snapshot = parseSnapshot({ healthy: true, questions: [question("q", "billing", "2026-10-06T14:00:00Z", "succeeded", { answered_by: "cfo", answer: "SQLite" })] });
  const cases: [string, Parameters<typeof holdsUnsent>[0], Action[], boolean][] = [
    ["no change drafted", { "change:q": blank }, [], false],
    ["a changed option", { "change:q": { ...blank, selection: "option:B" } }, [], true],
    ["a written change", { "change:q": { ...blank, selection: "other", written: "Use PostgreSQL" } }, [], true],
    ["still sending", { "change:q": { ...blank, written: "Use PostgreSQL", submission: change, sending: true } }, [], true],
    ["accepted", { "change:q": { ...blank, written: "Use PostgreSQL", submission: change, receipt: action("change-1", "answer_change", "queued") } }, [], false],
    ["delivered", { "change:q": { ...blank, written: "Use PostgreSQL", submission: change } }, [action("change-1", "answer_change", "succeeded")], false],
    ["refused", { "change:q": { ...blank, written: "Use PostgreSQL", submission: change, error: "The board refused it" } }, [], true],
    ["delivery failed", { "change:q": { ...blank, written: "Use PostgreSQL", submission: change } }, [action("change-1", "answer_change", "failed")], true],
    ["delivery unconfirmed", { "change:q": { ...blank, written: "Use PostgreSQL", submission: change } }, [action("change-1", "answer_change", "uncertain")], true],
    ["cleared", { "change:q": blank }, [], false],
  ];
  for (const [name, drafts, actions, want] of cases) assert.equal(holdsUnsent(drafts, { ...snapshot, actions }), want, name);
});

test("a send the board refused leaves its item waiting only while that item is still open", () => {
  // Arrange
  const answer = submitted("a1", { kind: "goblin_answer", text: "SQLite" });
  const snapshot = parseSnapshot({ healthy: true, questions: [question("q", "gb-a", "2026-09-26T10:00:00Z"), question("closed", "gb-a", "2026-09-26T10:00:00Z", "superseded")] });
  const refused = { submission: answer, sending: false, error: "The board restarted; reload it." };
  const cases: [string, Parameters<typeof notSent>[0], string, Action[], boolean][] = [
    ["an accepted send", { submission: answer, sending: false, error: "", receipt: action("a1", "goblin_answer", "queued") }, "question:q", [], false],
    ["a send that failed in the browser yet reached the board", refused, "question:q", [action("a1", "goblin_answer", "running")], false],
    ["a refused send on an item still open", refused, "question:q", [], true],
    ["a refused send on an item since closed", refused, "question:closed", [], false],
    ["a send still on its way", { submission: answer, sending: true, error: "" }, "question:q", [], false],
    ["an unsent draft", { submission: null, sending: false, error: "" }, "question:q", [], false],
  ];

  for (const [name, draft, key, actions, want] of cases) {
    // Act
    const waits = notSent(draft, itemFor(snapshot, key), actions);

    // Assert
    assert.equal(waits, want, name);
  }
});

test("a question he answered elsewhere finishes its open card as answered, like a page answer", () => {
  // Arrange
  const [inChat, onBoard] = parseSnapshot({ healthy: true, questions: [
    question("herdr-strays-20260929", "", "2026-09-29T02:00:00Z", "succeeded", { answer: "Stop them", answer_kind: "option", answered_option: "Stop them", answered_by: "overlord", answered_in: "chat" }),
    question("pick-a-layout", "", "2026-09-29T02:00:00Z", "succeeded", { answer: "Tree", answer_kind: "option", answered_option: "Tree", answered_by: "overlord" }),
  ] }).questions ?? [];

  // Act
  const elsewhere = [inChat, onBoard].map((candidate) => answeredElsewhere({ kind: "question", key: "question:" + candidate.id, question: candidate }));

  // Assert
  assert.deepEqual(elsewhere, [true, false]);
});

test("a card whose item closed without it sending anything finishes as Answered or Closed, and stays for an answer that did not arrive", () => {
  // Arrange
  const at = "2026-10-02T11:00:00Z";
  const snapshot = parseSnapshot({ healthy: true,
    actions: [{ id: "lost", kind: "cfo_answer", status: "uncertain" }, { id: "sent", kind: "review_answer", status: "queued" }],
    questions: [
      question("open", "", at),
      question("on-another-board", "", at, "queued", { answer: "Merge it", answer_kind: "option", answer_id: "a1" }),
      question("by-the-cfo", "billing", at, "succeeded", { answer: "Merge it", answered_by: "cfo" }),
      question("dismissed", "", at, "cleared", { message: "You dismissed it: answered elsewhere or no longer needed." }),
      question("superseded", "billing", at, "superseded"),
      question("not-delivered", "", at, "failed", { answer: "Merge it", answer_id: "a2" }),
      question("not-confirmed", "", at, "uncertain", { answer: "Merge it", answer_id: "lost" }),
    ],
    reviews: [
      review("answered-on-another-board", "", at, "answered", { answer: "They read well", answer_id: "sent" }),
      review("waiting-billing-7", "billing", at, "withdrawn", { reason: "billing reported again" }),
      review("cleared", "", at, "cleared"),
    ],
    runs: [{ id: "install", identity: "c", title: "Install the build", state: "succeeded", created_at: at }],
  });
  const cases: [string, "" | "Answered" | "Closed"][] = [
    ["question:open", ""],
    ["question:on-another-board", "Answered"],
    ["question:by-the-cfo", "Answered"],
    ["question:dismissed", "Closed"],
    ["question:superseded", "Closed"],
    ["question:not-delivered", ""],
    ["question:not-confirmed", ""],
    ["review:answered-on-another-board", "Answered"],
    ["review:waiting-billing-7", "Closed"],
    ["review:cleared", "Closed"],
    ["run:install", ""],
  ];

  for (const [key, want] of cases) {
    // Act
    const finishes = closedElsewhere(itemFor(snapshot, key)!, snapshot.actions);

    // Assert
    assert.equal(finishes, want, key);
  }
});

test("a run item the CFO withdrew leaves the Command Center, and its history says who withdrew it and why", () => {
  // Arrange
  const snapshot = parseSnapshot({ healthy: true, runs: [
    { id: "install-main-66714dea", identity: "cfo-1", title: "Install main", shell: "powershell", command: "cfo install", state: "withdrawn", reason: "the candidate binary is gone", created_at: "2026-10-01T03:00:00Z", finished_at: "2026-10-01T05:00:00Z" },
  ] });

  // Act
  const waiting = waitingItems(snapshot);
  const settled = settledItems(snapshot);

  // Assert
  assert.deepEqual(waiting, []);
  assert.deepEqual(settled.map((item) => settledLabel(item, [])), ["Withdrawn by the CFO: the candidate binary is gone"]);
});

test("a wait's one-line row reads the goblin's words alone, and any other review item keeps its title", () => {
  // Arrange
  const page = "http://127.0.0.1:4387/session/f26e";
  const table = "| Type | Name |\n| --- | --- |\n| CNAME | `mcp` |";
  const wait = (title: string, lavish = "") => review("waiting-billing-7", "billing", "2026-09-27T10:00:00Z", "open", { title, lavish }) as unknown as Review;

  // Act
  const endsWithTable = reviewLine(wait("Waiting on you: Add the **records**\n" + table + " (page " + page + ")", page));
  const opensWithTable = reviewLine(wait("Waiting on you: " + table + "\nAdd these records (page " + page + ")", page));
  const other = reviewLine(review("plan-billing", "billing", "2026-09-27T10:00:00Z", "open", { title: "Look at the **plan**" }) as unknown as Review);

  // Assert
  assert.equal(endsWithTable, "Add the records");
  assert.equal(opensWithTable, "Add these records");
  assert.equal(other, "Look at the plan");
});

test("a page he sent a revision on waits on its goblin, so it leaves Waiting on you without settling", () => {
  // Arrange
  const snapshot = parseSnapshot({ healthy: true, reviews: [
    review("waiting-billing-7", "billing", "2026-10-01T05:00:00Z", "open", { lavish: "http://127.0.0.1:4387/session/f26e", lavish_page: "C:\\work\\plan.html", revising_since: "2026-10-01T05:10:00Z" }),
    review("waiting-notes-3", "notes", "2026-10-01T05:01:00Z", "open", { lavish: "http://127.0.0.1:4387/session/a1b2", lavish_page: "C:\\work\\notes.html" }),
  ] });

  // Act
  const waiting = waitingItems(snapshot).map((item) => item.key);
  const settled = settledItems(snapshot).map((item) => item.key);
  const onScreen = waitingItems(snapshot, new Set(["review:waiting-billing-7"]));

  // Assert
  assert.deepEqual(waiting, ["review:waiting-notes-3"]);
  assert.deepEqual(settled, []);
  assert.deepEqual(onScreen.map((item) => item.key), ["review:waiting-billing-7", "review:waiting-notes-3"], "the card on screen when he revised stays, saying so");
  assert.equal(nextOpenKey(onScreen, "review:waiting-notes-3"), null, "moving on never lands on a page that waits on its goblin");
});
