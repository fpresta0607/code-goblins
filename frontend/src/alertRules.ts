import { newestItemOf, waitingItems, waitsOnOverlord, type Item } from "./commandQueue.ts";
import { credentialAsk } from "./credentials.ts";
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

// How much of a message an alert shows.
const TEXT_LIMIT = 160;
const shortened = (text: string) => text.length > TEXT_LIMIT ? text.slice(0, TEXT_LIMIT - 1).trimEnd() + "…" : text;

// Everything in the Command Center opens there.
const OPEN_COMMAND_CENTER = "Open Command Center";

// A goblin speaks by its title, as its card and the Command Center inbox name
// it, falling back to its id.
const nameOf = (tasks: Task[], id: string) => tasks.find((task) => task.id === id)?.title || id;

function itemAlert(item: Item, tasks: Task[]): BoardAlert {
  const target: AlertTarget = { kind: "command", key: item.key };
  const alert = (task: string, text: string): BoardAlert => ({ key: item.key, tone: "needs", speaker: task ? nameOf(tasks, task) : "CFO", text: shortened(text), action: OPEN_COMMAND_CENTER, task, target });
  const asker = (task: string) => task ? nameOf(tasks, task) : "The CFO";
  if (item.kind === "question") {
    // A question says its lead: the first paragraph or bullet, without the
    // details that follow.
    const [lead] = messageBlocks(item.question.text);
    const spans = !lead ? [] : lead.kind === "paragraph" ? lead.spans : lead.items[0];
    return alert(item.question.task, asker(item.question.task) + " asks: " + spans.map((span) => span.text).join("").replace(/\s+/g, " ").trim());
  }
  if (item.kind === "review") return alert(item.review.task, asker(item.review.task) + (waitsOnOverlord(item.review) ? " is waiting on you: " : " wants your review: ") + item.review.title);
  if (item.kind === "credential") return alert(item.request.task, asker(item.request.task) + " asks: " + credentialAsk(item.request));
  return alert("", "A command waits for you to run it: " + item.run.title);
}

// A goblin's state that the Overlord hears of: blocked or failed by its
// evidence or failed by its own report, or done with its pull request.
// Anything else, such as working, in review or waiting on another task, is
// routine and says nothing. A goblin's own blocked report raises a question,
// which alerts by itself when it reaches the Command Center.
function taskState(task: Task): "blocked" | "failed" | "done" | "" {
  if (task.phase === "blocked" || task.phase === "failed") return task.phase;
  if (task.report === "failed") return "failed";
  return (task.phase === "done" || task.report === "done") && task.pr ? "done" : "";
}

// A blocked goblin needs the Overlord, so its alert opens the Command Center
// on its newest item waiting there, or with no item, on the first one waiting
// or the inbox. Only its done or failed news opens the goblin itself.
function taskAlert(task: Task, state: "blocked" | "failed" | "done", next: Snapshot): BoardAlert {
  const name = task.title || task.id;
  const blocked = state === "blocked";
  const target: AlertTarget = blocked ? { kind: "command", key: newestItemOf(next, task.id)?.key || "" } : { kind: "task", id: task.id };
  const key = "task:" + task.id + ":" + task.generation + ":" + state;
  const alert = (tone: BoardAlert["tone"], text: string): BoardAlert => ({ key, tone, speaker: name, text: shortened(text), action: blocked ? OPEN_COMMAND_CENTER : "Open " + name, task: task.id, target });
  if (state === "done") return alert("done", name + " finished: " + pullRequestLabel(task.pr) + " is ready.");
  const reported = task.activity.startsWith(state + ": ") ? task.activity.slice(state.length + 2) : task.activity;
  const said = task.report === state ? reported : task.reason;
  return alert(state === "failed" ? "failed" : "needs", name + (state === "failed" ? " failed: " : " is blocked: ") + (said || "it needs a decision to go on."));
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

// The most toasts shown at once; the oldest leaves first.
const MAX_TOASTS = 4;

// A toast on screen: id is its arrival, so an alert that arrives again is a
// new toast with its own time on screen, not the old one carried over.
export interface Arrival {
  id: number;
  alert: BoardAlert;
}

// arrive stacks fresh alerts below the toasts on screen, each replacing the
// toast its key already has.
export function arrive(shown: Arrival[], fresh: BoardAlert[]): Arrival[] {
  const keys = new Set(fresh.map((alert) => alert.key));
  const last = Math.max(0, ...shown.map((toast) => toast.id));
  return [...shown.filter((toast) => !keys.has(toast.alert.key)), ...fresh.map((alert, index) => ({ id: last + 1 + index, alert }))].slice(-MAX_TOASTS);
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
