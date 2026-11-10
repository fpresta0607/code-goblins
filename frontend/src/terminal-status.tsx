import type { HarnessStatus } from "./terminalStream";
import "./terminal-status.css";

// What the session's terminal draws over its prompt line, its context gauge
// and its notices, as Claude Code's "83% context used · ✘ Auto-update failed ·
// Run claude doctor", over the box for as long as the terminal shows it
// (#2236). A notice that names something to do is a button that sends the
// command that does it, as Run claude doctor sends /doctor, whose result the
// conversation then shows.
export function TerminalStatus({ status, isSending, onAction }: { status: HarnessStatus; isSending: boolean; onAction: (command: string) => void }) {
  return <div className="terminal-status" role="status" aria-label="Terminal status">
    {status.gauge && <span className="terminal-gauge">{status.gauge}</span>}
    {status.notices.map((notice, index) => notice.action
      ? <button key={index} type="button" className="terminal-notice-action" disabled={isSending} aria-description={"Sends " + notice.action} onClick={() => onAction(notice.action)}>{notice.text}</button>
      : <span key={index} className="terminal-notice">{notice.text}</span>)}
  </div>;
}
