import test from "node:test";
import assert from "node:assert/strict";
import { answeredBy, answeredLabel, chosenOption, outcomeIcon, questionOutcome, settledQuestions, waitingQuestions } from "./commandQueue.ts";
import { parseSnapshot } from "./types.ts";

const question = (id: string, task: string, created_at: string, status = "pending", extra: Record<string, unknown> = {}) => ({ id, identity: "i-" + id, task, created_at, status, options: ["A", "B"], ...extra });

test("the CFO's questions come first, then goblins by longest wait", () => {
  const snapshot = parseSnapshot({ healthy: true, questions: [
    question("g-new", "billing", "2026-09-24T00:30:00Z"), question("cfo-late", "", "2026-09-24T00:40:00Z"),
    question("g-old", "steward", "2026-09-24T00:10:00Z"), question("cfo-early", "", "2026-09-24T00:20:00Z"),
    question("done", "billing", "2026-09-24T00:00:00Z", "queued"), question("g-undated", "notes", ""),
  ] });
  assert.deepEqual(waitingQuestions(snapshot).map((q) => q.id), ["cfo-early", "cfo-late", "g-old", "g-new", "g-undated"]);
  assert.deepEqual(waitingQuestions(snapshot, new Set(["done"])).map((q) => q.id), ["cfo-early", "cfo-late", "done", "g-old", "g-new", "g-undated"], "a card answered in this sitting keeps its place");
  assert.deepEqual(waitingQuestions(parseSnapshot({ healthy: true })), []);
});

test("answered questions are listed newest first with what was chosen", () => {
  const snapshot = parseSnapshot({ healthy: true, questions: [
    question("a", "billing", "2026-09-24T00:10:00Z", "succeeded", { answer_id: "x", answer: "A", answer_kind: "option" }),
    question("b", "", "2026-09-24T00:20:00Z", "queued", { answer_id: "y", answer: "Ship it Friday", answer_kind: "other" }),
    question("c", "steward", "2026-09-24T00:30:00Z", "superseded"),
    question("d", "notes", "2026-09-24T00:40:00Z"),
  ] });
  assert.deepEqual(settledQuestions(snapshot).map((q) => q.id), ["c", "b", "a"]);
  const [c, b, a] = settledQuestions(snapshot);
  assert.equal(answeredLabel(a), "You chose A");
  assert.equal(answeredLabel(b), "You wrote: Ship it Friday");
  assert.equal(answeredLabel(c), "Superseded; the asker was replaced");
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
    assert.equal(answeredLabel(candidate), label, candidate.id);
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
    ["a superseded question", { status: "superseded" }, "superseded", "Superseded; the asker was replaced", "close"],
    ["a pending question", { status: "pending" }, "pending", "Waiting on you", "close"],
  ];
  for (const [name, fields, outcome, label, icon] of cases) {
    const [candidate] = parseSnapshot({ healthy: true, questions: [question("q", "billing", "2026-09-24T00:10:00Z", "pending", fields)] }).questions ?? [];
    assert.equal(questionOutcome(candidate), outcome, name);
    assert.equal(answeredLabel(candidate), label, name);
    assert.equal(outcomeIcon(questionOutcome(candidate)), icon, name);
  }
});
