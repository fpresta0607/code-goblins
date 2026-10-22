import { useEffect, useState } from "react";
import { spoken } from "./dictation";
import { browserOffered, setUsesBrowser, usesBrowser } from "./dictationEngine";
import { Icon } from "./Icon";
import { refreshDictations, useVoice } from "./useVoice";

const clock = (at: number) => at ? new Date(at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "";

// The board's recent dictations, from every box in every window, each with
// Copy and Paste into where, as a terminal's voice bubble and the message box
// list them. Its foot names what listens, model on this PC or the browser's
// own speech recognition, and offers the browser's where it has one; onEngine
// is told of a choice of it. It reads the list as it opens, and again as its
// window takes the focus, with what was dictated in another window meanwhile.
export function RecentDictations({ where, model, onPaste, onClose, onEngine }: {
  where: string; model: string; onPaste: (text: string) => void; onClose: () => void; onEngine: () => void;
}) {
  const messages = useVoice();
  const [copied, setCopied] = useState("");
  const browser = usesBrowser();
  const engine = browser ? "this browser's speech recognition" : (model || "the speech model") + " on this PC";
  useEffect(() => {
    refreshDictations();
    window.addEventListener("focus", refreshDictations);
    return () => window.removeEventListener("focus", refreshDictations);
  }, []);
  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(""), 1400);
    return () => clearTimeout(timer);
  }, [copied]);
  // A page served over plain HTTP, such as across the tailnet, has no clipboard.
  const clipboard = !!navigator.clipboard;
  return <section className="voice-card voice-recent" role="dialog" aria-label="Recent messages">
    <p className="voice-card-title">Recent messages</p>
    <button className="icon-button pixel-icon voice-close" aria-label="Close recent messages" data-tip="Close" onClick={onClose}><Icon name="close" /></button>
    {messages.length ? <ul>{messages.map((message, index) => {
      const key = message.at + ":" + index;
      return <li key={key}>
        {message.at > 0 && <span className="voice-meta">{clock(message.at)}</span>}
        <p className="voice-text">{message.text}</p>
        <span className="voice-actions">
          {clipboard && <button className="icon-button pixel-icon" aria-label={"Copy: " + message.text} data-tip={copied === key ? "Copied" : "Copy"}
            onClick={() => { navigator.clipboard.writeText(message.text).then(() => setCopied(key), () => {}); }}><Icon name={copied === key ? "check" : "copy"} /></button>}
          <button className="icon-button pixel-icon" aria-label={"Paste into " + where + ": " + message.text} data-tip={"Paste into " + where}
            onClick={() => onPaste(spoken([message.text]))}><Icon name="paste" /></button>
        </span>
      </li>;
    })}</ul> : <p className="voice-empty">Nothing dictated yet.</p>}
    <p className="voice-footer">Hold Ctrl+Shift+Space, speak, release. {browser ? "Heard by this browser's speech recognition, which sends your voice to the browser's maker." : "Heard by " + engine + ": what you say never leaves it."}</p>
    {browserOffered() && <p className="voice-empty"><label><input type="checkbox" checked={browser} onChange={(event) => { setUsesBrowser(event.target.checked); onEngine(); }} /> Use this browser's speech recognition instead</label></p>}
  </section>;
}
