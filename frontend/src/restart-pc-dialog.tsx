import { useEffect, useId, useRef } from "react";
import { Avatar } from "./Avatar";

// The one question before Restart PC, since a restart ends every program on
// the PC, his own among them: what the restart does, in the order it does
// it. Cancel has the focus, so Enter alone restarts nothing, and Restart PC
// says it is working while the supervisor answers.
export function RestartPcDialog({ pools, line, isPending, onRestart, onClose }: {
  pools: string; line: string; isPending: boolean; onRestart: () => void; onClose: () => void;
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
  return <dialog ref={dialog} className="stop-task-dialog restart-pc-dialog" aria-labelledby={title} aria-describedby={description} onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onClose(); }}>
    <Avatar persona="cfo" />
    <h2 id={title}>Restart the PC?</h2>
    <p id={description}>The kernel's pools hold {pools} GB, past the {line} GB line where free memory can no longer hold the fleet. A restart frees them.</p>
    <ol className="restart-pc-steps">
      <li>The CFO is told first.</li>
      <li>Every working goblin writes its handoff and pauses until the PC has restarted.</li>
      <li>The heavy runs end, then Windows restarts. An app with unsaved work asks you first.</li>
      <li>After you sign in, the CFO and the goblins come back by themselves.</li>
    </ol>
    <div className="stop-task-choices">
      <button className="primary" disabled={isPending} aria-busy={isPending} onClick={onRestart}>{isPending ? <><span className="card-start-spinner" aria-hidden="true" />Restarting the PC…</> : "Restart PC"}</button>
      <button onClick={onClose}>Cancel</button>
    </div>
  </dialog>;
}
