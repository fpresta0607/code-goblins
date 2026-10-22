import { useEffect, useRef } from "react";

// A terminal's text laid out to select on a touch screen, where xterm's own
// selection, made by dragging a mouse, cannot be made: a long press selects
// in it as in any page, and the phone's own menu copies. It opens at its end,
// where the terminal is, and Done returns to the terminal.
export function TerminalTextSheet({ text, onClose }: { text: string; onClose: () => void }) {
  const body = useRef<HTMLPreElement>(null);
  useEffect(() => { if (body.current) body.current.scrollTop = body.current.scrollHeight; }, []);
  return <section className="terminal-text" role="dialog" aria-label="Terminal text">
    <header><p>Hold to select, then tap Copy.</p><button type="button" className="primary" onClick={onClose}>Done</button></header>
    <pre ref={body}>{text}</pre>
  </section>;
}
