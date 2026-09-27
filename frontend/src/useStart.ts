import { useState } from "react";
import type { Snapshot, Task } from "./types";
import { object } from "./types";
import type { CardStart } from "./TaskCard";
import { message, request } from "./api";
import { startBlock, startOutcome, type AcceptedStart } from "./start";

// Start on a queued task: the supervisor dispatches it through cfo spawn, and
// onStarted hears once it accepted the start, so the board can open the new
// goblin's session when it is up. A refused start shows its reason on the
// card, and a spawn that failed shows the reason the supervisor recorded,
// never the last Start's while this one is awaited.
export function useStart(snapshot: Snapshot, awaited: AcceptedStart | null, onStarted: (accepted: AcceptedStart) => void) {
  const [problems, setProblems] = useState<Record<string, string>>({});
  const [requesting, setRequesting] = useState("");
  const start = async (task: Task) => {
    setRequesting(task.id);
    setProblems((prior) => ({ ...prior, [task.id]: "" }));
    try {
      const accepted = object(await request("/api/tasks/start", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ task: task.id }) }));
      onStarted({ id: task.id, revision: typeof accepted.revision === "number" ? accepted.revision : 0 });
    } catch (failure: unknown) {
      setProblems((prior) => ({ ...prior, [task.id]: message(failure) }));
    } finally {
      setRequesting("");
    }
  };
  return (task: Task, index: number): CardStart => {
    const another = requesting !== "" && requesting !== task.id || snapshot.tasks.some((other) => other.starting && other.id !== task.id);
    return {
      blocked: requesting === task.id ? "Starting" : startBlock(task, snapshot.memory, another),
      problem: problems[task.id] || (awaited?.id === task.id && startOutcome(awaited, snapshot) === "wait" ? "" : task.start_error),
      prominent: index === 0,
      onStart: () => void start(task),
    };
  };
}
