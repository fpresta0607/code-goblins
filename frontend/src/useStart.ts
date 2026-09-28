import { useEffect, useRef, useState } from "react";
import type { Snapshot, Task } from "./types";
import { object } from "./types";
import type { CardStart } from "./TaskCard";
import { message, request } from "./api";
import { refusalStands, startBlock, startOutcome, type AcceptedStart, type Refusal } from "./start";

export type CardStarter = (task: Task) => CardStart;

// Start on a queued task: the supervisor dispatches it through cfo spawn, and
// onStarted hears once it accepted the start, so the board can open the new
// goblin's session when it is up. A refused start, or a press on a blocked
// one, shows its reason on the card: one whose cause passes by itself until a
// newer snapshot shows Start no longer blocked, any other until the card's
// Start is pressed again. A spawn that failed shows the reason the supervisor
// recorded, never the last Start's while this one is awaited. The board holds
// one for every list of queued tasks, so each shows the same Start; there is
// none before the first snapshot.
export function useStart(snapshot: Snapshot | null, awaited: AcceptedStart | null, onStarted: (accepted: AcceptedStart) => void): CardStarter | null {
  const [refusals, setRefusals] = useState<Record<string, Refusal>>({});
  const [requesting, setRequesting] = useState("");
  const revision = useRef(snapshot?.revision ?? 0);
  useEffect(() => { revision.current = snapshot?.revision ?? 0; });
  if (!snapshot) return null;
  const refuse = (id: string, refusal?: Refusal) => setRefusals((prior) =>
    refusal ? { ...prior, [id]: refusal } : Object.fromEntries(Object.entries(prior).filter(([other]) => other !== id)));
  const blockOf = (task: Task) => {
    const another = requesting !== "" && requesting !== task.id || snapshot.tasks.some((other) => other.starting && other.id !== task.id);
    return requesting === task.id ? "Starting" : startBlock(task, snapshot.memory, another);
  };
  for (const [id, refusal] of Object.entries(refusals)) {
    const task = snapshot.tasks.find((candidate) => candidate.id === id);
    if (!refusalStands(refusal, snapshot, task ? blockOf(task) : "")) refuse(id);
  }
  const start = async (task: Task) => {
    setRequesting(task.id);
    refuse(task.id);
    try {
      const accepted = object(await request("/api/tasks/start", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ task: task.id }) }));
      onStarted({ id: task.id, revision: typeof accepted.revision === "number" ? accepted.revision : 0 });
    } catch (failure: unknown) {
      const answer: unknown = failure instanceof Error ? failure.cause : undefined;
      const passing = typeof answer === "object" && answer !== null && "passing" in answer && answer.passing === true;
      refuse(task.id, { reason: message(failure), revision: revision.current, passing });
    } finally {
      setRequesting("");
    }
  };
  return (task: Task): CardStart => {
    const blocked = blockOf(task);
    return {
      blocked,
      problem: refusals[task.id]?.reason || (awaited?.id === task.id && startOutcome(awaited, snapshot) === "wait" ? "" : task.start_error),
      onStart: () => {
        if (!blocked) void start(task);
        else if (blocked !== "Starting") refuse(task.id, { reason: blocked, revision: snapshot.revision, passing: true });
      },
    };
  };
}
