import type { Snapshot, Task } from "./types.ts";
import { ownsTaskSession } from "./lineageTree.ts";
import { sessionEnd } from "./session-end.ts";

// What Open in terminal sends for the terminal a panel shows, named the way
// that terminal's own view names it: a native view by its query, a Herdr
// view by its task, owning session and generation, and the CFO's Herdr view
// by nothing at all.
export type WindowTarget = { native: string } | { task: string; session: string; generation: string } | Record<string, never>;

export function windowTarget(snapshot: Snapshot, cfo: boolean, task?: Task): WindowTarget | null {
  if (cfo) return snapshot.cfo_terminal ? { native: "cfo=" + encodeURIComponent(snapshot.cfo_terminal) } : {};
  if (!task?.generation || task.archived || sessionEnd(task)) return null;
  if (task.backend === "native") return { native: new URLSearchParams({ task: task.id, generation: task.generation }).toString() };
  const owner = snapshot.sessions.find((session) => ownsTaskSession(session, task));
  return { task: task.id, session: owner?.id || "", generation: task.generation };
}
