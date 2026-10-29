import { useId } from "react";
import { Icon } from "./Icon";
import "./afk.css";

// SettingSwitch is a setting's switch, drawn as the AFK switch in the CFO
// panel's header is, the board's one look for a switch, with a spinner while
// the supervisor answers. Beside it a mark says how it stands: a green check
// when it works, an amber triangle when something is wrong, which names what
// to assistive technology. Under it, at most a few words for what is missing,
// or what is wrong when there are none.
export function SettingSwitch({ label, on, pending, disabled, mark, markTip, note, onPress }: { label: string; on: boolean; pending: boolean; disabled: boolean; mark?: "ok" | "problem"; markTip?: string; note?: string; onPress: () => void }) {
  const noteId = useId();
  const said = note || (mark === "problem" ? markTip : "");
  return <div className="setting-switch-row">
    <div className="setting-switch-line">
      <button className="afk-toggle setting-switch" role="switch" aria-checked={on} aria-label={label} aria-describedby={said ? noteId : undefined} aria-busy={pending} disabled={disabled} onClick={onPress}>
        <span className="afk-toggle-label">{label}</span>
        {pending && <span className="card-start-spinner" aria-hidden="true" />}
        <span className="afk-toggle-track" aria-hidden="true"><span className="afk-toggle-thumb" /></span>
      </button>
      {mark && <span className={"setting-switch-mark " + mark} role="img" aria-label={mark === "ok" ? label + " works" : markTip || label + " needs attention"}><Icon name={mark === "ok" ? "check" : "warning"} /></span>}
    </div>
    {said && <small className="setting-switch-note" id={noteId}>{said}</small>}
  </div>;
}
