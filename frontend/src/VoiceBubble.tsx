import { useRef, useState, type ReactNode } from "react";
import { usesBrowser } from "./dictationEngine";
import { Icon } from "./Icon";
import { RecentDictations } from "./recent-dictations";
import { TOUCH_SCREEN } from "./small-screen";
import { VOICE_HINT_KEY } from "./voice";
import { VoiceBars } from "./voice-bars";

function hintDismissed(): boolean {
  try { return localStorage.getItem(VOICE_HINT_KEY) === "dismissed"; } catch { return false; }
}

// A terminal's voice bubble, always in the pane's bottom-right corner: idle,
// a small microphone that opens the board's recent dictations, from every box
// in every window, each with Copy and Paste into this terminal; recording, a waveform that moves with the
// voice, which is a flat dotted line while the keys are held in silence. The
// first visit explains the shortcut once. model is the speech model
// the supervisor runs on this PC, which the bubble names as what listens; the
// list's foot says so too and offers the browser's own speech recognition
// where the browser has one. On a touch screen, where nothing is held down
// with ease, the bubble is the dictation itself: onTap is told of each tap,
// the first to start listening and the next to stop and send what was said
// (the Overlord, 2026-10-21, "Can't hold on my phone screen"). There tools,
// a terminal's touch tools, sit beside it.
export function VoiceBubble({ listening, level, model, onPaste, onTap, tools }: { listening: boolean; level: () => number; model: string; onPaste: (text: string) => void; onTap: (at: number) => void; tools?: ReactNode }) {
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
  const bubble = useRef<HTMLButtonElement>(null);
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
  const isTouch = matchMedia(TOUCH_SCREEN).matches;
  const tip = isTouch ? (listening ? "Listening with " + engine + " · tap to send" : "Tap to dictate")
    : listening ? "Listening with " + engine + " · release Ctrl+Shift+Space to type" : "Hold Ctrl+Shift+Space to dictate";
  // Escape closes the list from the bubble or from inside it.
  return <div className="voice-dock" onKeyDown={(event) => { if (open && event.key === "Escape") { event.stopPropagation(); close(); } }}>
    {hint && !open && <div className="voice-card voice-hint" role="note">
      <p className="voice-card-title">Speak into this terminal</p>
      <p>{isTouch ? "Tap the microphone and speak, then tap it again to send." : "Hold Ctrl+Shift+Space and speak."}</p>
      <button className="icon-button pixel-icon voice-close" aria-label="Dismiss hint" data-tip="Dismiss hint" onClick={dismiss}><Icon name="close" /></button>
    </div>}
      {open && <RecentDictations where="this terminal" model={model} onPaste={(text) => { onPaste(text); setOpen(false); }} onClose={close} onEngine={() => chosen((count) => count + 1)} />}
    {isTouch && tools}
    {isTouch ? <button ref={bubble} className={"voice-bubble" + (listening ? " recording terminal-listening" : "")}
      aria-label={listening ? "Stop and send" : "Dictate"} aria-pressed={listening} data-tip={tip}
      onPointerDown={(event) => event.preventDefault()} onClick={(event) => { dismiss(); onTap(event.timeStamp); }}>
      {listening ? <VoiceBars level={level} /> : <Icon name="mic" />}
    </button> : <button ref={bubble} className={"voice-bubble" + (listening ? " recording terminal-listening" : "")}
      aria-label={listening ? "Listening" : "Recent messages"} aria-expanded={open} data-tip={tip} onClick={toggle}>
      {listening ? <VoiceBars level={level} /> : <Icon name="mic" />}
    </button>}
    {listening && <span className="sr-only" role="status">Listening with {engine}</span>}
  </div>;
}
