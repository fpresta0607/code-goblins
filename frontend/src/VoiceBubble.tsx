import { useEffect, useRef, useState } from "react";
import { spoken } from "./dictation";
import { browserOffered, setUsesBrowser, usesBrowser } from "./dictationEngine";
import { Icon } from "./Icon";
import { VOICE_HINT_KEY } from "./voice";
import type { Voice } from "./useVoice";

const BARS = 9;
// While listening, a bar takes the microphone's level this often.
const SAMPLE_MS = 70;

function hintDismissed(): boolean {
  try { return localStorage.getItem(VOICE_HINT_KEY) === "dismissed"; } catch { return false; }
}

const clock = (at: number) => at ? new Date(at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "";

// A terminal's voice bubble, always in the pane's bottom-right corner: idle,
// a small microphone that opens the pane's recent dictations, each with Copy
// and Paste into this terminal; recording, it shows the microphone's level as
// bars. The first visit explains the shortcut once. model is the speech model
// the supervisor runs on this PC, which the bubble names as what listens; the
// list's foot says so too and offers the browser's own speech recognition
// where the browser has one.
export function VoiceBubble({ voice, listening, level, model, onPaste }: { voice: Voice; listening: boolean; level: () => number; model: string; onPaste: (text: string) => void }) {
  const [open, setOpen] = useState(false);
  // Read on every render, as the hint is, so a choice made in one pane shows
  // in the others.
  const [, chosen] = useState(0);
  const browser = usesBrowser();
  const engine = browser ? "this browser's speech recognition" : (model || "the speech model") + " on this PC";
  // Read on every render, so a hint dismissed in one pane stays away in the
  // others, which stay mounted.
  const [dismissed, setDismissed] = useState(false);
  const hint = !dismissed && !hintDismissed();
  const [copied, setCopied] = useState("");
  const bubble = useRef<HTMLButtonElement>(null);
  const bars = useRef<(HTMLSpanElement | null)[]>([]);
  // Each bar is one recent sample of the microphone's level, newest on the
  // right; nothing runs while the bubble is idle.
  useEffect(() => {
    if (!listening) return;
    const levels = new Array<number>(BARS).fill(0);
    let frame = 0, sampled = 0;
    const draw = (now: number) => {
      if (now - sampled >= SAMPLE_MS) {
        sampled = now;
        levels.shift();
        levels.push(level());
        levels.forEach((value, index) => { const bar = bars.current[index]; if (bar) bar.style.transform = `scaleY(${.2 + .8 * value})`; });
      }
      frame = requestAnimationFrame(draw);
    };
    frame = requestAnimationFrame(draw);
    return () => cancelAnimationFrame(frame);
  }, [listening, level]);
  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(""), 1400);
    return () => clearTimeout(timer);
  }, [copied]);
  const dismiss = () => {
    setDismissed(true);
    try { localStorage.setItem(VOICE_HINT_KEY, "dismissed"); } catch { /* the hint shows again next visit */ }
  };
  const close = () => { setOpen(false); bubble.current?.focus(); };
  const toggle = () => {
    if (open) { setOpen(false); return; }
    dismiss();
    setOpen(true);
  };
  const tip = listening ? "Listening with " + engine + " · release Ctrl+Shift+Space to type" : "Hold Ctrl+Shift+Space to dictate";
  // A page served over plain HTTP, such as across the tailnet, has no clipboard.
  const clipboard = !!navigator.clipboard;
  // Escape closes the list from the bubble or from inside it.
  return <div className="voice-dock" onKeyDown={(event) => { if (open && event.key === "Escape") { event.stopPropagation(); close(); } }}>
    {hint && !open && <div className="voice-card voice-hint" role="note">
      <p className="voice-card-title">Speak into this terminal</p>
      <p>Focus the terminal, hold Ctrl+Shift+Space, speak, then release.</p>
      <p>Click the microphone for your recent words.</p>
      <button className="icon-button pixel-icon voice-close" aria-label="Dismiss hint" data-tip="Dismiss hint" data-tip-align="end" onClick={dismiss}><Icon name="close" /></button>
    </div>}
    {open && <section className="voice-card voice-recent" role="dialog" aria-label="Recent messages">
      <p className="voice-card-title">Recent messages</p>
      <button className="icon-button pixel-icon voice-close" aria-label="Close recent messages" data-tip="Close" data-tip-align="end" onClick={close}><Icon name="close" /></button>
      {voice.messages.length ? <ul>{voice.messages.map((message, index) => {
        const key = message.at + ":" + index;
        return <li key={key}>
          {message.at > 0 && <span className="voice-meta">{clock(message.at)}</span>}
          <p className="voice-text">{message.text}</p>
          <span className="voice-actions">
            {clipboard && <button className="icon-button pixel-icon" aria-label={"Copy: " + message.text} data-tip={copied === key ? "Copied" : "Copy"} data-tip-align="end"
              onClick={() => { navigator.clipboard.writeText(message.text).then(() => setCopied(key), () => {}); }}><Icon name={copied === key ? "check" : "copy"} /></button>}
            <button className="icon-button pixel-icon" aria-label={"Paste into this terminal: " + message.text} data-tip="Paste into this terminal" data-tip-align="end"
              onClick={() => { onPaste(spoken([message.text])); setOpen(false); }}><Icon name="paste" /></button>
          </span>
        </li>;
      })}</ul> : <p className="voice-empty">Nothing dictated yet.</p>}
      <p className="voice-footer">Hold Ctrl+Shift+Space, speak, release. {browser ? "Heard by this browser's speech recognition, which sends your voice to the browser's maker." : "Heard by " + engine + ": what you say never leaves it."}</p>
      {browserOffered() && <p className="voice-empty"><label><input type="checkbox" checked={browser} onChange={(event) => { setUsesBrowser(event.target.checked); chosen((count) => count + 1); }} /> Use this browser's speech recognition instead</label></p>}
    </section>}
    <button ref={bubble} className={"voice-bubble" + (listening ? " recording terminal-listening" : "")}
      aria-label={listening ? "Listening" : "Recent messages"} aria-expanded={open} data-tip={tip} data-tip-align="end" onClick={toggle}>
      {listening ? <><span className="voice-dot" aria-hidden="true" /><span className="voice-bars" aria-hidden="true">{Array.from({ length: BARS }, (_, index) => <span key={index} ref={(bar) => { bars.current[index] = bar; }} />)}</span></> : <Icon name="mic" />}
    </button>
    {listening && <span className="sr-only" role="status">Listening with {engine}</span>}
  </div>;
}
