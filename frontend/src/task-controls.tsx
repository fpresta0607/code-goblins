import { useRef, useState, type ReactNode } from "react";
import { object, type Snapshot, type Task } from "./types";
import type { CardStart } from "./TaskCard";
import { message, request } from "./api";
import { queueBlock } from "./start";
import { Icon } from "./Icon";
import { StopTaskDialog } from "./stop-task-dialog";

// leading is a control shown first in the group, such as a card's terminal button.
export function TaskControls({ task, snapshot, start, leading, onAdjust }: {
  task: Task; snapshot: Snapshot; start?: CardStart; leading?: ReactNode; onAdjust: (source: HTMLElement) => void;
}) {
  const [confirmation, setConfirmation] = useState<{ generation: string; revision: string } | null>(null);
  const [pending, setPending] = useState<{ action: string; revision: number | null } | null>(null);
  const [problem, setProblem] = useState("");
  const attempt = useRef<{ payload: string; operation: string } | null>(null);
  if (pending?.revision != null && snapshot.revision >= pending.revision) setPending(null);
  if (confirmation && (confirmation.generation !== task.generation || confirmation.revision !== task.queue_revision)) setConfirmation(null);
  const isChanging = !!pending || task.starting || ["pausing", "stopping", "resuming"].includes(task.phase);
  const isQueued = task.phase === "queued";
  const isResumeRetry = task.lifecycle?.action === "resume" && ["failed", "resuming"].includes(task.lifecycle.phase);
  const canResume = task.phase === "paused" || isResumeRetry;
  const canPause = !!task.generation && !canResume && !task.archived;
  const resumeBlock = !isResumeRetry && snapshot.memory && snapshot.memory.available < snapshot.memory.next ? "Resume needs 5 GB free to keep the 4 GB floor" : "";
  const act = async (action: "pause" | "resume" | "stop") => {
    if (isChanging) return;
    setConfirmation(null); setProblem("");
    const payload = JSON.stringify({ task: task.id, generation: task.generation, revision: task.queue_revision, action });
    if (attempt.current?.payload !== payload) attempt.current = { payload, operation: crypto.randomUUID() };
    setPending({ action, revision: null });
    try {
      const response = object(await request("/api/tasks/lifecycle", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ task: task.id, generation: task.generation, revision: task.queue_revision, action, operation: attempt.current.operation }) }));
      if (typeof response.revision !== "number") throw new Error("The board did not confirm this action; refresh before retrying.");
      setPending({ action, revision: response.revision });
      attempt.current = null;
    } catch (error: unknown) { setProblem(message(error)); setPending(null); }
  };
  if (task.archived || task.phase === "stopped") return null;
  return <>
    <div className="task-controls" role="group" aria-label={"Controls for " + (task.title || task.id)}>
      {leading}
      {isQueued && start && !queueBlock(task) && <button className="icon-button raised" aria-label={"Start " + task.title} data-tip={start.blocked || "Start"} aria-disabled={!!start.blocked || isChanging} onClick={(event) => { if (!isChanging) start.onStart(event.currentTarget); }}><Icon name="play" /></button>}
      {isQueued && <button className="icon-button raised" aria-label={"Adjust " + task.title} data-tip="Adjust" disabled={isChanging} onClick={(event) => onAdjust(event.currentTarget)}><Icon name="edit" /></button>}
      {canPause && <button className="icon-button raised" aria-label={"Pause " + task.title} data-tip="Pause" disabled={isChanging} onClick={() => void act("pause")}><Icon name="pause" /></button>}
      {canResume && <button className="icon-button raised" aria-label={"Resume " + task.title} data-tip={resumeBlock || "Resume"} aria-disabled={!!resumeBlock || isChanging} onClick={() => { if (!resumeBlock && !isChanging) void act("resume"); }}><Icon name="play" /></button>}
      <button className="icon-button raised danger" aria-label={"Stop " + task.title} data-tip="Stop" data-tip-align="end" disabled={isChanging} onClick={() => setConfirmation({ generation: task.generation, revision: task.queue_revision })}><Icon name="trash" /></button>
    </div>
    {isChanging && <p className="task-action-progress" role="status">{pending ? { pause: "Pausing", resume: "Resuming", stop: "Stopping" }[pending.action] : task.starting ? "Starting" : task.phase === "pausing" ? "Pausing" : task.phase === "resuming" ? "Resuming" : "Stopping"}...</p>}
    {(problem || task.action_error || start?.problem) && <p className="task-action-problem" role="alert">{problem || task.action_error || start?.problem}</p>}
    {confirmation && <StopTaskDialog task={task} canPause={canPause} onPause={() => void act("pause")} onStop={() => void act("stop")} onClose={() => setConfirmation(null)} />}
  </>;
}
