import { useEffect, useId, useRef } from "react";
import { Avatar } from "./Avatar";
import { afkTime, switchedBy, type Occasion } from "./afk";
import type { Afk } from "./types";
import { AfkRefusal } from "./afk-refusal";
import "./afk.css";

// What the board offers the moment the Overlord is back while AFK mode is on,
// or at his first click or key after the CFO turned it on at his ask, which
// occasion names. Back after he was gone, the main button turns it off, which
// shows the report, and the lesser one stays away. Right after the CFO turned
// it on at his ask he has just asked for it, so the main button keeps it on
// and closes, turning it off is the lesser one, and one short line says the
// CFO did it at his ask ("not right button for this kind of turn on", the
// Overlord, 2026-10-09). It opens on his own click or key, never by itself.
// The key that opened it may be the first of many he is typing, so the focus
// goes to the dialog and to neither button: what he types next presses
// nothing, and Escape stays away. Turn AFK off says it is working while the
// supervisor answers, and a refusal is one short sentence above the buttons.
// How much the CFO decided is said once it decided something.
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
  const asked = occasion === "asked";
  const who = switchedBy(afk.from, afk.asked);
  const since = asked ? "It did so at your ask, at " + afkTime(afk.since, now) + "." : "AFK has been on since " + afkTime(afk.since, now) + (who && ", turned on " + who) + ".";
  const turnOff = <button className={asked ? undefined : "primary"} disabled={pending} aria-busy={pending} onClick={onTurnOff}>{pending ? <><span className="card-start-spinner" aria-hidden="true" />Turning AFK off…</> : "Turn AFK off"}</button>;
  return <dialog ref={dialog} tabIndex={-1} className="stop-task-dialog afk-dialog" aria-labelledby={title} aria-describedby={description} onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onStay(); }}>
    <Avatar persona="cfo" />
    <h2 id={title}>{asked ? "The CFO turned AFK on" : "Welcome back"}</h2>
    <p id={description}>{since}{afk.decided > 0 && " The CFO decided " + afk.decided + "."}</p>
    <AfkRefusal on={false} problem={problem} />
    <div className="stop-task-choices">
      {asked ? <><button className="primary" onClick={onStay}>Got it</button>{turnOff}</> : <>{turnOff}<button onClick={onStay}>Stay AFK</button></>}
    </div>
    {!asked && <p className="muted">Turning it off shows the report.</p>}
  </dialog>;
}
