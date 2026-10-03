import { isOpen, itemFor, newestItemOf, openKeys, waitingItems, waitReason, waitsOnOverlord, type Item } from "./commandQueue.ts";
import { credentialAsk } from "./credentials.ts";
import { messageBlocks } from "./messageText.ts";
import type { Snapshot, Task } from "./types.ts";
import { pullRequestLabel } from "./workflow.ts";

// What an alert opens: a Command Center item by its key, where an empty key
// opens the first item waiting or the inbox, or a goblin's task.
export type AlertTarget = { kind: "command"; key: string } | { kind: "task"; id: string } | { kind: "cfo" };

// An alert tells the Overlord that something needs him or finished: its key
// is the one event it stands for, a Command Center item's own id or a
// goblin's news, and says is who says what, by which a copy of it is known.
// It is a goblin, or the CFO, coming up to him: speaker names who, text is its
// one plain line, and action is the one thing to do about it.
export interface BoardAlert {
  key: string;
  says: string;
  tone: "needs" | "failed" | "done";
  speaker: string;
  text: string;
  action: string;
  // task is the goblin the alert is about or that asks; empty for the CFO.
  task: string;
  target: AlertTarget;
}

// How much of a message an alert shows, and of the name it starts with, so a
// goblin titled with a whole sentence still leaves room for what happened.
const TEXT_LIMIT = 160;
const NAME_LIMIT = 60;
const shortened = (text: string, limit = TEXT_LIMIT) => text.length > limit ? text.slice(0, limit - 1).trimEnd() + "…" : text;

// Everything in the Command Center opens there.
const OPEN_COMMAND_CENTER = "Open Command Center";

// A goblin speaks by its title, as its card and the Command Center inbox name
// it, falling back to its id.
const nameOf = (tasks: Task[], id: string) => tasks.find((task) => task.id === id)?.title || id;

// An item alerts by its own id, and says what it asks and who asks it, so the
// same ask filed again under a new item, such as a wait a goblin files once
// more, is known as a copy.
function itemAlert(item: Item, tasks: Task[]): BoardAlert {
  const target: AlertTarget = { kind: "command", key: item.key };
  const alert = (task: string, text: string): BoardAlert => ({ key: item.key, says: "item:" + (task || "cfo") + ":" + text, tone: "needs", speaker: task ? nameOf(tasks, task) : "CFO", text: shortened(text), action: OPEN_COMMAND_CENTER, task, target });
  const asker = (task: string) => task ? shortened(nameOf(tasks, task), NAME_LIMIT) : "The CFO";
  if (item.kind === "question") {
    // A question says its lead: the first paragraph or bullet, without the
    // details that follow.
    const [lead] = messageBlocks(item.question.text);
    const spans = !lead ? [] : lead.kind === "paragraph" ? lead.spans : lead.items[0];
    return alert(item.question.task, asker(item.question.task) + " asks: " + spans.map((span) => span.text).join("").replace(/\s+/g, " ").trim());
  }
  if (item.kind === "review") return alert(item.review.task, asker(item.review.task) + (waitsOnOverlord(item.review) ? " is waiting on you: " + waitReason(item.review) : " wants your review: " + item.review.title));
  if (item.kind === "credential") return alert(item.request.task, asker(item.request.task) + " asks: " + credentialAsk(item.request));
  return alert("", "A command waits for you to run it: " + item.run.title);
}

// A goblin's blocked or failed notify waits on the CFO, and its task reads so
// until he answers or acks it.
const asksCFO = (task: Task) => task.reason.startsWith("Waiting on the CFO");

// A goblin's state that the Overlord hears of: blocked or failed by its
// evidence or failed by its own report, or done with its pull request.
// Anything else, such as working, in review or waiting on another task, is
// routine and says nothing. A goblin waiting on its own question, which its
// task reads as Waiting on the CFO, says nothing either: its question is the
// CFO's to answer, who is woken for it, so nothing waits on the Overlord.
function taskState(task: Task): "blocked" | "failed" | "done" | "" {
  if (task.phase === "blocked") return asksCFO(task) ? "" : "blocked";
  if (task.phase === "failed" || task.report === "failed") return asksCFO(task) ? "" : "failed";
  return (task.phase === "done" || task.report === "done") && task.pr ? "done" : "";
}

// A failed report that waited on the CFO was his question. Once he handled
// it, the goblin's report still reads failed until it reports again: that is
// the same question, not a failure. A goblin failed or blocked by its
// evidence is news all the same.
const handledByCFO = (prior: Task, task: Task) => asksCFO(prior) && prior.report === "failed" && task.report === "failed" && task.phase !== "failed" && task.phase !== "blocked";

// A blocked goblin needs the Overlord, so its alert opens the Command Center
// on its newest item waiting there, or with no item, on the first one waiting
// or the inbox. Only its done or failed news opens the goblin itself. Its
// news has no id of its own, so its key is what it says: the same pull request
// or the same failure, and its next pull request is another.
function taskAlert(task: Task, state: "blocked" | "failed" | "done", next: Snapshot): BoardAlert {
  const name = shortened(task.title || task.id, NAME_LIMIT);
  const blocked = state === "blocked";
  const target: AlertTarget = blocked ? { kind: "command", key: newestItemOf(next, task.id)?.key || "" } : { kind: "task", id: task.id };
  const alert = (tone: BoardAlert["tone"], news: string, text: string): BoardAlert => {
    const key = "task:" + task.id + ":" + task.generation + ":" + state + ":" + news;
    return { key, says: key, tone, speaker: task.title || task.id, text: shortened(text), action: blocked ? OPEN_COMMAND_CENTER : "Open", task: task.id, target };
  };
  if (state === "done") return alert("done", task.pr, name + " finished: " + pullRequestLabel(task.pr) + " is ready.");
  const reported = task.activity.startsWith("failed: ") ? task.activity.slice("failed: ".length) : task.activity;
  const said = state === "failed" && task.report === "failed" ? reported : task.reason;
  return alert(state === "failed" ? "failed" : "needs", said, name + (state === "failed" ? " failed: " : " is blocked: ") + (said || "it needs a decision to go on."));
}

// boardAlerts is what changed between two snapshots that needs the Overlord
// or finished: a new question, review, command or credential request in the
// Command Center, and a goblin that became blocked, failed or done with its
// pull request. The first snapshot a page sees alerts nothing: what already
// waits is under the badge.
// A question that was open inside its page's card is not new when it shows
// as a card of its own.
// A goblin's failed question that the CFO handled is no failure after it.
// The Completed column's history is not a goblin finishing, so it alerts
// nothing either.
export function boardAlerts(previous: Snapshot | null, next: Snapshot): BoardAlert[] {
  const notices: BoardAlert[] = [];
  const quiet = next.cfo_quiet;
  if (quiet && quiet.since !== previous?.cfo_quiet?.since) {
    notices.push({ key: "cfo-quiet:" + quiet.since, says: "quiet-cfo:" + quiet.since, tone: "needs", speaker: "CFO", task: "",
      text: `The CFO has not answered ${quiet.count} question${quiet.count === 1 ? "" : "s"}; the oldest has waited ${Math.floor(quiet.oldest_age / 60)} minutes.`,
      action: "Open the CFO's terminal", target: { kind: "cfo" } });
  }
  if (!previous) return notices;
  const known = openKeys(previous);
  const items = waitingItems(next).filter((item) => !known.has(item.key)).map((item) => itemAlert(item, next.tasks));
  const before = new Map(previous.tasks.map((task) => [task.id, task]));
  const tasks = next.tasks.filter((task) => !task.archived).flatMap((task) => {
    const state = taskState(task);
    const prior = before.get(task.id);
    const changed = !prior || prior.generation !== task.generation || (taskState(prior) !== state && !handledByCFO(prior, task));
    return state && changed ? [taskAlert(task, state, next)] : [];
  });
  return [...notices, ...items, ...tasks];
}

// An alert a browser showed: its key, what it said, and when.
export interface SeenAlert {
  key: string;
  says: string;
  at: number;
}

// How many alerts a browser remembers having shown, newest last.
export const SEEN_LIMIT = 100;

// How long the same words from the same speaker are one event.
const SAME_EVENT_MS = 5 * 60 * 1000;

// unseen is the alerts among a snapshot's that are not copies of one this
// browser showed, each once, and the alerts remembered after showing them. An
// item is a copy of one shown under its id, whenever that was, and any alert
// is a copy of one that said the same less than five minutes before: a
// snapshot, a reconnect or a reload brings no alert back, and the same news
// later is a new event. An item that is a copy by its words is remembered
// under its own id too, at the time of the one it copies, so it never alerts
// later. The list comes back as it was given when nothing was added to it.
export function unseen(alerts: BoardAlert[], seen: readonly SeenAlert[], now: number): { fresh: BoardAlert[]; seen: readonly SeenAlert[] } {
  const shown = [...seen];
  const fresh: BoardAlert[] = [];
  for (const alert of alerts) {
    const isItem = isItemAlert(alert);
    if (isItem && shown.some((one) => one.key === alert.key)) continue;
    const said = shown.find((one) => one.says === alert.says && now - one.at < SAME_EVENT_MS);
    if (said && !isItem) continue;
    shown.push({ key: alert.key, says: alert.says, at: said ? said.at : now });
    if (!said) fresh.push(alert);
  }
  return { fresh, seen: shown.length === seen.length ? seen : shown.slice(-SEEN_LIMIT) };
}

// An item's alert has the item's key and says its words; a goblin's news has
// no id of its own, so its key is what it says.
export const isItemAlert = (alert: BoardAlert) => alert.key !== alert.says;

// The name the supervisor records an alert under, short enough for it to
// take: a goblin's long reason is one event by how it starts. The cut never
// leaves half of a character, which the supervisor would record as another.
export function announceKey(alert: BoardAlert): string {
  const name = ("alert:" + alert.key).slice(0, 160);
  const last = name.charCodeAt(name.length - 1);
  return last >= 0xd800 && last <= 0xdbff ? name.slice(0, -1) : name;
}

// An alert whose item the snapshot shows closed has nothing left to open: he
// answered or cleared it, here or anywhere else. An item the snapshot does
// not hold, as while the supervisor restarts, is not known to be closed.
export function outlived(alert: BoardAlert, snapshot: Snapshot): boolean {
  if (alert.target.kind === "cfo") return alert.key !== "cfo-quiet:" + snapshot.cfo_quiet?.since;
  const item = isItemAlert(alert) ? itemFor(snapshot, alert.key) : undefined;
  return !!item && !isOpen(item);
}

// The most toasts shown at once; the oldest leaves first.
const MAX_TOASTS = 4;

// arrive stacks fresh alerts below the toasts on screen. A goblin's news that
// comes again takes the place of its toast still resting there.
export function arrive(shown: BoardAlert[], fresh: BoardAlert[]): BoardAlert[] {
  const keys = new Set(fresh.map((alert) => alert.key));
  return [...shown.filter((toast) => !keys.has(toast.key)), ...fresh].slice(-MAX_TOASTS);
}

// A Windows notification is for an alert the Overlord cannot see: the board
// is out of sight, its tab hidden or its window minimized, and he allowed
// them. A board on screen notifies nothing, in front or not.
export function notifies(permission: NotificationPermission | "unsupported", hidden: boolean): boolean {
  return permission === "granted" && hidden;
}

// One item is one signal: what waits on him shows on the bar's Open Command
// Center button and under the count, so its alert is no toast. A goblin's
// news, which waits on nothing, is one.
export const showsToast = (alert: BoardAlert) => !isItemAlert(alert) || alert.target.kind === "cfo";

// The board asks for Windows notifications once, with the first alert, and
// never again once he has answered or dismissed the ask.
export function asksPermission(permission: NotificationPermission | "unsupported", asked: boolean): boolean {
  return permission === "default" && !asked;
}
