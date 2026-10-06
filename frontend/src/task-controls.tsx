import { useRef, useState, type ReactNode } from "react";
import { object, type Snapshot, type Task } from "./types";
import type { CardStart } from "./TaskCard";
import { message, request } from "./api";
import { diskBlock, memoryBlock, queueBlock } from "./start";
import { Icon } from "./Icon";
import { StopTaskDialog } from "./stop-task-dialog";
import { plainText, withoutHarness } from "./task-words";

// A task's controls: icons on its card, where start starts a queued task and
// onAdjust opens its panel, and labelled buttons in its panel's action row,
// which leaves both to the card. A queued task has not started, so it is
// removed rather than stopped.
// leading is a control shown first in the group, such as a card's terminal button.
export function TaskControls({ task, snapshot, start, leading, labelled = false, onAdjust }: {
  task: Task; snapshot: Snapshot; start?: CardStart; leading?: ReactNode; labelled?: boolean; onAdjust?: (source: HTMLElement) => void;
}) {
  const [confirmation, setConfirmation] = useState<{ generation: string; revision: string } | null>(null);
  const [pending, setPending] = useState<{ action: string; revision: number | null } | null>(null);
  const [problem, setProblem] = useState("");
  const attempt = useRef<{ payload: string; operation: string } | null>(null);
  if (pending?.revision != null && snapshot.revision >= pending.revision) setPending(null);
  if (confirmation && (confirmation.generation !== task.generation || confirmation.revision !== task.queue_revision)) setConfirmation(null);
  const isChanging = !!pending || task.starting || task.switching || ["pausing", "stopping", "resuming"].includes(task.phase);
  const isQueued = task.phase === "queued";
  const isResumeRetry = task.lifecycle?.action === "resume" && ["failed", "resuming"].includes(task.lifecycle.phase);
  const canResume = task.phase === "paused" || isResumeRetry;
  const canPause = !!task.generation && !canResume && !task.archived;
  const resumeNeeds = memoryBlock(snapshot.memory) || diskBlock(snapshot.disk);
  const resumeBlock = !isResumeRetry && resumeNeeds ? "Resume needs " + resumeNeeds : "";
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
  const problemText = plainText(problem || task.action_error || start?.problem || "");
  const name = withoutHarness(task.title) || task.id;
  const end = isQueued ? "Remove" : "Stop";
  const ending = isQueued ? "Removing" : "Stopping";
  // A labelled button says its own name, so its tip only says why it cannot
  // run; an icon's tip is its name until then.
  const face = (action: string, tone = "", block = "") => ({
    className: (labelled ? "labelled-button" : "icon-button raised") + tone,
    "aria-label": action + " " + name,
    ...(!labelled ? { "data-tip": block || action } : block ? { "data-tip": block, "data-tip-align": "start" } : {}),
  });
  const text = (action: string) => labelled && <span>{action}</span>;
  return <>
    <div className="task-controls" role="group" aria-label={"Controls for " + name}>
      {leading}
      {isQueued && start && !queueBlock(task) && <button {...face("Start", "", start.blocked)} aria-disabled={!!start.blocked || isChanging} onClick={(event) => { if (!isChanging) start.onStart(event.currentTarget); }}><Icon name="play" />{text("Start")}</button>}
      {isQueued && onAdjust && <button {...face("Adjust")} disabled={isChanging} onClick={(event) => onAdjust(event.currentTarget)}><Icon name="edit" /></button>}
      {canPause && <button {...face("Pause")} disabled={isChanging} onClick={() => void act("pause")}><Icon name="pause" />{text("Pause")}</button>}
      {canResume && <button {...face("Resume", "", resumeBlock)} aria-disabled={!!resumeBlock || isChanging} onClick={() => { if (!resumeBlock && !isChanging) void act("resume"); }}><Icon name="play" />{text("Resume")}</button>}
      <button {...face(end, " danger")} disabled={isChanging} onClick={() => setConfirmation({ generation: task.generation, revision: task.queue_revision })}><Icon name="trash" />{text(end)}</button>
    </div>
    {(isChanging || problemText) && <div className="task-notes">
      {isChanging && <p className="task-action-progress" role="status">{pending ? { pause: "Pausing", resume: "Resuming", stop: ending }[pending.action] : task.switching ? "Switching engine" : task.starting ? "Starting" : task.phase === "pausing" ? "Pausing" : task.phase === "resuming" ? "Resuming" : ending}...</p>}
      {problemText && <p className="task-action-problem" role="alert">{problemText}</p>}
    </div>}
    {confirmation && <StopTaskDialog task={task} canPause={canPause} onPause={() => void act("pause")} onStop={() => void act("stop")} onClose={() => setConfirmation(null)} />}
  </>;
}
