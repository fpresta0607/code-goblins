import { Icon } from "./Icon";
import { useTerminalLink } from "./terminal-link";
import { tryAgain } from "./try-again";
import "./limit.css";

// LimitStrip is one compact line on the top of the message box while the
// session is at a usage limit (the Overlord, 2026-10-28, of Claude's own
// app): the gauge, "Session limit reached", when it resets and Try again,
// which asks the session to continue through its terminal's link (terminal,
// try-again.ts). It takes the place of the harness's own words for the
// limit.
export function LimitStrip({ resets, terminal }: { resets: string; terminal: string }) {
  const link = useTerminalLink(terminal);
  return <div className="limit-strip" role="status">
    <Icon name="gauge" />
    <strong>Session limit reached</strong>
    {resets && <span className="limit-strip-when">Resets at {resets}</span>}
    <button type="button" disabled={!link} onClick={() => { if (link) tryAgain(terminal, link); }}>Try again</button>
  </div>;
}
