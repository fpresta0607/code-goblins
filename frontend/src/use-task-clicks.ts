import { useSyncExternalStore } from "react";
import type { TaskClick } from "./task-clicks";

// The Overlord's clicks on tasks this page has not seen a snapshot of yet,
// by task: a card, the panel and the canvas each show the same click.
let clicks: ReadonlyMap<string, TaskClick> = new Map();
const listeners = new Set<() => void>();

// recordClick keeps what the Overlord clicked on a task, or forgets it when
// the supervisor refused it, which puts the task back as it was.
export function recordClick(task: string, click: TaskClick | null): void {
  const next = new Map(clicks);
  if (click) next.set(task, click); else next.delete(task);
  clicks = next;
  for (const listener of listeners) listener();
}

// isOnItsWay says a click on task is on its way to the supervisor, so a
// second one, such as the other half of a double click, sends nothing.
export function isOnItsWay(task: string): boolean {
  return clicks.get(task)?.revision === null;
}

const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
};

export function useTaskClicks(): ReadonlyMap<string, TaskClick> {
  return useSyncExternalStore(subscribe, () => clicks);
}
