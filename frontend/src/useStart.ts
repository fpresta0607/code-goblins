import { useState } from "react";
import type { Snapshot, Task } from "./types";
import { object } from "./types";
import type { CardStart } from "./TaskCard";
import { message, request } from "./api";
import { startBlock, type AcceptedStart } from "./start";

export type CardStarter = (task: Task) => CardStart;

// Start on a queued task: the supervisor dispatches it through cfo spawn, and
// onStarted hears once it accepted the start, so the board can open the new
// goblin's session when it is up. Why a start cannot run is in its button's
// tip, and onStart answers with what a refused start met, which the card
// shows for a moment. The board holds one for every list of queued tasks, so
// each shows the same Start; there is none before the first snapshot.
export function useStart(snapshot: Snapshot | null, onStarted: (accepted: AcceptedStart) => void): CardStarter | null {
  const [requesting, setRequesting] = useState("");
  if (!snapshot) return null;
  const blockOf = (task: Task) => {
    const another = requesting !== "" && requesting !== task.id || snapshot.tasks.some((other) => (other.starting || other.phase === "resuming") && other.id !== task.id);
    return requesting === task.id ? "Starting" : startBlock(task, snapshot.memory, another, snapshot.disk);
  };
  const start = async (task: Task): Promise<string> => {
    setRequesting(task.id);
    try {
      const accepted = object(await request("/api/tasks/start", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ task: task.id }) }));
      onStarted({ id: task.id, revision: typeof accepted.revision === "number" ? accepted.revision : 0 });
      return "";
    } catch (error: unknown) { return message(error); } finally {
      setRequesting("");
    }
  };
  return (task: Task): CardStart => {
    const blocked = blockOf(task);
    return { blocked, onStart: () => blocked ? Promise.resolve("") : start(task) };
  };
}
