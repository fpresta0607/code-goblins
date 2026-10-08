import { useState, type ReactNode } from "react";
import { Icon } from "./Icon";
import { useAfkActions } from "./afk-actions";
import { AfkOnDialog } from "./afk-on-dialog";
import { useAfkSwitch } from "./useAfkSwitch";
import type { Afk } from "./types";
import "./afk.css";
import { ClickFeedback } from "./click-feedback";

// The AFK toggle in the CFO panel's header, beside the CFO's status: a small
// switch with its label. Only the Overlord turns it, which the supervisor
// proves of the program that asks, and a refusal says so in a few words for
// a moment. Turning it on asks first. Turning it off needs no question:
// it only gives him his decisions back, and it is how a switch that cannot be
// read is reset, which its tip says while it cannot be read.
// While it is off, the report of the last stretch opens from the button
// beside it. leading is a control shown before them, on the same row at
// every width.
export function AfkToggle({ afk, instance, leading }: { afk: Afk; instance: string; leading?: ReactNode }) {
  const [asking, setAsking] = useState(false);
  const { pending, problem, turn, clear } = useAfkSwitch(instance);
  const { openReport, turnedOff } = useAfkActions();
  const on = afk.state === "on";
  const turnOn = async () => { if (await turn(true)) setAsking(false); };
  // A switch that cannot be read is reset to off and keeps no report.
  const turnOff = async () => { if (await turn(false) && on) turnedOff(); };
  const press = () => { if (afk.state === "off") { clear(); setAsking(true); } else void turnOff(); };
  return <>
    <div className="afk-header">
      {leading}
      {afk.state === "off" && afk.report && <button className="icon-button raised" aria-label="Open the last AFK report" data-tip="Last AFK report" data-tip-align="end" onClick={openReport}><Icon name="file" /></button>}
      <button className="afk-toggle" role="switch" aria-checked={on} aria-label="AFK mode" data-tip={on ? "Turn AFK off" : afk.state === "off" ? "Turn AFK on" : "Reset AFK to off"} data-tip-align="end" disabled={pending} onClick={press}>
        <span className="afk-toggle-label">AFK</span>
        <span className="afk-toggle-track" aria-hidden="true"><span className="afk-toggle-thumb" /></span>
      </button>
    </div>
    {!asking && <ClickFeedback text={problem} />}
    {asking && <AfkOnDialog pending={pending} problem={problem} onTurnOn={() => void turnOn()} onClose={() => { setAsking(false); clear(); }} />}
  </>;
}
