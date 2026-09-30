import { useEffect, useRef, useState } from "react";
import { spoken } from "./dictation";
import { Icon } from "./Icon";
import type { Voice } from "./useVoice";
import type { SiqspeakState } from "./voice";

const HINT_KEY = "cfo-voice-hint-v1";
const BARS = 9;
// While listening, a bar takes the microphone's level this often.
const SAMPLE_MS = 70;

const STATUS: Record<SiqspeakState, string> = {
  running: "SIQspeak running",
  stopped: "SIQspeak is not running",
  missing: "SIQspeak was not found",
  unreadable: "SIQspeak could not be read",
};

function hintDismissed(): boolean {
  try { return localStorage.getItem(HINT_KEY) === "dismissed"; } catch { return false; }
}

const clock = (at: number) => at ? new Date(at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "";

// A terminal's voice bubble, always in the pane's bottom-right corner: idle,
// a small microphone that names SIQspeak's state in its tip and opens the
// pane's recent messages,
// SIQspeak's transcriptions beside the board's own dictations, each with
// Copy and Paste into this terminal; recording, it shows the microphone's
// level as bars. The first visit explains the shortcut once.
export function VoiceBubble({ voice, listening, level, onPaste }: { voice: Voice; listening: boolean; level: () => number; onPaste: (text: string) => void }) {
  const [open, setOpen] = useState(false);
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
    try { localStorage.setItem(HINT_KEY, "dismissed"); } catch { /* the hint shows again next visit */ }
  };
  const close = () => { setOpen(false); bubble.current?.focus(); };
  const toggle = () => {
    if (open) { setOpen(false); return; }
    dismiss();
    setOpen(true);
    void voice.refresh();
  };
  const status = voice.state ? STATUS[voice.state] : "";
  const tip = listening ? "Listening · release Ctrl+Shift+Space to type" : status || "Hold Ctrl+Shift+Space to dictate";
  // A page served over plain HTTP, such as across the tailnet, has no clipboard.
  const clipboard = !!navigator.clipboard;
  // Escape closes the list from the bubble or from inside it.
  return <div className="voice-dock" onKeyDown={(event) => { if (open && event.key === "Escape") { event.stopPropagation(); close(); } }}>
    {hint && !open && <div className="voice-card voice-hint" role="note">
      <p className="voice-card-title">Speak into this terminal</p>
      <p>Focus the terminal, hold Ctrl+Shift+Space, speak, then release.</p>
      <p>Click the green bubble for your recent words.</p>
      <button className="icon-button voice-close" aria-label="Dismiss hint" data-tip="Dismiss hint" data-tip-align="end" onClick={dismiss}><Icon name="close" /></button>
    </div>}
    {open && <section className="voice-card voice-recent" role="dialog" aria-label="Recent messages">
      <p className="voice-card-title">Recent messages</p>
      {status && <p className={"voice-status " + voice.state}><span className="status-dot" />{status}</p>}
      <button className="icon-button voice-close" aria-label="Close recent messages" data-tip="Close" data-tip-align="end" onClick={close}><Icon name="close" /></button>
      {voice.state === "stopped" && <p className="voice-help">Open SIQspeak from its desktop shortcut. Until it runs, holding Ctrl+Shift+Space here uses the browser's speech recognition.</p>}
      {voice.state === "missing" && <p className="voice-help">Install SIQspeak on this computer to dictate locally. Until then, holding Ctrl+Shift+Space here uses the browser's speech recognition.</p>}
      {voice.messages.length ? <ul>{voice.messages.map((message, index) => {
        const key = message.source + ":" + message.at + ":" + index;
        return <li key={key}>
          <span className="voice-meta">{[clock(message.at), message.source === "board" ? "Board" : "SIQspeak"].filter(Boolean).join(" · ")}</span>
          <p className="voice-text">{message.text}</p>
          <span className="voice-actions">
            {clipboard && <button className="icon-button" aria-label={"Copy: " + message.text} data-tip={copied === key ? "Copied" : "Copy"} data-tip-align="end"
              onClick={() => { navigator.clipboard.writeText(message.text).then(() => setCopied(key), () => {}); }}><Icon name={copied === key ? "check" : "copy"} /></button>}
            <button className="icon-button" aria-label={"Paste into this terminal: " + message.text} data-tip="Paste into this terminal" data-tip-align="end"
              onClick={() => { onPaste(spoken([message.text])); setOpen(false); }}><Icon name="paste" /></button>
          </span>
        </li>;
      })}</ul> : <p className="voice-empty">Nothing dictated yet.</p>}
      <p className="voice-footer">Hold Ctrl+Shift+Space, speak, release.</p>
    </section>}
    <button ref={bubble} className={"voice-bubble" + (voice.state ? " " + voice.state : "") + (listening ? " recording terminal-listening" : "")}
      aria-label={listening ? "Listening" : "Recent messages" + (status ? ". " + status : "")} aria-expanded={open} data-tip={tip} data-tip-align="end" onClick={toggle}>
      {listening ? <><span className="voice-dot" aria-hidden="true" /><span className="voice-bars" aria-hidden="true">{Array.from({ length: BARS }, (_, index) => <span key={index} ref={(bar) => { bars.current[index] = bar; }} />)}</span></> : <Icon name="mic" />}
    </button>
    {listening && <span className="sr-only" role="status">Listening</span>}
  </div>;
}
