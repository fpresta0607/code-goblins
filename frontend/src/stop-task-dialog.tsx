import { useEffect, useId, useRef } from "react";
import type { Task } from "./types";
import { Avatar } from "./Avatar";
import { personaFor } from "./workflow";
import { Icon } from "./Icon";

// The confirmation before a task ends: a task that has not started is removed
// from the queue, and one that has is stopped, with Pause offered first.
export function StopTaskDialog({ task, canPause, onPause, onStop, onClose }: {
  task: Task; canPause: boolean; onPause: () => void; onStop: () => void; onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId(), description = useId();
  const isQueued = task.phase === "queued";
  useEffect(() => {
    const element = dialog.current;
    const source = document.activeElement;
    element?.showModal();
    element?.querySelector<HTMLButtonElement>(canPause ? ".primary" : ".stop-task-choices button:last-child")?.focus();
    return () => { element?.close(); if (source instanceof HTMLElement && source.isConnected) source.focus(); };
  }, [canPause]);
  return <dialog ref={dialog} className="stop-task-dialog" aria-labelledby={title} aria-describedby={description} onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onClose(); }}>
    <Avatar persona={personaFor(task)} />
    <h2 id={title}>{isQueued ? "Remove this task?" : "Stop this task?"}</h2>
    <p className="muted">{task.title || task.id}</p>
    <p id={description}>{isQueued ? "It leaves the queue and will not start." : "End this session and every process it started. Remove the worktree when its work is safe."}</p>
    <p className="preservation-notice"><Icon name="shield" />{isQueued ? "Its brief is kept." : "Pushed branches stay. Unpushed and uncommitted work is kept."}</p>
    <div className="stop-task-choices">
      {canPause && <button className="primary" autoFocus onClick={onPause}>Pause instead (Recommended)</button>}
      <button className="danger" onClick={onStop}>{isQueued ? "Remove from queue" : "Stop and delete"}</button>
      <button autoFocus={!canPause} onClick={onClose}>Cancel</button>
    </div>
    {canPause && <p className="muted">Pause keeps the task ready to resume.</p>}
  </dialog>;
}
