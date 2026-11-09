import { useEffect, useState } from "react";
import { CodeBlock } from "./code-block";
import { Icon } from "./Icon";
import { inlineElements } from "./messageText";
import { chooseKeys, PANEL_KEYS } from "./panel-keys";
import { panelParts } from "./panel-words";
import type { HarnessPanel as Panel } from "./terminalStream";
import "./harness-panel.css";

// A footer that names Escape or cancelling offers a way out.
const CANCELS = /\besc(ape)?\b|\bcancel\b/i;

// How long a press waits for the harness to take it before its choices can be
// pressed again.
const PRESS_WAIT_MS = 3000;

// HarnessPanel is a screen of the harness's own, a question, a permission
// prompt, a picker or a panel, open in its terminal's input line's place and
// drawn in the chat as a list he picks from (the Overlord, 2026-10-27: "all
// functionality should be available in the modern chat"). In Modern Chat it
// is a message at the conversation's end, never a popup over the box
// (#2223): what it asks in the chat's own words, its title in bold, its
// paragraphs with the terminal's wrap undone and a command or preview it
// quotes as a code card, with none of the terminal's gutter bars or edges
// (panel-words.ts); each choice as a button that sends the terminal's own
// keys for it, with the harness's focus marked; and one plain Cancel where the
// harness offers a way out, or where it lists nothing to pick. Every other
// key the terminal takes is the box's own (panel-keys.ts), never a row of
// chips (the CFO's review, 2026-10-27), and a press on a choice or on Cancel
// leaves the keyboard in the box. One press sends its keys once: the panel's
// buttons wait until the harness has taken it, or for PRESS_WAIT_MS when the
// screen stays as it was. onKeys sends keys to the terminal. Nothing in it
// shows the terminal: the style menu of the session's picture is the one
// switch to Terminal (the Overlord, 2026-11-04).
export function HarnessPanel({ panel, name, isInChat = false, onKeys }: {
  panel: Panel; name: string; isInChat?: boolean; onKeys: (keys: string) => void;
}) {
  const screen = JSON.stringify(panel);
  const [pressed, setPressed] = useState("");
  const isPressed = pressed === screen;
  useEffect(() => {
    if (!pressed) return;
    const wait = window.setTimeout(() => setPressed(""), PRESS_WAIT_MS);
    return () => window.clearTimeout(wait);
  }, [pressed]);
  const press = (keys: string) => {
    if (isPressed) return;
    setPressed(screen);
    onKeys(keys);
  };
  const parts = panelParts(panel.rows);
  return <section className={isInChat ? "harness-panel in-chat message message-reply" : "harness-panel"} role="group" aria-label={name + " asks"}>
    {parts.length > 0 && <div className={isInChat ? "harness-panel-rows message-words" : "harness-panel-rows"}>
      {parts.map((part, at) => part.kind === "break" ? <hr key={at} />
        : part.kind === "code" ? <CodeBlock key={at} text={part.text} language="" />
          : <p key={at} className={part.kind === "title" ? "harness-panel-title" : undefined}>{inlineElements(part.text)}</p>)}
    </div>}
    {panel.choices.length > 0 && <div className="harness-panel-choices" aria-label="Choices">
      {panel.choices.map((choice, at) => {
        const keys = chooseKeys(panel, at);
        return <button key={at} type="button" className="harness-choice" aria-current={choice.isFocused || undefined} disabled={!keys || isPressed} onMouseDown={(event) => event.preventDefault()} onClick={() => { if (keys) press(keys); }}>
          <span className="harness-choice-key" aria-hidden="true">{choice.number || (choice.isFocused ? <Icon name="chevron" /> : "")}</span>
          <span className="harness-choice-text">{choice.text}</span>
        </button>;
      })}
    </div>}
    <div className="harness-panel-foot">
      <p className="harness-panel-hints">{panel.hints.join(" · ")}</p>
      {!panel.choices.length && panel.hints.some((hint) => /\benter\b/i.test(hint)) && <button type="button" className="labelled-button harness-cancel" disabled={isPressed} onMouseDown={(event) => event.preventDefault()} onClick={() => press(PANEL_KEYS.enter)}>Continue</button>}
      {(!panel.choices.length || panel.hints.some((hint) => CANCELS.test(hint))) && <button type="button" className="labelled-button harness-cancel" disabled={isPressed} onMouseDown={(event) => event.preventDefault()} onClick={() => press(PANEL_KEYS.escape)}>Cancel</button>}
    </div>
  </section>;
}
