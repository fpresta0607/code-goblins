import { Icon } from "./Icon";
import { chooseKeys, PANEL_KEYS } from "./panel-keys";
import type { HarnessPanel as Panel } from "./terminalStream";
import "./harness-panel.css";

// A footer that names Escape or cancelling offers a way out.
const CANCELS = /\besc(ape)?\b|\bcancel\b/i;

const DIVIDER = /^[─╌━═]+$/;

// HarnessPanel is a screen of the harness's own, a question, a permission
// prompt, a picker or a panel, open in its terminal's input line's place and
// drawn in the chat as a list he picks from (the Overlord, 2026-10-27: "all
// functionality should be available in the modern chat"): what it asks or
// shows, each choice as a button that sends the terminal's own keys for it,
// with the harness's focus marked, and one plain Cancel where the harness
// offers a way out, or where it lists nothing to pick. Every other key the
// terminal takes is the box's own (panel-keys.ts), never a row of chips (the
// CFO's review, 2026-10-27). onKeys sends keys to the terminal, and
// onShowTerminal, when given, steps the terminal in for a screen the list does
// not cover.
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
      <p className="harness-panel-hints">{panel.hints.join(" · ")}</p>
      {(!panel.choices.length || panel.hints.some((hint) => CANCELS.test(hint))) && <button type="button" className="labelled-button harness-cancel" onClick={() => onKeys(PANEL_KEYS.escape)}>Cancel</button>}
      {onShowTerminal && <button type="button" className="icon-button harness-show-terminal" aria-label="Show the terminal" data-tip="Show the terminal" onClick={onShowTerminal}><Icon name="terminal" /></button>}
    </div>
  </section>;
}
