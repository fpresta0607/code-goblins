import { useState } from "react";
import { Icon } from "./Icon";
import { UNREADABLE } from "./afk";
import { useAfkActions } from "./afk-actions";
import { AfkOnDialog } from "./afk-on-dialog";
import { useAfkSwitch } from "./useAfkSwitch";
import type { Afk } from "./types";
import "./afk.css";

// The AFK switch on the CFO's bar: its label and a pill switch, Off or On.
// Only the Overlord turns it, which the supervisor proves of the program that
// asks, so a refusal is said under the switch in the supervisor's words. On
// asks first. Off needs no question: it only gives him his decisions back, and
// it is how a switch that cannot be read is reset. While the switch cannot be
// read neither side is pressed. While it is off, the report of the last
// stretch opens from the button beside it. pixel draws that button for the
// CFO's dialogue box.
export function AfkToggle({ afk, instance, pixel = false }: { afk: Afk; instance: string; pixel?: boolean }) {
  const [asking, setAsking] = useState(false);
  const { pending, problem, turn, clear } = useAfkSwitch(instance);
  const { openReport } = useAfkActions();
  const turnOn = async () => { if (await turn(true)) setAsking(false); };
  return <>
    <div className="afk-controls">
      {afk.state === "off" && afk.report && <button className={"icon-button " + (pixel ? "pixel-icon" : "raised")} aria-label="Open the last AFK report" data-tip="Last AFK report" onClick={openReport}><Icon name="file" /></button>}
      <div className="afk-switch" role="group" aria-label="AFK mode">
        <span className="afk-switch-label" aria-hidden="true">AFK</span>
        <div className="afk-switch-sides">
          <button aria-label="AFK off" aria-pressed={afk.state === "off"} disabled={pending} onClick={() => { if (afk.state !== "off") void turn(false); }}>Off</button>
          <button className="afk-on" aria-label="AFK on" aria-pressed={afk.state === "on"} disabled={pending} onClick={() => { if (afk.state !== "on") { clear(); setAsking(true); } }}>On</button>
        </div>
      </div>
    </div>
    {afk.state === "unreadable" && !problem && <p className="afk-problem" role="status">{UNREADABLE}</p>}
    {problem && !asking && <p className="afk-problem" role="alert">{problem}</p>}
    {asking && <AfkOnDialog pending={pending} problem={problem} onTurnOn={() => void turnOn()} onClose={() => { setAsking(false); clear(); }} />}
  </>;
}
