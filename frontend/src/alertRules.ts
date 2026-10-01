import { newestItemOf, waitingItems, waitReason, waitsOnOverlord, type Item } from "./commandQueue.ts";
import { messageBlocks } from "./messageText.ts";
import type { Snapshot, Task } from "./types.ts";
import { pullRequestLabel } from "./workflow.ts";

// What an alert opens: a Command Center item by its key, where an empty key
// opens the first item waiting or the inbox, or a goblin's task.
export type AlertTarget = { kind: "command"; key: string } | { kind: "task"; id: string };

// An alert tells the Overlord that something needs him or finished: its key
// is the one event it stands for, so an event alerts once. It is a goblin, or
// the CFO, coming up to him: speaker names who, text is its one plain line,
// and action is the one thing to do about it.
export interface BoardAlert {
  key: string;
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

// An item alerts by what it says and who says it, so the same ask filed again
// under a new item, such as a wait a goblin files once more, is one event.
function itemAlert(item: Item, tasks: Task[]): BoardAlert {
  const target: AlertTarget = { kind: "command", key: item.key };
  const alert = (task: string, text: string): BoardAlert => ({ key: "item:" + (task || "cfo") + ":" + text, tone: "needs", speaker: task ? nameOf(tasks, task) : "CFO", text: shortened(text), action: OPEN_COMMAND_CENTER, task, target });
  const asker = (task: string) => task ? shortened(nameOf(tasks, task), NAME_LIMIT) : "The CFO";
  if (item.kind === "question") {
    // A question says its lead: the first paragraph or bullet, without the
    // details that follow.
    const [lead] = messageBlocks(item.question.text);
    const spans = !lead ? [] : lead.kind === "paragraph" ? lead.spans : lead.items[0];
    return alert(item.question.task, asker(item.question.task) + " asks: " + spans.map((span) => span.text).join("").replace(/\s+/g, " ").trim());
  }
  if (item.kind === "review") return alert(item.review.task, asker(item.review.task) + (waitsOnOverlord(item.review) ? " is waiting on you: " + waitReason(item.review) : " wants your review: " + item.review.title));
  return alert("", "A command waits for you to run it: " + item.run.title);
}

// A goblin's state that the Overlord hears of: blocked or failed by its
// evidence or failed by its own report, or done with its pull request.
// Anything else, such as working, in review or waiting on another task, is
// routine and says nothing. A goblin blocked on its own question, which its
// task reads as its report or as Waiting on the CFO, says nothing either: the
// question alerts by itself when it reaches the Command Center.
function taskState(task: Task): "blocked" | "failed" | "done" | "" {
  if (task.phase === "blocked") return task.report === "blocked" || task.reason.startsWith("Waiting on the CFO") ? "" : "blocked";
  if (task.phase === "failed" || task.report === "failed") return "failed";
  return (task.phase === "done" || task.report === "done") && task.pr ? "done" : "";
}

// A blocked goblin needs the Overlord, so its alert opens the Command Center
// on its newest item waiting there, or with no item, on the first one waiting
// or the inbox. Only its done or failed news opens the goblin itself. Its key
// is the news itself: the same pull request or the same failure is one event,
// and its next pull request is another.
function taskAlert(task: Task, state: "blocked" | "failed" | "done", next: Snapshot): BoardAlert {
  const name = shortened(task.title || task.id, NAME_LIMIT);
  const blocked = state === "blocked";
  const target: AlertTarget = blocked ? { kind: "command", key: newestItemOf(next, task.id)?.key || "" } : { kind: "task", id: task.id };
  const alert = (tone: BoardAlert["tone"], news: string, text: string): BoardAlert => ({ key: "task:" + task.id + ":" + task.generation + ":" + state + ":" + news, tone, speaker: task.title || task.id, text: shortened(text), action: blocked ? OPEN_COMMAND_CENTER : "Open", task: task.id, target });
  if (state === "done") return alert("done", task.pr, name + " finished: " + pullRequestLabel(task.pr) + " is ready.");
  const reported = task.activity.startsWith(state + ": ") ? task.activity.slice(state.length + 2) : task.activity;
  const said = task.report === state ? reported : task.reason;
  return alert(state === "failed" ? "failed" : "needs", said, name + (state === "failed" ? " failed: " : " is blocked: ") + (said || "it needs a decision to go on."));
}

// boardAlerts is what changed between two snapshots that needs the Overlord
// or finished: a new question, review or command in the Command Center, and a
// goblin that became blocked, failed or done with its pull request. The first
// snapshot a page sees alerts nothing: what already waits is under the badge.
// The Completed column's history is not a goblin finishing, so it alerts
// nothing either.
export function boardAlerts(previous: Snapshot | null, next: Snapshot): BoardAlert[] {
  if (!previous) return [];
  const known = new Set(waitingItems(previous).map((item) => item.key));
  const items = waitingItems(next).filter((item) => !known.has(item.key)).map((item) => itemAlert(item, next.tasks));
  const before = new Map(previous.tasks.map((task) => [task.id, task]));
  const tasks = next.tasks.filter((task) => !task.archived).flatMap((task) => {
    const state = taskState(task);
    const prior = before.get(task.id);
    const changed = !prior || prior.generation !== task.generation || taskState(prior) !== state;
    return state && changed ? [taskAlert(task, state, next)] : [];
  });
  return [...items, ...tasks];
}

// How many alert keys a browser remembers having shown, newest last.
export const SEEN_LIMIT = 100;

// unseen is the alerts among a snapshot's that this page has never shown,
// each once, and the keys remembered after showing them: an event alerts once
// however often a snapshot, a reconnect or a reload brings it back.
export function unseen(alerts: BoardAlert[], seen: readonly string[]): { fresh: BoardAlert[]; seen: string[] } {
  const keys = new Set(seen);
  const fresh: BoardAlert[] = [];
  for (const alert of alerts) {
    if (keys.has(alert.key)) continue;
    keys.add(alert.key);
    fresh.push(alert);
  }
  return { fresh, seen: [...seen, ...fresh.map((alert) => alert.key)].slice(-SEEN_LIMIT) };
}

// The most toasts shown at once; the oldest leaves first.
const MAX_TOASTS = 4;

// arrive stacks fresh alerts below the toasts on screen.
export function arrive(shown: BoardAlert[], fresh: BoardAlert[]): BoardAlert[] {
  return [...shown, ...fresh].slice(-MAX_TOASTS);
}

// A Windows notification is for an alert the Overlord would not see: the
// board's tab is hidden or its window is not in front, and he allowed them.
export function notifies(permission: NotificationPermission | "unsupported", hidden: boolean, focused: boolean): boolean {
  return permission === "granted" && (hidden || !focused);
}

// The board asks for Windows notifications once, with the first alert, and
// never again once he has answered or dismissed the ask.
export function asksPermission(permission: NotificationPermission | "unsupported", asked: boolean): boolean {
  return permission === "default" && !asked;
}
