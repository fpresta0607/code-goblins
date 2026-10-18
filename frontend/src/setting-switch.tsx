import { useId } from "react";
import { Icon } from "./Icon";
import "./afk.css";

// SettingSwitch is a setting's switch, drawn as the AFK switch in the CFO
// panel's header is, the board's one look for a switch, with a spinner while
// the supervisor answers. Beside it a mark says how it stands: a green check
// when it works, an amber triangle when something is wrong, whose tip says
// what and which the keyboard reaches. Under it, at most a few words for what
// is missing.
export function SettingSwitch({ label, on, pending, disabled, tip, mark, markTip, note, onPress }: { label: string; on: boolean; pending: boolean; disabled: boolean; tip?: string; mark?: "ok" | "problem"; markTip?: string; note?: string; onPress: () => void }) {
  const noteId = useId();
  return <div className="setting-switch-row">
    <div className="setting-switch-line">
      <button className="afk-toggle setting-switch" role="switch" aria-checked={on} aria-label={label} aria-describedby={note ? noteId : undefined} aria-busy={pending} data-tip={tip || undefined} disabled={disabled} onClick={onPress}>
        <span className="afk-toggle-label">{label}</span>
        {pending && <span className="card-start-spinner" aria-hidden="true" />}
        <span className="afk-toggle-track" aria-hidden="true"><span className="afk-toggle-thumb" /></span>
      </button>
      {mark && <span className={"setting-switch-mark " + mark} role="img" tabIndex={0} aria-label={mark === "ok" ? label + " works" : markTip || label + " needs attention"} data-tip={markTip || undefined}><Icon name={mark === "ok" ? "check" : "warning"} /></span>}
    </div>
    {note && <small className="setting-switch-note" id={noteId}>{note}</small>}
  </div>;
}
