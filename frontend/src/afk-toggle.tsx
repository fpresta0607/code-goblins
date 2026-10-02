import { useState } from "react";
import { Icon } from "./Icon";
import { UNREADABLE } from "./afk";
import { useAfkActions } from "./afk-actions";
import { AfkOnDialog } from "./afk-on-dialog";
import { useAfkSwitch } from "./useAfkSwitch";
import type { Afk } from "./types";
import "./afk.css";

// The AFK toggle in the CFO panel's header, beside the CFO's status: a small
// switch with its label. Only the Overlord turns it, which the supervisor
// proves of the program that asks, so a refusal is said under the header in
// the supervisor's words. Turning it on asks first. Turning it off needs no
// question: it only gives him his decisions back, and it is how a switch that
// cannot be read is reset, which the header says while it cannot be read.
// While it is off, the report of the last stretch opens from the button
// beside it.
export function AfkToggle({ afk, instance }: { afk: Afk; instance: string }) {
  const [asking, setAsking] = useState(false);
  const { pending, problem, turn, clear } = useAfkSwitch(instance);
  const { openReport } = useAfkActions();
  const on = afk.state === "on";
  const turnOn = async () => { if (await turn(true)) setAsking(false); };
  const press = () => { if (afk.state === "off") { clear(); setAsking(true); } else void turn(false); };
  return <>
    <div className="afk-header">
      {afk.state === "off" && afk.report && <button className="icon-button raised" aria-label="Open the last AFK report" data-tip="Last AFK report" data-tip-align="end" onClick={openReport}><Icon name="file" /></button>}
      <button className="afk-toggle" role="switch" aria-checked={on} aria-label="AFK mode" data-tip={on ? "Turn AFK off" : afk.state === "off" ? "Turn AFK on" : "Reset AFK to off"} data-tip-align="end" disabled={pending} onClick={press}>
        <span className="afk-toggle-label">AFK</span>
        <span className="afk-toggle-track" aria-hidden="true"><span className="afk-toggle-thumb" /></span>
      </button>
    </div>
    {afk.state === "unreadable" && !problem && <p className="afk-problem" role="status">{UNREADABLE}</p>}
    {problem && !asking && <p className="afk-problem" role="alert">{problem}</p>}
    {asking && <AfkOnDialog pending={pending} problem={problem} onTurnOn={() => void turnOn()} onClose={() => { setAsking(false); clear(); }} />}
  </>;
}
