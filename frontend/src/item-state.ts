import { asItems, isOpen, type Item } from "./commandQueue.ts";
import type { Items, Snapshot } from "./types.ts";

// What the board knows of the Command Center's items beyond its last
// snapshot. A snapshot carries the whole fleet and can take seconds to reach
// the board, so three things do not wait for one: the supervisor sends the
// items alone the moment one changes, what the Overlord sends closes its item
// in the frame he sends it, and an item that closed stays closed, whatever
// arrives late.

// Sent is what he sent for an item: the action's kind and request ID, an
// answer's text and kind, and the publication it was sent for.
export interface Sent extends Publication { kind: string; id: string; text: string; answer_kind: string }

// Publication names one publishing of an item. The supervisor takes an ID
// again once it has dropped the record that used it, so an ID alone does not:
// the same ID with another identity or created_at is a new item.
export interface Publication { identity: string; created_at: string }

export function publication(item: Item): Publication {
  const { identity, created_at } = item.kind === "question" ? item.question : item.kind === "review" ? item.review : item.kind === "run" ? item.run : item.request;
  return { identity, created_at };
}
const same = (a: Publication, b: Publication) => a.identity === b.identity && a.created_at === b.created_at;

// withItems is the last snapshot with the Command Center's items the
// supervisor sent after it. Items from another supervisor, or older than the
// snapshot, change nothing, and neither do items with no snapshot to go on.
export function withItems(current: Snapshot | null, items: Items): Snapshot | null {
  if (!current || current.instance !== items.instance || current.revision > items.revision) return current;
  return { ...current, revision: items.revision, questions: items.questions, reviews: items.reviews, runs: items.runs, credentials: items.credentials, actions: items.actions };
}

// How many closed items the board remembers, newest last.
const LIMIT = 500;

// remembered adds every item a snapshot shows closed to those the board has
// seen closed, each as it was last seen. The map comes back as it was given
// when the snapshot shows none.
export function remembered(prior: ReadonlyMap<string, Item>, snapshot: Snapshot): ReadonlyMap<string, Item> {
  const closed = asItems(snapshot).filter((item) => !isOpen(item));
  if (!closed.length) return prior;
  const next = new Map(prior);
  for (const item of closed) {
    next.delete(item.key);
    next.set(item.key, item);
  }
  for (const key of next.keys()) {
    if (next.size <= LIMIT) break;
    next.delete(key);
  }
  return next;
}

// What an item becomes the moment the supervisor takes what he sent for it.
// A command he runs stays open while it runs, so sending it closes nothing.
function afterSending(item: Item, sent: Sent): Item | undefined {
  if (item.kind === "question" && (sent.kind === "cfo_answer" || sent.kind === "goblin_answer")) return { ...item, question: { ...item.question, status: "queued", answer_id: sent.id, answer: sent.text, answer_kind: sent.answer_kind } };
  if (item.kind === "question" && sent.kind === "question_clear") return { ...item, question: { ...item.question, status: "cleared", message: "You dismissed it: answered elsewhere or no longer needed." } };
  if (item.kind === "review" && sent.kind === "review_answer") return { ...item, review: { ...item.review, state: "answered", answer: sent.text, answer_id: sent.id } };
  if (item.kind === "review" && sent.kind === "review_clear") return { ...item, review: { ...item.review, state: "cleared", reason: sent.text } };
  return undefined;
}

// holdClosed is a snapshot in which no item the board knows to be closed is
// open: one it saw closed shows as it last saw it, and one he sent something
// for shows as the supervisor will record it. A snapshot taken before an
// answer and arriving after it therefore brings nothing back. Only the same
// publication is held: an ID published again later is a new item and shows
// open. The snapshot comes back as it was given when it opens nothing.
export function holdClosed(snapshot: Snapshot, closed: ReadonlyMap<string, Item>, sent: ReadonlyMap<string, Sent>): Snapshot {
  if (!closed.size && !sent.size) return snapshot;
  let held = false;
  const kept = (item: Item): Item => {
    if (!isOpen(item)) return item;
    const of = publication(item);
    const seen = closed.get(item.key);
    const acted = sent.get(item.key);
    const was = seen && same(publication(seen), of) ? seen : acted && same(acted, of) ? afterSending(item, acted) : undefined;
    if (was) held = true;
    return was || item;
  };
  const questions = snapshot.questions?.map((question) => { const item = kept({ kind: "question", key: "question:" + question.id, question }); return item.kind === "question" ? item.question : question; });
  const reviews = snapshot.reviews?.map((review) => { const item = kept({ kind: "review", key: "review:" + review.id, review }); return item.kind === "review" ? item.review : review; });
  const runs = snapshot.runs?.map((run) => { const item = kept({ kind: "run", key: "run:" + run.id, run }); return item.kind === "run" ? item.run : run; });
  const credentials = snapshot.credentials?.map((request) => { const item = kept({ kind: "credential", key: "credential:" + request.id, request }); return item.kind === "credential" ? item.request : request; });
  return held ? { ...snapshot, questions, reviews, runs, credentials } : snapshot;
}
