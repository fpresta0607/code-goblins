import type { HarnessStatus } from "./terminalStream";
import "./terminal-status.css";

// What the session's terminal draws by its prompt line, its context gauge
// and its notices, as Claude Code's "83% context used", under the box for as
// long as the terminal shows it (#2236, #2294). A notice of a failed update,
// as "✘ Auto-update failed · Run claude doctor", is not drawn here: the
// panel's header offers it as Retry update (update-check-button.tsx, #2956).
export function TerminalStatus({ status }: { status: HarnessStatus }) {
  const notices = status.notices.filter((notice) => notice.check !== "update");
  if (!status.gauge && notices.length === 0) return null;
  return <div className="terminal-status" role="status" aria-label="Terminal status">
    {status.gauge && <span className="terminal-gauge">{status.gauge}</span>}
    {notices.map((notice, index) => <span key={index} className="terminal-notice">{notice.text}</span>)}
  </div>;
}
