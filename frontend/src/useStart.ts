import type { Snapshot, Task } from "./types";
import { object } from "./types";
import type { CardStart } from "./TaskCard";
import { message, reportToCfo, request } from "./api";
import { startBlock, type AcceptedStart } from "./start";
import { recordClick } from "./use-task-clicks";

export type CardStarter = (task: Task) => CardStart;

// Start on a queued task, one click: the card says Starting in the frame he
// clicks, the supervisor dispatches it through cfo spawn in its turn, and
// onStarted hears once it took the start, so the board can open the new
// goblin's session when it is up. A Start the supervisor refuses puts the
// card back and goes to the CFO, never to a line on the board (the
// Overlord, 2026-10-08). The board holds one for every list of queued tasks,
// so each shows the same Start; there is none before the first snapshot.
export function useStart(snapshot: Snapshot | null, onStarted: (accepted: AcceptedStart) => void): CardStarter | null {
  if (!snapshot) return null;
  const start = async (task: Task) => {
    recordClick(task.id, { action: "start", revision: null });
    try {
      const accepted = object(await request("/api/tasks/start", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ task: task.id }) }));
      const revision = typeof accepted.revision === "number" ? accepted.revision : 0;
      recordClick(task.id, { action: "start", revision });
      onStarted({ id: task.id, revision });
    } catch (error: unknown) {
      recordClick(task.id, null);
      reportToCfo("the Start of " + task.id, message(error));
    }
  };
  return (task: Task): CardStart => {
    const blocked = startBlock(task);
    return { blocked, onStart: () => { if (!blocked) void start(task); } };
  };
}
