import { isOpen, itemFor, openKeys, waitingItems, waitReason, waitsOnOverlord, type Item } from "./commandQueue.ts";
import { credentialAsk } from "./credentials.ts";
import { publishedAt } from "./item-state.ts";
import { messageBlocks, plainMessage } from "./messageText.ts";
import type { Snapshot, Task } from "./types.ts";
import { withoutHarness } from "./task-words.ts";

// An alert tells the Overlord that a Command Center item asks him something,
// and nothing else does (the Overlord, 2026-10-07: "alerts should only be
// open command center questions"): a goblin blocked, failed or done, and the
// CFO falling behind on its questions, are said on the goblin's card and the
// CFO's bar. Its key is the item's id with its publishing, item is the
// Command Center item it opens, and says is who asks what, by which the same
// ask filed again under a new item is known as a copy. speaker names who
// asks, and text is its one plain line.
export interface BoardAlert {
  key: string;
  says: string;
  speaker: string;
  text: string;
  item: string;
}

// How much of a message an alert shows, and of the name it starts with, so a
// goblin titled with a whole sentence still leaves room for what it asks.
const TEXT_LIMIT = 160;
const NAME_LIMIT = 60;
const shortened = (text: string, limit = TEXT_LIMIT) => text.length > limit ? text.slice(0, limit - 1).trimEnd() + "…" : text;

// A goblin speaks by its title, as its card and the Command Center inbox name
// it, falling back to its id.
const nameOf = (tasks: Task[], id: string) => withoutHarness(tasks.find((task) => task.id === id)?.title || "") || id;

// An item's alert is keyed by its id and the publishing it stands for: the
// supervisor takes an ID again once it has dropped the record that used it,
// and the same ID with another created_at is a new item.
const itemAlertKey = (item: Item) => item.key + "@" + publishedAt(item);

function itemAlert(item: Item, tasks: Task[]): BoardAlert {
  const alert = (task: string, text: string): BoardAlert => ({ key: itemAlertKey(item), says: "item:" + (task || "cfo") + ":" + text, speaker: task ? nameOf(tasks, task) : "CFO", text: shortened(text), item: item.key });
  const asker = (task: string) => task ? shortened(nameOf(tasks, task), NAME_LIMIT) : "The CFO";
  if (item.kind === "question") {
    // A question says its lead: the first paragraph or bullet, without the
    // details or tables of values around it.
    const lead = messageBlocks(item.question.text).find((block) => block.kind !== "table");
    const spans = !lead ? [] : lead.kind === "paragraph" ? lead.spans : lead.items[0];
    return alert(item.question.task, asker(item.question.task) + " asks: " + spans.map((span) => span.text).join("").replace(/\s+/g, " ").trim());
  }
  // A wait reads as its goblin's reason in plain words, without the queue's
  // prefix, its page's link or a table of values its card shows.
  if (item.kind === "review") return alert(item.review.task, asker(item.review.task) + (waitsOnOverlord(item.review) ? " is waiting on you: " + plainMessage(waitReason(item.review)) : " wants your review: " + plainMessage(item.review.title)));
  if (item.kind === "credential") return alert(item.request.task, asker(item.request.task) + " asks: " + credentialAsk(item.request));
  // A new release is an event of its own, announced by Code Goblins itself.
  if (item.run.update) return { ...alert("", "Code Goblins " + item.run.update.to + " is ready: update from " + item.run.update.from), speaker: "Code Goblins" };
  if (item.run.task) return alert(item.run.task, asker(item.run.task) + " asks you to run a command: " + plainMessage(item.run.title));
  return alert("", "A command waits for you to run it: " + item.run.title);
}

// boardAlerts is the items new in the Command Center between two snapshots
// that ask the Overlord something: a question, review, command or credential
// request. The first snapshot a page sees alerts nothing: what already waits
// is under the badge. A question that was open inside its page's card is not
// new when it shows as a card of its own.
export function boardAlerts(previous: Snapshot | null, next: Snapshot): BoardAlert[] {
  if (!previous) return [];
  const known = openKeys(previous);
  return waitingItems(next).filter((item) => !known.has(item.key)).map((item) => itemAlert(item, next.tasks));
}

// An alert a browser showed: its key, what it said, and when.
export interface SeenAlert {
  key: string;
  says: string;
  at: number;
}

// How many alerts a browser remembers having shown, newest last.
export const SEEN_LIMIT = 100;

// How long the same ask from the same asker is one event.
const SAME_EVENT_MS = 5 * 60 * 1000;

// unseen is the alerts among a snapshot's that are not copies of one this
// browser showed, each once, and the alerts remembered after showing them. An
// alert is a copy of one shown under its key, whenever that was, or of one
// that said the same less than five minutes before: a snapshot, a reconnect
// or a reload brings no alert back, and the same ask later is a new event. A
// copy by its words is remembered under its own key too, at the time of the
// one it copies, so it never alerts later. The list comes back as it was
// given when nothing was added to it.
export function unseen(alerts: BoardAlert[], seen: readonly SeenAlert[], now: number): { fresh: BoardAlert[]; seen: readonly SeenAlert[] } {
  const shown = [...seen];
  const fresh: BoardAlert[] = [];
  for (const alert of alerts) {
    if (shown.some((one) => one.key === alert.key)) continue;
    const said = shown.find((one) => one.says === alert.says && now - one.at < SAME_EVENT_MS);
    shown.push({ key: alert.key, says: alert.says, at: said ? said.at : now });
    if (!said) fresh.push(alert);
  }
  return { fresh, seen: shown.length === seen.length ? seen : shown.slice(-SEEN_LIMIT) };
}

// What the desktop window's page hears when the Overlord clicks a Windows
// notification the window raised from its own look at the board, which the
// page holds no notification for: the alert's key, whose item it opens.
export const NOTIFICATION_CLICK = "code-goblins-notification-click";

// The Command Center item an alert's key stands for: the key without its
// publishing.
export function alertItem(key: string): string {
  const at = key.lastIndexOf("@");
  return at < 0 ? key : key.slice(0, at);
}

// The name the supervisor records an alert under: its key whole, since an
// item's id is short and its publishing ends it.
export const announceKey = (alert: BoardAlert) => "alert:" + alert.key;

// An alert whose item the snapshot shows closed, or published again since
// under its id, has nothing left to open: he answered or cleared it, here or
// anywhere else. An item the snapshot does not hold, as while the supervisor
// restarts, is not known to be closed.
export function outlived(alert: BoardAlert, snapshot: Snapshot): boolean {
  const item = itemFor(snapshot, alert.item);
  return !!item && (!isOpen(item) || itemAlertKey(item) !== alert.key);
}

// A Windows notification is for an alert the Overlord cannot see: the board
// is out of sight, its tab hidden or its window minimized, and he allowed
// them. A board on screen notifies nothing, in front or not: there the bar's
// Open Command Center, which glows and counts what waits, is the item's one
// signal.
export function notifies(permission: NotificationPermission | "unsupported", hidden: boolean): boolean {
  return permission === "granted" && hidden;
}

// The board asks for Windows notifications once, with the first alert, and
// never again once he has answered or dismissed the ask.
export function asksPermission(permission: NotificationPermission | "unsupported", asked: boolean): boolean {
  return permission === "default" && !asked;
}
