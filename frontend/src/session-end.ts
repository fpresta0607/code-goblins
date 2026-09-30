import type { Task } from "./types.ts";

export function sessionEnd(task: Task): "retired" | "paused" | "stopped" | null {
  if (task.phase === "paused" || task.phase === "stopped") return task.phase;
  return task.archived && !task.id.startsWith("merged:") ? "retired" : null;
}
