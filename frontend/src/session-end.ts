import type { Task } from "./types.ts";

export function sessionEnd(task: Task): "retired" | "paused" | "stopped" | null {
  if (task.archived && task.retired_at && !task.retired_at.startsWith("0001") && task.lifecycle?.phase !== "stopped") return "retired";
  if (task.phase === "paused" || task.phase === "stopped") return task.phase;
  return task.archived && !task.id.startsWith("merged:") ? "retired" : null;
}
