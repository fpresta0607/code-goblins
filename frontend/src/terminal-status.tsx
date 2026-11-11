import type { HarnessStatus } from "./terminalStream";
import type { UpdateCheck } from "./types";
import "./terminal-status.css";

interface TerminalStatusProps { status: HarnessStatus; check: UpdateCheck | undefined; onRetry: () => void }

// What the session's terminal draws by its prompt line, its context gauge
// and its notices, as Claude Code's "83% context used · ✘ Auto-update failed ·
// Run claude doctor", under the box for as long as the terminal shows it
// (#2236, #2294). A notice of a failed update says why once a check of it
// said, and offers Retry, which has the supervisor run the harness's own
// update check outside the conversation and shows how it ended. It goes once
// the install is the newest, which the terminal's own notice outlasts until
// its session restarts. No notice types anything into the session (the
// Overlord, 2026-11-10: "the Auto-update failed notice needs a reason and a
// Retry", #2333).
export function TerminalStatus({ status, check, onRetry }: TerminalStatusProps) {
  const notices = status.notices.filter((notice) => !(notice.check === "update" && check?.current));
  if (!status.gauge && notices.length === 0) return null;
  const isChecking = check?.running === true;
  const said = isChecking ? "" : check?.result ?? "";
  return <div className="terminal-status" role="status" aria-label="Terminal status">
    {status.gauge && <span className="terminal-gauge">{status.gauge}</span>}
    {notices.map((notice, index) => notice.check === "update"
      ? <span key={index} className="terminal-check">
        <span className="terminal-notice">{notice.text}{said && check?.failed ? ": " + said : ""}</span>
        {said && !check?.failed && <span className="terminal-check-result">Checked again: {said}</span>}
        <button type="button" className="terminal-notice-action" disabled={isChecking} aria-description="Runs the update check again, outside the conversation" onClick={onRetry}>{isChecking ? "Checking" : "Retry"}</button>
      </span>
      : <span key={index} className="terminal-notice">{notice.text}</span>)}
  </div>;
}
