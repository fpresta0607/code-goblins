import { useEffect, useId, useRef } from "react";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import "./afk.css";

// The question before AFK mode turns on, since it hands the CFO the Overlord's
// authority until he turns it off. Cancel has the focus, so Enter alone turns
// nothing on.
export function AfkOnDialog({ pending, problem, onTurnOn, onClose }: {
  pending: boolean; problem: string; onTurnOn: () => void; onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId(), description = useId();
  useEffect(() => {
    const element = dialog.current;
    const source = document.activeElement;
    element?.showModal();
    element?.querySelector<HTMLButtonElement>(".stop-task-choices button:last-child")?.focus();
    return () => { element?.close(); if (source instanceof HTMLElement && source.isConnected) source.focus(); };
  }, []);
  return <dialog ref={dialog} className="stop-task-dialog afk-dialog" aria-labelledby={title} aria-describedby={description} onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onClose(); }}>
    <Avatar persona="cfo" />
    <h2 id={title}>Go AFK?</h2>
    <p id={description}>The CFO runs the fleet under your authority until you turn AFK off, and logs every decision it makes. Nothing on the board prompts you meanwhile.</p>
    <p className="preservation-notice"><Icon name="shield" />What is yours alone is held for you, never decided.</p>
    {problem && <p className="task-action-problem" role="alert">{problem}</p>}
    <div className="stop-task-choices">
      <button className="primary" disabled={pending} onClick={onTurnOn}>Turn AFK on</button>
      <button onClick={onClose}>Cancel</button>
    </div>
  </dialog>;
}
