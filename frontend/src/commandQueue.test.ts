import test from "node:test";
import assert from "node:assert/strict";
import { answeredBy, answeredLabel, chosenOption, itemFor, outcomeIcon, questionOutcome, settledIcon, settledItems, settledLabel, waitingItems } from "./commandQueue.ts";
import { parseSnapshot } from "./types.ts";

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
      question("d", "notes", "2026-09-24T00:40:00Z"),
    ],
    reviews: [
      review("r1", "steward", "2026-09-24T00:15:00Z", "answered", { answer: "Go with B", answer_id: "z", delivered: true, updated_at: "2026-09-24T00:50:00Z" }),
      review("r2", "steward", "2026-09-24T00:16:00Z", "withdrawn", { reason: "the goblin found the answer", updated_at: "2026-09-24T00:25:00Z" }),
      review("r3", "", "2026-09-24T00:17:00Z", "cleared", { updated_at: "2026-09-24T00:18:00Z" }),
      review("r4", "steward", "2026-09-24T00:18:00Z"),
    ],
  });
  assert.deepEqual(settledItems(snapshot).map((item) => item.key), ["review:r1", "question:c", "review:r2", "question:b", "review:r3", "question:a"]);
  const label = (key: string) => settledLabel(itemFor(snapshot, key)!);
  const cases: [string, string][] = [["question:a", "You chose A"], ["question:b", "You wrote: Ship it Friday"], ["question:c", "Superseded; the asker was replaced"],
    ["review:r1", "You wrote: Go with B"], ["review:r2", "Withdrawn: the goblin found the answer"], ["review:r3", "Cleared"]];
  for (const [key, text] of cases) assert.equal(label(key), text, key);
  assert.equal(parseSnapshot({ healthy: true }).reviews?.length, 0);
  assert.deepEqual(settledIcon(itemFor(snapshot, "review:r1")!), { icon: "check-double", tone: "succeeded" });
  assert.deepEqual(settledIcon(itemFor(snapshot, "question:c")!), { icon: "close", tone: "superseded" });
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
    assert.equal(settledLabel({ kind: "question", key: "question:" + candidate.id, question: candidate }), label, candidate.id);
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
