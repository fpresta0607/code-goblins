import { waitingItems, type Item } from "./commandQueue.ts";
import { plainMessage } from "./messageText.ts";
import type { Snapshot, Task } from "./types.ts";

// What an alert opens: a Command Center item by its key, or a goblin's task.
export type AlertTarget = { kind: "command"; key: string } | { kind: "task"; id: string };

// An alert tells the Overlord that something needs him or finished: its key
// is the one event it stands for, so an event alerts once.
export interface BoardAlert {
  key: string;
  tone: "needs" | "failed" | "done";
  title: string;
  text: string;
  // task is the goblin the alert is about or that asks; empty for the CFO.
  task: string;
  target: AlertTarget;
}

// How much of a message an alert shows.
const TEXT_LIMIT = 160;
const shortened = (text: string) => text.length > TEXT_LIMIT ? text.slice(0, TEXT_LIMIT - 1).trimEnd() + "…" : text;

const taskTitle = (snapshot: Snapshot, id: string) => snapshot.tasks.find((task) => task.id === id)?.title || id;

function itemAlert(snapshot: Snapshot, item: Item): BoardAlert {
  const target: AlertTarget = { kind: "command", key: item.key };
  if (item.kind === "question") {
    const asker = item.question.task ? taskTitle(snapshot, item.question.task) : "The CFO";
    return { key: item.key, tone: "needs", title: asker + " asks you", text: shortened(plainMessage(item.question.text)), task: item.question.task, target };
  }
  if (item.kind === "review") {
    const asker = item.review.task ? taskTitle(snapshot, item.review.task) : "The CFO";
    return { key: item.key, tone: "needs", title: asker + " wants your review", text: shortened(item.review.title), task: item.review.task, target };
  }
  return { key: item.key, tone: "needs", title: "A command waits for you to run it", text: shortened(item.run.title), task: "", target };
}

// A goblin's state that the Overlord hears of: blocked, failed, or done with
// its pull request. Anything else, such as working, in review or waiting on
// another task, is routine and says nothing.
function taskState(task: Task): "blocked" | "failed" | "done" | "" {
  if (task.phase === "blocked" || task.phase === "failed") return task.phase;
  return task.phase === "done" && task.pr ? "done" : "";
}

function taskAlert(task: Task, state: "blocked" | "failed" | "done"): BoardAlert {
  const target: AlertTarget = { kind: "task", id: task.id };
  const key = "task:" + task.id + ":" + task.generation + ":" + state;
  if (state === "done") return { key, tone: "done", title: task.title + " is done", text: "Its pull request is ready: " + task.pr, task: task.id, target };
  return { key, tone: state === "failed" ? "failed" : "needs", title: task.title + (state === "failed" ? " failed" : " is blocked"), text: shortened(task.reason || "It needs a decision to go on."), task: task.id, target };
}

// boardAlerts is what changed between two snapshots that needs the Overlord
// or finished: a new question, review or command in the Command Center, and a
// goblin that became blocked, failed or done with its pull request. The first
// snapshot a page sees alerts nothing: what already waits is under the badge.
export function boardAlerts(previous: Snapshot | null, next: Snapshot): BoardAlert[] {
  if (!previous) return [];
  const known = new Set(waitingItems(previous).map((item) => item.key));
  const items = waitingItems(next).filter((item) => !known.has(item.key)).map((item) => itemAlert(next, item));
  const before = new Map(previous.tasks.map((task) => [task.id, task]));
  const tasks = next.tasks.flatMap((task) => {
    const state = taskState(task);
    const prior = before.get(task.id);
    const changed = !prior || prior.generation !== task.generation || taskState(prior) !== state;
    return state && changed ? [taskAlert(task, state)] : [];
  });
  return [...items, ...tasks];
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
