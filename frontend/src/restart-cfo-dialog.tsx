import { useEffect, useId, useRef, useState } from "react";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";

// The confirmation before the board restarts the CFO, as goblins resume does:
// its terminal closes, which interrupts what it is doing, and it starts again
// there, on the same conversation where it can when the harness stays the same.
export function RestartCfoDialog({ currentHarness, onRestart, onClose }: { currentHarness: string; onRestart: (harness: string) => void; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [harness, setHarness] = useState(currentHarness);
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
    <p id={description}>Its terminal closes, which interrupts what it is doing now, and it starts again in the chosen harness. Goblins and the board keep running.</p>
    <label className="restart-cfo-harness">Harness
      <select value={harness} onChange={(event) => setHarness(event.target.value)}>
        <option value="claude">Claude Code</option>
        <option value="codex">Codex</option>
        <option value="pi">pi</option>
      </select>
    </label>
    <p aria-live="polite">{harness === currentHarness && harness !== "pi" ? "Resumes its current conversation where it can." : "Starts a new conversation with this home's saved context."}</p>
    <p className="preservation-notice"><Icon name="shield" />Its conversation is kept.</p>
    <div className="stop-task-choices">
      <button className="primary" autoFocus onClick={() => onRestart(harness)}>Restart the CFO</button>
      <button onClick={onClose}>Cancel</button>
    </div>
  </dialog>;
}
