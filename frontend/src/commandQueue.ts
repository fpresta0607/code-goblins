import type { IconName } from "./Icon.tsx";
import type { Question, Review, Snapshot } from "./types.ts";

// Everything the Overlord is asked lives in one queue: a goblin's or the CFO's
// question, or a review item (images, a Lavish page, or a wait on him).
export type Item = { kind: "question"; key: string; question: Question } | { kind: "review"; key: string; review: Review };

const asItems = (snapshot: Snapshot): Item[] => [
  ...(snapshot.questions || []).map((question): Item => ({ kind: "question", key: "question:" + question.id, question })),
  ...(snapshot.reviews || []).map((review): Item => ({ kind: "review", key: "review:" + review.id, review })),
];
const task = (item: Item) => item.kind === "question" ? item.question.task : item.review.task;
const created = (item: Item) => Date.parse(item.kind === "question" ? item.question.created_at : item.review.created_at) || Number.MAX_SAFE_INTEGER;
const closed = (item: Item) => Date.parse(item.kind === "question" ? item.question.created_at : item.review.updated_at) || 0;
const open = (item: Item) => item.kind === "question" ? item.question.status === "pending" : item.review.state === "open";

export function itemFor(snapshot: Snapshot, key: string): Item | undefined {
  return asItems(snapshot).find((item) => item.key === key);
}

// The stack he works through: the CFO's own items first, then goblins by
// longest wait. An item answered in this sitting keeps its place, so it can
// show its outcome instead of vanishing under the pointer.
export function waitingItems(snapshot: Snapshot, kept: ReadonlySet<string> = new Set()): Item[] {
  return asItems(snapshot)
    .filter((item) => open(item) || kept.has(item.key))
    .sort((a, b) => Number(!!task(a)) - Number(!!task(b)) || created(a) - created(b));
}

export function settledItems(snapshot: Snapshot): Item[] {
  return asItems(snapshot).filter((item) => !open(item)).sort((a, b) => closed(b) - closed(a));
}

export type QuestionOutcome = "pending" | "answered" | "superseded" | "cleared" | "failed" | "uncertain";

// What became of a question. The CFO's cfo answer sets answered_by with an
// empty answer_id; a board answer that failed or went unconfirmed never
// reached its asker, so it is not answered.
export function questionOutcome(question: Question): QuestionOutcome {
  const { status } = question;
  if (status === "pending" || status === "superseded" || status === "cleared" || status === "failed") return status;
  return (status === "queued" || status === "running" || status === "succeeded") && (question.answer_id || question.answered_by) ? "answered" : "uncertain";
}

export function answeredLabel(question: Question): string {
  switch (questionOutcome(question)) {
    case "pending": return "Waiting on you";
    // Superseded covers a replaced asker and a question the CFO retired with
    // cfo send --ack-blocking; the backend's message tells them apart.
    case "superseded": return question.message || "Superseded; the asker was replaced";
    case "cleared": return "Closed without an answer";
    case "failed": return "Your answer did not reach " + (question.task ? "the goblin" : "the CFO");
    case "uncertain": return "Delivery unconfirmed";
  }
  const who = question.answered_by === "cfo" ? "The CFO" : "You";
  return question.answer_kind === "other" ? who + " wrote: " + question.answer : who + " chose " + question.answer;
}

export function outcomeIcon(outcome: QuestionOutcome): IconName {
  if (outcome === "answered") return "check-double";
  return outcome === "failed" || outcome === "uncertain" ? "warning" : "close";
}

// The choice that closed a question: the backend's answered_option, or the
// recorded answer for a question closed before that field existed.

// What became of an item, in plain words and as its mark: a question by its
// outcome, a review item by its state.
export function settledLabel(item: Item): string {
  if (item.kind === "question") return answeredLabel(item.question);
  const { state, answer, reason } = item.review;
  return state === "answered" ? "You wrote: " + answer : state === "withdrawn" ? "Withdrawn: " + reason : "Cleared";
}

export function settledIcon(item: Item): { icon: IconName; tone: string } {
  if (item.kind === "question") {
    const outcome = questionOutcome(item.question);
    return { icon: outcomeIcon(outcome), tone: outcome === "answered" ? "succeeded" : outcome };
  }
  return item.review.state === "answered" ? { icon: "check-double", tone: "succeeded" } : { icon: "close", tone: item.review.state };
}

export function chosenOption(question: Question): string {
  return question.answered_option || (question.answer_kind === "other" ? "" : question.answer);
}

export function answeredBy(question: Question): string {
  return "Answered by " + (question.answered_by === "cfo" ? "the CFO" : "you");
}
