import { useEffect, useId, useRef } from "react";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";

// The confirmation before the board restarts the CFO, as goblins resume does:
// its terminal closes, which interrupts what it is doing, and it starts again
// there on the same conversation.
export function RestartCfoDialog({ onRestart, onClose }: { onRestart: () => void; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId(), description = useId();
  useEffect(() => {
    const element = dialog.current, source = document.activeElement;
    element?.showModal();
    element?.querySelector<HTMLButtonElement>(".primary")?.focus();
    return () => { element?.close(); if (source instanceof HTMLElement && source.isConnected) source.focus(); };
  }, []);
  return <dialog ref={dialog} className="stop-task-dialog" aria-labelledby={title} aria-describedby={description} onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onClose(); }}>
    <Avatar persona="cfo" />
    <h2 id={title}>Restart the CFO?</h2>
    <p id={description}>Its terminal closes, which interrupts what it is doing now, and it starts again there on the same conversation. Goblins and the board keep running.</p>
    <p className="preservation-notice"><Icon name="shield" />Its conversation is kept.</p>
    <div className="stop-task-choices">
      <button className="primary" autoFocus onClick={onRestart}>Restart the CFO</button>
      <button onClick={onClose}>Cancel</button>
    </div>
    <p className="muted">For a CFO whose screen froze while it kept working.</p>
  </dialog>;
}
