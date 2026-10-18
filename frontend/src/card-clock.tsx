import type { Task } from "./types";
import { Icon } from "./Icon";
import { cardDate, clockText } from "./cards";
import { taskColumn } from "./workflow";

// A goblin's quiet clock, the same on its board card and its canvas card: how
// long its session has run or how long it has waited, or when it paused or
// finished, as its month and day or, for today, its time. A queued task the
// supervisor is starting has waited, and has not run yet.
export function CardClock({ task, now }: { task: Task; now: number }) {
  const column = taskColumn(task), waiting = task.phase === "queued";
  const clock = column === "Completed" || column === "Paused" || task.phase === "paused" || waiting && column !== "Tasks" ? "" : clockText(task.since, now, waiting ? "waiting" : "running");
  if (clock) return <span className="card-clock"><Icon name="clock" /><span className="sr-only">{waiting ? "Waiting for" : "Running for"} </span>{clock}</span>;
  const ended = (column === "Paused" || column === "Completed") && task.at ? new Date(task.at) : null;
  if (!ended || !Number.isFinite(ended.getTime())) return null;
  return <span className="card-clock"><Icon name="clock" /><time dateTime={task.at}>{cardDate(ended, now)}</time></span>;
}
