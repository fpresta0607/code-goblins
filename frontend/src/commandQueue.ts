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

export function answeredLabel(question: Question): string {
  if (question.status === "superseded") return "Superseded; the asker was replaced";
  if (!question.answer_id) return "Closed";
  const who = question.answered_by === "cfo" ? "The CFO" : "You";
  return question.answer_kind === "other" ? who + " wrote: " + question.answer : who + " chose " + question.answer;
}

// The choice that closed a question: the backend's answered_option, or the
// recorded answer for a question closed before that field existed.
export function chosenOption(question: Question): string {
  return question.answered_option || (question.answer_kind === "other" ? "" : question.answer);
}

export function answeredBy(question: Question): string {
  return "Answered by " + (question.answered_by === "cfo" ? "the CFO" : "you");
}
