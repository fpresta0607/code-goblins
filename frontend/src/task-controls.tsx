import { useRef, useState, type ReactNode } from "react";
import { object, type Snapshot, type Task } from "./types";
import type { CardStart } from "./TaskCard";
import { message, reportToCfo, request } from "./api";
import { queueBlock } from "./start";
import { Icon } from "./Icon";
import { StopTaskDialog } from "./stop-task-dialog";
import { withoutHarness } from "./task-words";
import { harnessName } from "./workflow";
import { recordClick } from "./use-task-clicks";

// A task's controls: icons on its card, where start starts a queued task and
// onAdjust opens its panel, and labelled buttons in its panel's action row,
// which leaves both to the card. A queued task has not started, so it is
// removed rather than stopped. While an update of a live goblin's harness
// waits, Update restarts it onto it, on its own conversation, at the end of
// its turn; pressed again before then it takes the press back. Update looks
// like the other controls and shows only while an update waits.
// trailing is a control shown last in the group, such as a card's terminal
// button, so one that shows only on hover never leaves a gap before the rest.
// One click does it: the task's status says Pausing, Resuming or Stopping in
// the frame he clicks, a Resume waits its turn on the supervisor rather than
// being refused, and a click the supervisor refuses goes to the CFO, never
// to a line on the board (the Overlord, 2026-10-08, "i hate yellow text line
// display").
export function TaskControls({ task, snapshot, start, trailing, labelled = false, onAdjust }: {
  task: Task; snapshot: Snapshot; start?: CardStart; trailing?: ReactNode; labelled?: boolean; onAdjust?: (source: HTMLElement) => void;
}) {
  const [confirmation, setConfirmation] = useState<{ generation: string; revision: string } | null>(null);
  const attempt = useRef<{ payload: string; operation: string } | null>(null);
  if (confirmation && (confirmation.generation !== task.generation || confirmation.revision !== task.queue_revision)) setConfirmation(null);
  const isChanging = task.starting || task.switching || ["pausing", "stopping", "resuming"].includes(task.phase);
  const isQueued = task.phase === "queued";
  const isResumeRetry = task.lifecycle?.action === "resume" && ["failed", "resuming"].includes(task.lifecycle.phase);
  const canResume = task.phase === "paused" || isResumeRetry;
  const canPause = !!task.generation && !canResume && !task.archived;
  const isUpdatePending = task.pending_engine?.when === "update";
  const canUpdate = isUpdatePending || !!task.harness_update && !canResume && !task.archived && !isQueued;
  const update = async () => {
    if (isChanging) return;
    try {
      await request("/api/tasks/engine", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ task: task.id, generation: task.generation, when: isUpdatePending ? "cancel" : "update" }) });
    } catch (error: unknown) { reportToCfo("the harness update of " + task.id, message(error)); }
  };
  const act = async (action: "pause" | "resume" | "stop") => {
    if (isChanging) return;
    setConfirmation(null);
    const payload = JSON.stringify({ task: task.id, generation: task.generation, revision: task.queue_revision, action });
    if (attempt.current?.payload !== payload) attempt.current = { payload, operation: crypto.randomUUID() };
    recordClick(task.id, { action, revision: null });
    try {
      const response = object(await request("/api/tasks/lifecycle", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ task: task.id, generation: task.generation, revision: task.queue_revision, action, operation: attempt.current.operation }) }));
      if (typeof response.revision !== "number") throw new Error("The board did not confirm this action.");
      recordClick(task.id, { action, revision: response.revision });
      attempt.current = null;
    } catch (error: unknown) {
      recordClick(task.id, null);
      reportToCfo("the " + action + " of " + task.id, message(error));
    }
  };
  if (task.archived || task.phase === "stopped") return null;
  const name = withoutHarness(task.title) || task.id;
  const end = isQueued ? "Remove" : "Stop";
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
      {isQueued && start && !queueBlock(task) && <button {...face("Start", "", start.blocked)} aria-disabled={!!start.blocked || isChanging} onClick={() => { if (!isChanging) start.onStart(); }}><Icon name="play" />{text("Start")}</button>}
      {isQueued && onAdjust && <button {...face("Adjust")} disabled={isChanging} onClick={(event) => onAdjust(event.currentTarget)}><Icon name="edit" /></button>}
      {canUpdate && <button className={labelled ? "labelled-button" : "icon-button raised"} aria-label={(isUpdatePending ? "Cancel the update of " : "Update ") + name} data-tip={isUpdatePending ? "Take the update back" : harnessName(task.harness) + " was updated. Restart this goblin onto it at its next stopping point. Its conversation is kept."} data-tip-align="start" disabled={isChanging} onClick={() => void update()}><Icon name={isUpdatePending ? "clock" : "download"} />{text(isUpdatePending ? "Cancel update" : "Update")}</button>}
      {canPause && <button {...face("Pause")} disabled={isChanging} onClick={() => void act("pause")}><Icon name="pause" />{text("Pause")}</button>}
      {canResume && <button {...face("Resume")} aria-disabled={isChanging} onClick={() => { if (!isChanging) void act("resume"); }}><Icon name="play" />{text("Resume")}</button>}
      <button {...face(end, " danger")} disabled={isChanging} onClick={() => setConfirmation({ generation: task.generation, revision: task.queue_revision })}><Icon name="trash" />{text(end)}</button>
      {trailing}
    </div>
    {confirmation && <StopTaskDialog task={task} canPause={canPause} onPause={() => void act("pause")} onStop={() => void act("stop")} onClose={() => setConfirmation(null)} />}
  </>;
}
