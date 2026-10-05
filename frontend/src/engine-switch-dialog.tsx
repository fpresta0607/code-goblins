import { useEffect, useId, useRef } from "react";
import type { Task } from "./types";
import type { EngineSelection } from "./engine-catalog";
import { Icon } from "./Icon";
import { harnessName } from "./workflow";

export function EngineSwitchDialog({ task, choice, onSwitch, onClose }: { task: Task; choice: EngineSelection; onSwitch: (when: "turn-end" | "now") => void; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId(), description = useId();
  useEffect(() => {
    const element = dialog.current, source = document.activeElement;
    element?.showModal();
    element?.querySelector<HTMLButtonElement>(".primary")?.focus();
    return () => { element?.close(); if (source instanceof HTMLElement && source.isConnected) source.focus(); };
  }, []);
  return <dialog ref={dialog} className="stop-task-dialog engine-switch-dialog" aria-labelledby={title} aria-describedby={description} onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onClose(); }}>
    <h2 id={title}>Switch this task's engine?</h2>
    <p className="muted">{task.title || task.id}</p>
    <p>{harnessName(choice.harness)} · {choice.model} {choice.effort}</p>
    <p id={description}>The old terminal closes and the session is handed over. The task, worktree and branch stay.</p>
    <p className="preservation-notice"><Icon name="shield" />Uncommitted work stays.</p>
    <div className="stop-task-choices">
      <button className="primary" autoFocus onClick={() => onSwitch("turn-end")}>Switch when its turn ends</button>
      <button className="danger" onClick={() => onSwitch("now")}>Switch now</button>
      <button onClick={onClose}>Cancel</button>
    </div>
    <p className="muted">Turn end waits for an idle session with no gate step running. Switch now interrupts its turn and any running gate step.</p>
  </dialog>;
}
