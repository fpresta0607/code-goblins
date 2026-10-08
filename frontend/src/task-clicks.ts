import type { Snapshot, Task } from "./types.ts";

// What the Overlord clicked on a task: a Start, Pause, Resume or Stop, and
// the revision from which a snapshot shows it, null until the supervisor
// answers. A snapshot carries the whole fleet and can take seconds to reach
// the board, so the click shows on the task in the frame he clicks, as
// Starting, Pausing, Resuming or Stopping, until a snapshot from that
// revision on shows the task itself.
export type TaskAction = "start" | "pause" | "resume" | "stop";
export interface TaskClick { action: TaskAction; revision: number | null }

const PHASES = { pause: "pausing", resume: "resuming", stop: "stopping" } as const;

// withClicks is the snapshot with each click still on its way shown on its
// task, or the snapshot itself when none is.
export function withClicks(snapshot: Snapshot, clicks: ReadonlyMap<string, TaskClick>): Snapshot {
  const isOnItsWay = (click: TaskClick | undefined): click is TaskClick => !!click && (click.revision === null || snapshot.revision < click.revision);
  if (!snapshot.tasks.some((task) => isOnItsWay(clicks.get(task.id)))) return snapshot;
  return { ...snapshot, tasks: snapshot.tasks.map((task) => {
    const click = clicks.get(task.id);
    return isOnItsWay(click) ? clicked(task, click.action) : task;
  }) };
}

// A Start on its way waits its turn until the supervisor takes it, so the
// card he clicked reads Starting where it is and leaves Tasks once the
// supervisor starts it.
function clicked(task: Task, action: TaskAction): Task {
  return action === "start" ? { ...task, starting: true, asked: true } : { ...task, phase: PHASES[action] };
}
