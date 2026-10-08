import type { Snapshot, Task } from "./types.ts";
import { turnStatus } from "./start.ts";
import { pausedWithParent, pauseStatus } from "./task-words.ts";
import { asksOverlord, nodeStatus, statusPhase } from "./workflow.ts";

// A task's status: the words it reads and the phase its dot is drawn in.
export interface TaskStatus { text: string; phase: string }

// taskStatus is the one status a task reads, on its card, its panel, its
// terminal pane and its canvas node, all from the same snapshot, so they
// change together and never disagree (the Overlord, 2026-10-08: "status dots
// need to be better synched across pane and panels and boards"). A Start or
// Resume he clicked that waits for memory or disk has not begun and says
// when it runs, and a paused goblin says why it waits and what resumes it.
export function taskStatus(task: Task, snapshot: Snapshot): TaskStatus {
  const trains = snapshot.merge_trains ?? [], phase = statusPhase(task, trains);
  const waits = turnStatus(task, snapshot.memory, snapshot.disk);
  if (waits) return { text: waits, phase: task.phase };
  if (phase === "paused") return { text: pauseStatus(task.lifecycle?.pause, snapshot.tasks, snapshot.ci_durations, pausedWithParent(task, snapshot.tasks)), phase };
  return { text: nodeStatus({ id: task.id, title: task.title, task, relation: "" }, asksOverlord(snapshot, task.id), snapshot.tasks, trains), phase };
}
