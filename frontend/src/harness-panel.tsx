import { Icon } from "./Icon";
import { chooseKeys, PANEL_KEYS } from "./panel-keys";
import type { HarnessPanel as Panel } from "./terminalStream";
import "./harness-panel.css";

// The keys every open screen offers as buttons, for what its list cannot
// name: moving, confirming, leaving, the next field and a toggle.
const KEYS = [
  { label: "Up", keys: PANEL_KEYS.up }, { label: "Down", keys: PANEL_KEYS.down }, { label: "Left", keys: PANEL_KEYS.left }, { label: "Right", keys: PANEL_KEYS.right },
  { label: "Enter", keys: PANEL_KEYS.enter }, { label: "Esc", keys: PANEL_KEYS.escape }, { label: "Tab", keys: PANEL_KEYS.tab }, { label: "Space", keys: PANEL_KEYS.space },
] as const;

const DIVIDER = /^[─╌━═]+$/;

// HarnessPanel is a screen of the harness's own, a question, a permission
// prompt, a picker or a panel, open in its terminal's input line's place and
// drawn in the chat as a list he picks from (the Overlord, 2026-10-27: "all
// functionality should be available in the modern chat"): what it asks or
// shows, each choice as a button that sends the terminal's own keys for it,
// the harness's focus marked, and the keys it takes. onKeys sends keys to the
// terminal, and onShowTerminal, when given, steps the terminal in for a
// screen the list does not cover.
export function HarnessPanel({ panel, name, onKeys, onShowTerminal }: {
  panel: Panel; name: string; onKeys: (keys: string) => void; onShowTerminal?: () => void;
}) {
  return <section className="harness-panel" role="group" aria-label={name + " asks"}>
    {panel.rows.length > 0 && <div className="harness-panel-rows">
      {panel.rows.map((row, at) => DIVIDER.test(row.trim()) ? <hr key={at} /> : <p key={at}>{row || " "}</p>)}
    </div>}
    {panel.choices.length > 0 && <div className="harness-panel-choices" aria-label="Choices">
      {panel.choices.map((choice, at) => {
        const keys = chooseKeys(panel, at);
        return <button key={at} type="button" className="harness-choice" aria-current={choice.isFocused || undefined} disabled={!keys} onClick={() => { if (keys) onKeys(keys); }}>
          <span className="harness-choice-key" aria-hidden="true">{choice.number || (choice.isFocused ? <Icon name="chevron" /> : "")}</span>
          <span className="harness-choice-text">{choice.text}</span>
        </button>;
      })}
    </div>}
    <div className="harness-panel-foot">
      <div className="harness-panel-keys" role="group" aria-label="Keys">
        {KEYS.map((key) => <button key={key.label} type="button" className="harness-key" aria-label={"Press " + key.label} onClick={() => onKeys(key.keys)}>{key.label}</button>)}
      </div>
      {onShowTerminal && <button type="button" className="icon-button harness-show-terminal" aria-label="Show the terminal" data-tip="Show the terminal" onClick={onShowTerminal}><Icon name="terminal" /></button>}
    </div>
    {panel.hints.length > 0 && <p className="harness-panel-hints">{panel.hints.join(" · ")}</p>}
  </section>;
}
