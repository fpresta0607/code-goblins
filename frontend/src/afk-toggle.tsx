import { useState } from "react";
import { AfkOnDialog } from "./afk-on-dialog";
import { AfkRefusal } from "./afk-refusal";
import { useAfkSwitch } from "./useAfkSwitch";
import type { Afk } from "./types";
import "./afk.css";

// The AFK toggle in the CFO panel's one header row: a small
// switch with its label, whose track spins in place of its thumb while the
// supervisor answers, so the switch keeps its width in the row. Only the
// Overlord turns it, which the supervisor proves of the program that asks,
// and a refusal is one short sentence under the row's end, over the
// conversation, until he closes it. Turning it on asks first. Turning it off
// needs no question: it only gives him his decisions back, and it is how a
// switch that cannot be read is reset, which a muted line under the row says
// while it cannot be read.
// It turns on and off on his private board as on the PC: his decision of
// 2026-10-29, knowing the private board is proven his only by headers a
// program on the PC can forge.
// His last report reopens from the CFO's Task view, not from here (the
// Overlord, 2026-10-29: the last AFK report "could be something that you know
// is part of like the task panel").
export function AfkToggle({ afk, instance }: { afk: Afk; instance: string }) {
  const [asking, setAsking] = useState(false);
  const { pending, problem, pressedOn, turn, clear } = useAfkSwitch(instance);
  const on = afk.state === "on";
  const turnOn = async () => { if (await turn(true)) setAsking(false); };
  const press = () => { if (afk.state === "off") { clear(); setAsking(true); } else void turn(false); };
  return <>
    <div className="afk-header">
      <button className="afk-toggle" role="switch" aria-checked={on} aria-label="AFK mode" aria-busy={pending} disabled={pending} onClick={press}>
        <span className="afk-toggle-label">AFK</span>
        <span className="afk-toggle-track" aria-hidden="true">{pending ? <span className="card-start-spinner" /> : <span className="afk-toggle-thumb" />}</span>
      </button>
    </div>
    {!asking && <AfkRefusal on={pressedOn} problem={problem} onClose={clear} />}
    {asking && <AfkOnDialog pending={pending} problem={problem} onTurnOn={() => void turnOn()} onClose={() => { setAsking(false); clear(); }} />}
  </>;
}
