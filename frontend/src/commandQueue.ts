import type { IconName } from "./Icon.tsx";
import type { Question, Snapshot } from "./types.ts";

const time = (question: Question) => Date.parse(question.created_at) || Number.MAX_SAFE_INTEGER;

// The stack the Overlord works through: the CFO's own questions first, then
// goblins by longest wait. A card answered in this sitting keeps its place, so
// it can show its outcome instead of vanishing under the pointer.
export function waitingQuestions(snapshot: Snapshot, kept: ReadonlySet<string> = new Set()): Question[] {
  return (snapshot.questions || [])
    .filter((question) => question.status === "pending" || kept.has(question.id))
    .sort((a, b) => Number(!!a.task) - Number(!!b.task) || time(a) - time(b));
}

export function settledQuestions(snapshot: Snapshot): Question[] {
  return (snapshot.questions || []).filter((question) => question.status !== "pending").sort((a, b) => time(b) - time(a));
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
    case "superseded": return "Superseded; the asker was replaced";
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
export function chosenOption(question: Question): string {
  return question.answered_option || (question.answer_kind === "other" ? "" : question.answer);
}

export function answeredBy(question: Question): string {
  return "Answered by " + (question.answered_by === "cfo" ? "the CFO" : "you");
}
