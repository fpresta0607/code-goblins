import { useState, type ReactNode } from "react";
import { AfkOnDialog } from "./afk-on-dialog";
import { AfkRefusal } from "./afk-refusal";
import { onThisMachine } from "./credentials";
import { useAfkSwitch } from "./useAfkSwitch";
import type { Afk } from "./types";
import "./afk.css";

// The AFK toggle in the CFO panel's one header row: a small
// switch with its label, which spins while the supervisor answers. Only the
// Overlord turns it, which the supervisor proves of the program that asks,
// and a refusal is one short sentence on a row of its own under the header
// until he closes it. Turning it on asks first. Turning it off needs no question:
// it only gives him his decisions back, and it is how a switch that cannot be
// read is reset, which a muted line beside it says while it cannot be read.
// On his private board it turns off and never on, with no words for why (CFO
// decision 1008966): on hands the CFO his authority, so it stays on the PC.
// His last report reopens from the CFO's Task view, not from here (the
// Overlord, 2026-10-29: the last AFK report "could be something that you know
// is part of like the task panel").
// leading is a control shown before them, on the same row at every width.
export function AfkToggle({ afk, instance, leading }: { afk: Afk; instance: string; leading?: ReactNode }) {
  const [asking, setAsking] = useState(false);
  const { pending, problem, pressedOn, turn, clear } = useAfkSwitch(instance);
  const on = afk.state === "on";
  const canTurnOn = onThisMachine(window.location.hostname);
  const turnOn = async () => { if (await turn(true)) setAsking(false); };
  const press = () => { if (afk.state === "off") { clear(); setAsking(true); } else void turn(false); };
  return <>
    <div className="afk-header">
      {leading}
      <button className="afk-toggle" role="switch" aria-checked={on} aria-label="AFK mode" aria-busy={pending} disabled={pending || (afk.state === "off" && !canTurnOn)} onClick={press}>
        <span className="afk-toggle-label">AFK</span>
        {pending && <span className="card-start-spinner" aria-hidden="true" />}
        <span className="afk-toggle-track" aria-hidden="true"><span className="afk-toggle-thumb" /></span>
      </button>
      {afk.state === "unreadable" && <span className="muted">Unreadable: a press resets it</span>}
    </div>
    {!asking && <AfkRefusal on={pressedOn} problem={problem} onClose={clear} />}
    {asking && <AfkOnDialog pending={pending} problem={problem} onTurnOn={() => void turnOn()} onClose={() => { setAsking(false); clear(); }} />}
  </>;
}
