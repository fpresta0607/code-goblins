import type { IconName } from "./Icon.tsx";
import { object, string, type Action, type BoardActivity, type Question, type Review, type ReviewDocument, type Run, type Snapshot } from "./types.ts";
import { deliveryMark, runMark, type Submission } from "./feedback.ts";

// Everything the Overlord is asked lives in one queue: a goblin's or the CFO's
// question, a review item (images, a Lavish page, or a wait on him), or a
// command the CFO needs him to run.
export type Item = { kind: "question"; key: string; question: Question } | { kind: "review"; key: string; review: Review } | { kind: "run"; key: string; run: Run };

const asItems = (snapshot: Snapshot): Item[] => [
  ...(snapshot.questions || []).map((question): Item => ({ kind: "question", key: "question:" + question.id, question })),
  ...(snapshot.reviews || []).map((review): Item => ({ kind: "review", key: "review:" + review.id, review })),
  ...(snapshot.runs || []).map((run): Item => ({ kind: "run", key: "run:" + run.id, run })),
];
const task = (item: Item) => item.kind === "question" ? item.question.task : item.kind === "review" ? item.review.task : "";
const created = (item: Item) => Date.parse(item.kind === "question" ? item.question.created_at : item.kind === "review" ? item.review.created_at : item.run.created_at) || Number.MAX_SAFE_INTEGER;
const closed = (item: Item) => Date.parse(item.kind === "question" ? item.question.answered_at || item.question.created_at
  : item.kind === "review" ? item.review.updated_at : item.run.finished_at || item.run.ran_at || item.run.created_at) || 0;
// A run stays in the stack while it runs, so its card shows the result.
export const isOpen = (item: Item) => item.kind === "question" ? item.question.status === "pending"
  : item.kind === "review" ? item.review.state === "open" : item.run.state === "ready" || item.run.state === "running";

export function itemFor(snapshot: Snapshot, key: string): Item | undefined {
  return asItems(snapshot).find((item) => item.key === key);
}

// The stack he works through: the CFO's own items first, then goblins by
// longest wait. An item answered in this sitting keeps its place, so it can
// show its outcome instead of vanishing under the pointer.
export function waitingItems(snapshot: Snapshot, kept: ReadonlySet<string> = new Set()): Item[] {
  return asItems(snapshot)
    .filter((item) => isOpen(item) || kept.has(item.key))
    .sort((a, b) => Number(!!task(a)) - Number(!!task(b)) || created(a) - created(b));
}

// The item to show after the one at key: the next open item, wrapping to the
// first, passing over what was sent in this sitting before the snapshot says
// so. Null means nothing else waits on him.
export function nextOpenKey(stack: Item[], key: string, sent: ReadonlySet<string> = new Set()): string | null {
  const index = stack.findIndex((item) => item.key === key);
  const waiting = (item: Item) => item.key !== key && isOpen(item) && !sent.has(item.key);
  return (stack.slice(index + 1).find(waiting) || stack.slice(0, Math.max(0, index)).find(waiting))?.key || null;
}

// SentDraft is what a card keeps of what the Overlord sent from it.
export interface SentDraft { submission: Submission | null; sending: boolean; error: string; receipt?: Action }

// SendState is what an item's card shows after Send. A send is done the moment
// it is made: its check shows at once and delivery goes on quietly. It is
// confirmed once delivered, and failed only when the request was refused or
// delivery failed or went unconfirmed, the one case the card shows again. Once
// its action is known, the action alone decides; a draft edited after a
// refusal has sent nothing.
export interface SendState { failed: boolean; confirmed: boolean; heading: string; cleared: boolean }

export function sendState(draft: SentDraft, actions: Action[]): SendState | undefined {
  const { submission } = draft;
  if (!submission) return undefined;
  const outcome = actions.find((action) => action.id === submission.id) || draft.receipt;
  if (!outcome && !draft.sending && !draft.error) return undefined;
  const sent = object(JSON.parse(submission.payload));
  const cleared = sent.kind === "review_clear";
  return {
    failed: outcome ? deliveryMark(outcome).trouble : !!draft.error,
    confirmed: outcome?.status === "succeeded",
    heading: cleared ? string(sent.text) || "Cleared" : "Sent",
    cleared,
  };
}

// failedSends is the items sent and moved past whose send then failed, which
// come back into view with what went wrong.
export function failedSends(sent: ReadonlySet<string>, drafts: Record<string, SentDraft>, actions: Action[]): string[] {
  return [...sent].filter((key) => !!drafts[key] && !!sendState(drafts[key], actions)?.failed);
}

// The page a question's card may open: its asker's most recent live review
// page, from the same goblin session or the CFO registration that asked, so a
// card never opens another task's page or one a replaced asker left behind.
export function questionPage(presentations: BoardActivity[], question: Question): BoardActivity | undefined {
  return [...presentations].reverse().find((event) => event.kind === "review" && (question.task
    ? event.task_id === question.task && event.generation === question.generation
    : !!event.cfo_identity && event.cfo_identity === question.identity));
}

// A document's facts on one line: its type from the file name, its size and
// who sent it, such as "PDF · 2.4 MB · from the CFO".
export function documentFacts(document: ReviewDocument, sender: string): string {
  const dot = document.name.lastIndexOf(".");
  const extension = dot > 0 ? document.name.slice(dot + 1).toUpperCase() : "";
  const size = document.size >= 1 << 20 ? (document.size / (1 << 20)).toFixed(1) + " MB" : document.size >= 1 << 10 ? Math.round(document.size / (1 << 10)) + " KB" : document.size + " B";
  return [extension || "File", size, "from " + sender].join(" · ");
}

export function settledItems(snapshot: Snapshot): Item[] {
  return asItems(snapshot).filter((item) => !isOpen(item)).sort((a, b) => closed(b) - closed(a));
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

// What became of an item, in plain words and as its mark: a question by its
// outcome, a review item by its state. The backend marks a review answered as
// soon as it queues the answer and sets delivered only once the goblin has it,
// so only delivered says it arrived. An answer action that succeeded without
// delivery was handed to the CFO because the goblin was replaced, and an
// answer whose action has aged out of the snapshot is no longer recorded.
function answerOutcome(review: Review, actions: Action[]): "delivered" | "handed" | "pending" | "failed" | "uncertain" | "unrecorded" {
  if (review.delivered) return "delivered";
  const status = actions.find((action) => action.id === review.answer_id)?.status;
  if (!status) return "unrecorded";
  if (status === "succeeded") return "handed";
  return status === "failed" || status === "uncertain" ? status : "pending";
}

export function settledLabel(item: Item, actions: Action[]): string {
  if (item.kind === "question") return answeredLabel(item.question);
  if (item.kind === "run") return runMark(item.run).label + (item.run.reason ? ": " + item.run.reason : "");
  const { state, answer, reason, task } = item.review;
  const asker = task ? "the goblin" : "the CFO";
  if (state === "withdrawn") return "Withdrawn: " + reason;
  if (state !== "answered") return reason || "Cleared";
  switch (answerOutcome(item.review, actions)) {
    case "failed": return "Your answer did not reach " + asker;
    case "uncertain": return "Delivery unconfirmed: inspect " + asker + "'s pane before answering again";
    case "pending": return "You wrote: " + answer + " (not yet delivered to " + asker + ")";
    case "handed": return "Sent to the CFO: " + answer;
    case "unrecorded": return "You wrote: " + answer + " (delivery no longer recorded)";
    case "delivered": return "You wrote: " + answer;
  }
}

export function settledIcon(item: Item, actions: Action[]): { icon: IconName; tone: string } {
  if (item.kind === "question") {
    const outcome = questionOutcome(item.question);
    return { icon: outcomeIcon(outcome), tone: outcome === "answered" ? "succeeded" : outcome };
  }
  if (item.kind === "run") return { icon: runMark(item.run).icon, tone: item.run.state };
  if (item.review.state !== "answered") return { icon: "close", tone: item.review.state };
  const outcome = answerOutcome(item.review, actions);
  if (outcome === "failed" || outcome === "uncertain") return { icon: "warning", tone: outcome };
  if (outcome === "delivered") return { icon: "check-double", tone: "succeeded" };
  return outcome === "handed" ? { icon: "check", tone: "succeeded" } : { icon: "check", tone: "queued" };
}

// The choice that closed a question: the backend's answered_option, or the
// recorded answer for a question closed before that field existed.
export function chosenOption(question: Question): string {
  return question.answered_option || (question.answer_kind === "other" ? "" : question.answer);
}

export function answeredBy(question: Question): string {
  return "Answered by " + (question.answered_by === "cfo" ? "the CFO" : "you");
}
