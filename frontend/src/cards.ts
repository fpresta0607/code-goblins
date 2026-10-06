import type { PanelView } from "./GoblinPanel";
import type { Session, Task } from "./types";
import { sessionEnd } from "./session-end.ts";
import { taskColumn } from "./workflow.ts";

// How long a goblin's session has run, or a queued task has waited, in whole
// minutes, hours or days; empty when the start is unknown, so a card never
// shows a clock it cannot back.
export function clockText(since: string, now: number, kind: "running" | "waiting"): string {
  const start = since && !since.startsWith("0001") ? Date.parse(since) : NaN;
  if (!Number.isFinite(start)) return "";
  const minutes = Math.max(0, Math.floor((now - start) / 60_000));
  if (minutes < 1) return kind === "running" ? "just started" : "just queued";
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ${minutes % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

// The supervisor raises a goblin to the CFO once it has gone this long
// without real progress (PROGRESS_THRESHOLD in internal/supervisor/work_speed.go).
const PROGRESS_THRESHOLD_MINUTES = 20;

// How long a live goblin has gone without real progress (a commit, a push, a
// gate step or a new status report), only once that passes the threshold; a
// goblin making progress, or one that delivered, says nothing.
export function stalledText(task: Task, now: number): string {
  const since = task.progress?.at || "";
  if (taskColumn(task) !== "In progress" || task.phase === "done" || !since || now - Date.parse(since) < PROGRESS_THRESHOLD_MINUTES * 60_000) return "";
  return "No progress for " + clockText(since, now, "running");
}

// A goblin's description is cut to a few lines with Show more while it runs
// past them; opened, it offers Show less.
export function showMoreLabel(expanded: boolean, overflowing: boolean): "Show more" | "Show less" | "" {
  if (expanded) return "Show less";
  return overflowing ? "Show more" : "";
}

// The views a panel offers: a queued task has no terminal yet, so its panel
// is its Task view alone; the CFO, a started task and a reported child
// session each have a terminal too.
export function panelViews(task?: Task, node?: Session): PanelView[] {
  if (task && sessionEnd(task)) return ["task", "terminal"];
  if (task && (task.archived || ["pausing", "stopping"].includes(task.phase))) return ["task"];
  return task && !task.generation && !node ? ["task"] : ["task", "terminal"];
}
