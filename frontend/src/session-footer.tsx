import { Icon } from "./Icon";
import { elapsedText, formatAge } from "./session-usage";
import { WorkingIndicator } from "./working-indicator";

// The line at the very bottom of the conversation: the session's working
// state in its harness's colour and no icon, then the time facts and not a
// wall of numbers (the Overlord, 2026-10-26): how long it has been working,
// when he last sent a message, and when the last reply arrived.
export function SessionFooter({ ink, isWorking, workingSince, lastSentAt, lastReplyAt, now, hasUsage, why, onStop }: {
  ink: string; isWorking: boolean; workingSince: string; lastSentAt: string; lastReplyAt: string; now: number; hasUsage: boolean; why: string; onStop?: () => void;
}) {
  const worked = isWorking ? elapsedText(workingSince, now) : "";
  return <div className="session-footer" role="status" aria-label="Session timing">
    {isWorking && <span className="session-working" style={{ color: ink }}><WorkingIndicator ink={ink} /><span className="session-worked" style={{ color: ink }}>Working{worked ? " " + worked : ""}</span>{onStop && <button type="button" className="labelled-button session-stop" onClick={onStop}><Icon name="pause" />Stop</button>}</span>}
    {lastSentAt && <span className="session-fact">Sent {formatAge(lastSentAt, now)}</span>}
    {lastReplyAt && <span className="session-fact">Replied {formatAge(lastReplyAt, now)}</span>}
    {!hasUsage && <span className="session-fact muted">usage unreadable</span>}
    {why && <span className="session-fact muted" data-tip={why}>{why}</span>}
  </div>;
}
