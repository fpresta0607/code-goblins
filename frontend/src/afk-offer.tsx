import { useEffect, useId, useRef } from "react";
import { Avatar } from "./Avatar";
import { afkTime, stillHeld, switchedBy, type Occasion } from "./afk";
import type { Afk } from "./types";
import "./afk.css";

// What the board offers the moment the Overlord is back while AFK mode is on,
// or at his first click or key after the CFO turned it on at his ask, which
// occasion names: turn it off, which shows the report, or stay away. It opens
// on his own click or key, never by itself. The key that opened it may be the first of
// many he is typing, so the focus goes to the dialog and to neither button:
// what he types next presses nothing, and Escape stays away.
export function AfkOffer({ afk, occasion, now, pending, problem, onTurnOff, onStay }: {
  afk: Afk; occasion: Occasion; now: number; pending: boolean; problem: string; onTurnOff: () => void; onStay: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId(), description = useId();
  useEffect(() => {
    const element = dialog.current;
    const source = document.activeElement;
    element?.showModal();
    element?.focus();
    return () => { element?.close(); if (source instanceof HTMLElement && source.isConnected) source.focus(); };
  }, []);
  const held = stillHeld(afk).length;
  const who = switchedBy(afk.from, afk.asked);
  return <dialog ref={dialog} tabIndex={-1} className="stop-task-dialog afk-dialog" aria-labelledby={title} aria-describedby={description} onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onStay(); }}>
    <Avatar persona="cfo" />
    <h2 id={title}>{occasion === "asked" ? "The CFO turned AFK on" : "Welcome back"}</h2>
    <p id={description}>AFK has been on since {afkTime(afk.since, now)}{who && ", turned on " + who}. The CFO decided {afk.decided} and holds {held} for you.</p>
    {problem && <p className="task-action-problem" role="alert">{problem}</p>}
    <div className="stop-task-choices">
      <button className="primary" disabled={pending} onClick={onTurnOff}>Turn AFK off</button>
      <button onClick={onStay}>Stay AFK</button>
    </div>
    <p className="muted">Turning it off shows the report.</p>
  </dialog>;
}
