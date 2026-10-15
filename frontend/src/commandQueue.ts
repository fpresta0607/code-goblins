import type { IconName } from "./Icon.tsx";
import { object, string, type Action, type BoardActivity, type BrowserSignIn, type CredentialRequest, type Question, type Review, type Run, type Snapshot } from "./types.ts";
import { deliveryMark, runMark, type Submission } from "./feedback.ts";
import { credentialSettled } from "./credentials.ts";
import { signInOutcome } from "./browser-signins.ts";
import { plainMessage } from "./messageText.ts";
import { updateOutcome } from "./update-progress.ts";

// Everything the board holds as an item lives in one queue: a goblin's or the
// CFO's question, a review item (images, a Lavish page, or a wait on him), a
// command the CFO needs him to run, or a request for credential values.
export type Item = { kind: "question"; key: string; question: Question } | { kind: "review"; key: string; review: Review } | { kind: "run"; key: string; run: Run }
  | { kind: "credential"; key: string; request: CredentialRequest }
  | { kind: "browser-signin"; key: string; signin: BrowserSignIn };

// CFO_QUIET_AFTER_MS is how long the CFO has a goblin's question to itself:
// past it the CFO's bar says the question waits, and it is his too.
export const CFO_QUIET_AFTER_MS = 10 * 60 * 1000;

// leftByTheCFO says whether a goblin's question is still unanswered
// CFO_QUIET_AFTER_MS after it was asked, at the snapshot's time at.
export const leftByTheCFO = (question: Question, at: number) => !!question.task && question.status === "pending" && at - Date.parse(question.created_at) >= CFO_QUIET_AFTER_MS;

// forOverlord says whether an item is his: a question the CFO asks him, what
// the CFO passes up, a goblin's wait or page addressed to him, a command and
// a credential request. A goblin's question is the CFO's to answer: it does
// not wait on him or open anything, and is in History once answered, until
// the CFO has left it for ten minutes, when it waits on him too, as the
// CFO's bar then says (the Overlord, 2026-10-13: "the command center is not
// asking me every question").
export const forOverlord = (item: Item, at: number) => item.kind !== "question" || !item.question.task || leftByTheCFO(item.question, at);

// A terminal a credential card opened shows on that card, not as a run of its own.
export const asItems = (snapshot: Snapshot): Item[] => [
  ...(snapshot.questions || []).map((question): Item => ({ kind: "question", key: "question:" + question.id, question })),
  ...(snapshot.reviews || []).map((review): Item => ({ kind: "review", key: "review:" + review.id, review })),
  ...(snapshot.runs || []).filter((run) => !run.credential_request).map((run): Item => ({ kind: "run", key: "run:" + run.id, run })),
  ...(snapshot.credentials || []).map((request): Item => ({ kind: "credential", key: "credential:" + request.id, request })),
  ...(snapshot.browser_signins || []).map((signin): Item => ({ kind: "browser-signin", key: "browser-signin:" + signin.id, signin })),
];
const task = (item: Item) => item.kind === "browser-signin" ? item.signin.task : item.kind === "credential" ? item.request.task : item.kind === "question" ? item.question.task : item.kind === "review" ? item.review.task : item.run.task;
const created = (item: Item) => Date.parse(item.kind === "browser-signin" ? item.signin.created_at : item.kind === "credential" ? item.request.created_at : item.kind === "question" ? item.question.created_at : item.kind === "review" ? item.review.created_at : item.run.created_at) || Number.MAX_SAFE_INTEGER;
// When an item closed, as its History row says.
export const closedAt = (item: Item) => item.kind === "browser-signin" ? item.signin.closed_at || item.signin.created_at : item.kind === "credential" ? item.request.closed_at || item.request.created_at : item.kind === "question" ? item.question.answered_at || item.question.created_at
  : item.kind === "review" ? item.review.updated_at : item.run.finished_at || item.run.ran_at || item.run.created_at;
const closed = (item: Item) => Date.parse(closedAt(item)) || 0;
// A run stays in the stack while it runs, so its card shows the result.
export const isOpen = (item: Item) => item.kind === "browser-signin" ? ["ready", "opening", "open"].includes(item.signin.state) : item.kind === "credential" ? item.request.state === "open" : item.kind === "question" ? item.question.status === "pending"
  : item.kind === "review" ? item.review.state === "open" : item.run.state === "ready" || item.run.state === "running";

// An open item waits on him, except a page he sent a revision on: that waits
// on its goblin's next version, which replaces the page in the same item.
const waitsOnHim = (item: Item) => isOpen(item) && !(item.kind === "review" && item.review.revising_since);

// An item the Overlord answered outside the Command Center, such as on its own
// page or in chat as the CFO recorded: its card finishes as one he answered
// from it does.
export const answeredElsewhere = (item: Item) => item.kind === "review" ? item.review.state === "answered" && !!item.review.answered_in
  : item.kind === "question" && item.question.status === "succeeded" && !!item.question.answered_in;

// How the card of an item that closed without that card sending anything
// finishes: Answered when he answered it somewhere else, such as on another
// board, Closed when its asker or the CFO took it back. Empty while the item
// is open, and for one whose answer did not arrive, whose card says what to
// do. A run's card shows its result and a credential request's card what it
// saved, so neither finishes this way.
export function closedElsewhere(item: Item, actions: Action[]): "" | "Answered" | "Closed" {
  if (item.kind === "run" || item.kind === "credential" || item.kind === "browser-signin" || isOpen(item) || settledIcon(item, actions).icon === "warning") return "";
  return (item.kind === "question" ? questionOutcome(item.question) === "answered" : item.review.state === "answered") ? "Answered" : "Closed";
}

// A goblin's wait on the Overlord himself, raised by notify --waiting-on
// overlord: a status to see and dismiss, not a question to answer. It closes
// by itself once the goblin reports again or the CFO answers it.
export const waitsOnOverlord = (review: Review) => !!review.task && review.id.startsWith("waiting-" + review.task + "-");

// The goblin's reason for a wait as it wrote it: the queue prefixes it with
// "Waiting on you: " and a page's link, which the card opens instead.
export const waitReason = (review: Review) => review.title.replace(/^Waiting on you:\s*/, "").replace(review.lavish ? " (page " + review.lavish + ")" : "", "");

// A review item on one line, for a list row or a button's label: a wait as
// the goblin's words alone, anything else as its title.
export const reviewLine = (review: Review) => plainMessage(waitsOnOverlord(review) ? waitReason(review) : review.title);

// What a wait points at, so its card says it plainly and opens it: the page
// the goblin named, else a file it delivered, else the web link it gave as the
// link (cfo notify --link), never an address it only names in its words, and
// never its question to the CFO. Null when it names nothing.
export type WaitTarget = { kind: "item"; key: string; label: string } | { kind: "page"; url: string; label: string };
export function waitTarget(review: Review, snapshot: Snapshot): WaitTarget | null {
  if (review.lavish) return { kind: "page", url: review.lavish, label: "Open review" };
  const file = (snapshot.reviews || []).find((candidate) => candidate.task === review.task && candidate.state === "open" && candidate.document);
  if (file?.document) return { kind: "item", key: "review:" + file.id, label: "Open the file" };
  const link = URL.canParse(review.link) ? new URL(review.link) : null;
  if (link && (link.protocol === "https:" || link.protocol === "http:")) return { kind: "page", url: review.link, label: "Open the link" };
  return null;
}

export function itemFor(snapshot: Snapshot, key: string): Item | undefined {
  return asItems(snapshot).find((item) => item.key === key);
}

// The stack he works through: the CFO's own items first, then goblins in the
// In progress order he set, then goblins he has not placed, each by longest
// wait. An item answered in this sitting keeps its place, so it can show its
// outcome instead of vanishing under the pointer.
export function waitingItems(snapshot: Snapshot, kept: ReadonlySet<string> = new Set()): Item[] {
  const place = (item: Item) => { const at = snapshot.attention.indexOf(task(item)); return at < 0 ? snapshot.attention.length : at; };
  return asItems(snapshot)
    .filter((item) => forOverlord(item, Date.parse(snapshot.at)) && waitsOnHim(item) || kept.has(item.key))
    .sort((a, b) => Number(!!task(a)) - Number(!!task(b)) || place(a) - place(b) || created(a) - created(b));
}

// Every item of his still open: what the board has already announced.
export function openKeys(snapshot: Snapshot): Set<string> {
  return new Set(asItems(snapshot).filter((item) => forOverlord(item, Date.parse(snapshot.at)) && isOpen(item)).map((item) => item.key));
}

// The newest item a goblin has waiting on the Overlord, if any.
export function newestItemOf(snapshot: Snapshot, id: string): Item | undefined {
  return asItems(snapshot).filter((item) => forOverlord(item, Date.parse(snapshot.at)) && isOpen(item) && task(item) === id).sort((a, b) => created(b) - created(a))[0];
}

// The item to show after the one at key: the next open item, wrapping to the
// first, passing over what was sent in this sitting before the snapshot says
// so. Null means nothing else waits on him.
export function nextOpenKey(stack: Item[], key: string, sent: ReadonlySet<string> = new Set()): string | null {
  const index = stack.findIndex((item) => item.key === key);
  const waiting = (item: Item) => item.key !== key && waitsOnHim(item) && !sent.has(item.key);
  return (stack.slice(index + 1).find(waiting) || stack.slice(0, Math.max(0, index)).find(waiting))?.key || null;
}

// SentDraft is what a card keeps of what the Overlord sent from it.
export interface SentDraft { submission: Submission | null; sending: boolean; error: string; receipt?: Action }

// SendState is what an item's card shows after Send. A send is done the moment
// it is made: its check shows at once and delivery goes on quietly. It reads
// Sending until the supervisor has it, Queued while it waits for its terminal
// and Sent after, and failed only when the request was refused or delivery
// failed or went unconfirmed, which a card still on screen shows.
// Once its action is known, the action alone decides; a draft edited after a
// refusal has sent nothing.
export interface SendState { failed: boolean; heading: string; cleared: boolean }

export function sendState(draft: SentDraft, actions: Action[]): SendState | undefined {
  const { submission } = draft;
  if (!submission) return undefined;
  const outcome = actions.find((action) => action.id === submission.id) || draft.receipt;
  if (!outcome && !draft.sending && !draft.error) return undefined;
  const sent = object(JSON.parse(submission.payload));
  const cleared = sent.kind === "review_clear" || sent.kind === "question_clear";
  return {
    failed: outcome ? deliveryMark(outcome).trouble : !!draft.error,
    heading: cleared ? sent.kind === "question_clear" ? "Dismissed" : "Cleared"
      : outcome?.status === "queued" ? "Queued"
      : draft.sending && !outcome || outcome?.status === "running" && !outcome.awaiting ? "Sending" : "Sent",
    cleared,
  };
}

// notSent says whether a send left nothing behind: the board refused it, or it
// failed before the board took it, so no action exists and its item, still
// open, waits on him as before.
export function notSent(draft: SentDraft, item: Item | undefined, actions: Action[]): boolean {
  const { submission } = draft;
  const accepted = !!draft.receipt || actions.some((action) => action.id === submission?.id);
  return !!submission && !draft.sending && !!draft.error && !accepted && !!item && isOpen(item);
}

// holdsUnsent says whether any card of an item still waiting, or a change to
// the CFO's answer made from History, keeps a choice or written text the
// Overlord has not sent, is sending it, or saw its send fail, which a reload
// would lose. A draft left on an item that closed is no longer his to send;
// one he is sending counts whatever the item shows, since the board shows it
// answered the moment he sends it.
export function holdsUnsent(drafts: Record<string, SentDraft & { selection: string; written: string }>, snapshot: Snapshot): boolean {
  return Object.entries(drafts).some(([key, draft]) => {
    const item = itemFor(snapshot, key);
    const isLive = draft.sending || key.startsWith("change:") || !!item && isOpen(item);
    const state = sendState(draft, snapshot.actions);
    return isLive && (!!draft.selection || !!draft.written.trim()) && (draft.sending || !state || state.failed);
  });
}


// The page a question's card may open: its asker's most recent live review
// page, from the same goblin session or the CFO registration that asked, so a
// card never opens another task's page or one a replaced asker left behind.
export function questionPage(presentations: BoardActivity[], question: Question): BoardActivity | undefined {
  return [...presentations].reverse().find((event) => event.kind === "review" && (question.task
    ? event.task_id === question.task && event.generation === question.generation
    : !!event.cfo_identity && event.cfo_identity === question.identity));
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
    // Superseded is a replaced asker; the backend's message says which.
    case "superseded": return question.message || "Superseded. The asker was replaced.";
    case "cleared": return question.message || "Closed without an answer";
    case "failed": return "Your answer did not reach " + (question.task ? "the goblin" : "the CFO");
    case "uncertain": return "Not confirmed: check " + (question.task ? "the goblin's" : "the CFO's") + " terminal";
  }
  const who = question.answered_by === "cfo" ? "The CFO" : "You";
  if (question.answered_in === "page") return who + " answered on its page" + (question.answer ? ": " + question.answer : "");
  if (question.answered_in) return "You answered in " + question.answered_in + " · recorded by the CFO";
  // A question the CFO answered and retired with --ack-blocking closed
  // without the board learning which choice.
  if (!question.answer) return who + " answered it";
  // A board answer still on its way says so until its reader has it.
  const onItsWay = question.status === "succeeded" ? "" : " (not yet delivered to " + (question.task ? "the goblin" : "the CFO") + ")";
  return (question.answer_kind === "other" ? who + " wrote: " + question.answer : who + " chose " + (cfoReason(question) ? question.answered_option : question.answer)) + onItsWay;
}

// The reason the CFO gave with its choice: cfo answer --note joins it to the
// choice as "<choice>. <reason>".
const cfoReason = (question: Question) => question.answered_by === "cfo" && question.answered_option && question.answer.startsWith(question.answered_option + ". ")
  ? question.answer.slice(question.answered_option.length + 2) : "";

// The line under an answer in History: the CFO's reason, or the answer his
// change replaced.
export function answerReason(question: Question): string {
  if (question.replaced_answer) return "Changed from " + question.replaced_answer;
  const reason = cfoReason(question);
  return reason ? "Reason: " + reason : "";
}

// Who answered, as History marks it: you, the CFO, or the CFO while you were
// away. A question nobody answered, or whose answer is still on its way or
// did not arrive, keeps the mark of what became of it, so its row reads sent,
// then delivered.
export function answerMark(question: Question): "" | "you" | "cfo" | "away" {
  if (questionOutcome(question) !== "answered" || question.status !== "succeeded") return "";
  if (question.answered_by !== "cfo") return "you";
  return question.answered_away ? "away" : "cfo";
}

// Whether he can change a question's answer: it has one, whoever gave it, on
// its way or delivered, so he can choose another at any time. An answer that
// did not arrive is none, and the supervisor checks the same again.
export function canChange(question: Question): boolean {
  if (question.status === "succeeded") return !!question.answered_by || !!question.answer_id;
  return (question.status === "queued" || question.status === "running" || question.status === "uncertain") && !!question.answer_id;
}

// answeredHere is a question as he answered it from its card: the board's own
// once it carries his answer, else with the answer he sent, from the moment he
// sent it, so going back to it shows his answer before the board hears back.
export function answeredHere(question: Question, draft: SentDraft): Question {
  if (question.status !== "pending" || !draft.submission) return question;
  const sent = object(JSON.parse(draft.submission.payload));
  return { ...question, status: "queued", answer_id: draft.submission.id, answer: string(sent.text), answer_kind: string(sent.answer_kind) };
}

// His change of a question's answer sent from this board that the board does
// not show yet: from the moment he sends it until the supervisor has it, when
// the question carries it, or a later change from another tab, itself.
function changeOnItsWay(question: Question, draft: SentDraft, actions: Action[]): { text: string; answer_kind: string } | undefined {
  const { submission } = draft;
  if (!submission || question.change_id === submission.id || actions.some((action) => action.id === submission.id)) return undefined;
  const sent = object(JSON.parse(submission.payload));
  return { text: string(sent.text), answer_kind: string(sent.answer_kind) };
}

// The answer a question stands on, as its card checks it: his change the
// moment he sends it, else the question's own answer. A change the supervisor
// refused leaves no submission, so the answer stands as it was.
export function standingAnswer(question: Question, draft: SentDraft, actions: Action[]): { selection: string; written: string } {
  const sent = changeOnItsWay(question, draft, actions);
  const kind = sent ? sent.answer_kind : question.answer_kind;
  const text = sent ? sent.text : kind === "other" ? question.answer : chosenOption(question);
  if (kind === "other") return { selection: "other", written: text };
  return { selection: text ? "option:" + text : "", written: "" };
}

// Who chose the answer a question stands on, and when, as its card says it.
export function standingWho(question: Question, draft: SentDraft & { sentAt?: string }, actions: Action[]): { mark: "you" | "cfo" | "away"; label: string; at: string } {
  const sent = changeOnItsWay(question, draft, actions);
  const kind = sent ? sent.answer_kind : question.answer_kind;
  const yours = kind === "other" ? "You wrote this" : "You chose this";
  if (sent) return { mark: "you", label: yours, at: draft.sentAt || "" };
  if (question.answered_by === "cfo") return { mark: question.answered_away ? "away" : "cfo", label: !question.answer ? "The CFO answered it" : question.answered_away ? "The CFO chose this while you were away" : "The CFO chose this", at: question.answered_at };
  if (question.answered_in && question.answered_in !== "page") return { mark: "you", label: "You answered in " + question.answered_in, at: question.answered_at };
  return { mark: "you", label: yours, at: question.answered_at };
}

// The one line a change needs when the first answer already did what cannot
// be undone by itself: the asking goblin's pull request merged.
export function changeWarning(question: Question, snapshot: Snapshot): string {
  const task = question.task ? snapshot.tasks.find((candidate) => candidate.id === question.task) : undefined;
  return task?.merged ? "Its pull request already merged. Your change goes as a new instruction." : "";
}

export function outcomeIcon(outcome: QuestionOutcome): IconName {
  if (outcome === "answered") return "check-double";
  return outcome === "failed" || outcome === "uncertain" ? "warning" : "close";
}

// What became of an item, in plain words and as its mark: a question by its
// outcome, a review item by its state. The backend marks a review answered as
// soon as it queues the answer and sets delivered only once the goblin has it,
// so only delivered says it arrived. An answer action that succeeded without
// delivery was handed to the CFO because the goblin was replaced, one of a
// goblin's items that awaits the CFO is being handed to it, and an answer
// whose action has aged out of the snapshot is no longer recorded.
function answerOutcome(review: Review, actions: Action[]): "delivered" | "handed" | "handing" | "pending" | "failed" | "uncertain" | "unrecorded" {
  if (review.delivered) return "delivered";
  const action = actions.find((candidate) => candidate.id === review.answer_id);
  if (!action) return "unrecorded";
  if (action.status === "succeeded") return "handed";
  if (action.status === "failed" || action.status === "uncertain") return action.status;
  return review.task && action.awaiting === "the CFO" ? "handing" : "pending";
}

// A delivery that never arrived reads as what to do about it, in the
// supervisor's words, while its action is still recorded.
export function settledLabel(item: Item, actions: Action[]): string {
  if (item.kind === "browser-signin") return signInOutcome(item.signin);
  if (item.kind === "credential") return credentialSettled(item.request);
  const advice = (answer: string) => actions.find((action) => action.id === answer)?.advice || "";
  if (item.kind === "question") return questionOutcome(item.question) === "uncertain" && advice(item.question.answer_id) || answeredLabel(item.question);
  // A command's exit code is kept here, beside how it ended; one he stopped
  // needs no reason, which speaks of him to the CFO.
  if (item.kind === "run") {
    const label = item.run.update ? updateOutcome(item.run).label + " " + item.run.update.to : runMark(item.run).label + (item.run.exit_code === null ? "" : " · exit " + item.run.exit_code);
    return label + (item.run.reason && item.run.state !== "stopped" && !label.includes(item.run.reason) ? ": " + item.run.reason : "");
  }
  const { state, answer, reason, task } = item.review;
  const asker = task ? "the goblin" : "the CFO";
  if (state === "withdrawn") return "Withdrawn: " + reason;
  if (state !== "answered") return reason || "Cleared";
  if (answeredElsewhere(item)) return reason;
  switch (answerOutcome(item.review, actions)) {
    case "failed": return "Your answer did not reach " + asker;
    case "uncertain": return advice(item.review.answer_id) || "Not confirmed: check " + asker + "'s terminal before answering again";
    case "pending": return "You wrote: " + answer + " (not yet delivered to " + asker + ")";
    case "handing": return "You wrote: " + answer + " (not yet delivered to the CFO)";
    case "handed": return "Sent to the CFO: " + answer;
    case "unrecorded": return "You wrote: " + answer + " (delivery no longer recorded)";
    case "delivered": return "You wrote: " + answer;
  }
}

export function settledIcon(item: Item, actions: Action[]): { icon: IconName; tone: string } {
  if (item.kind === "browser-signin") return item.signin.state === "signed_in" ? { icon: "check-double", tone: "succeeded" } : { icon: "close", tone: item.signin.state };
  if (item.kind === "credential") return item.request.state === "saved" || item.request.state === "done" ? { icon: "check-double", tone: "succeeded" } : { icon: "close", tone: item.request.state };
  if (item.kind === "question") {
    const outcome = questionOutcome(item.question);
    // One check while a board answer is on its way, two once it is delivered.
    if (outcome === "answered" && item.question.status !== "succeeded") return { icon: "check", tone: "queued" };
    return { icon: outcomeIcon(outcome), tone: outcome === "answered" ? "succeeded" : outcome };
  }
  if (item.kind === "run") return { icon: (item.run.update ? updateOutcome(item.run) : runMark(item.run)).icon, tone: item.run.state };
  if (item.review.state !== "answered") return { icon: "close", tone: item.review.state };
  if (answeredElsewhere(item)) return { icon: "check-double", tone: "succeeded" };
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
