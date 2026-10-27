import { Icon } from "./Icon";
import { useNow } from "./clock";
import { Notice } from "./notice";
import { elapsedText, formatAge } from "./session-usage";
import { WorkingIndicator } from "./working-indicator";

// The line at the very bottom of the conversation: the session's working
// state in its harness's colour and no icon, then the time facts and not a
// wall of numbers (the Overlord, 2026-10-26): how long it has been working,
// when he last sent a message, and when the last reply arrived. Usage that
// cannot be read is said once, in one plain sentence on the board's notice
// (the CFO's review, 2026-10-27), and why names the provider that was not.
export function SessionFooter({ ink, isWorking, workingSince, lastSentAt, lastReplyAt, now, hasUsage, why, onStop }: {
  ink: string; isWorking: boolean; workingSince: string; lastSentAt: string; lastReplyAt: string; now: number; hasUsage: boolean; why: string; onStop?: () => void;
}) {
  // The board's clock reads whole minutes; how long the session has worked
  // shows seconds, so the footer reads its own clock each second, and only
  // while the session works.
  const clock = Math.max(now, useNow(isWorking ? 1_000 : 60_000));
  const worked = isWorking ? elapsedText(workingSince, clock) : "";
  return <div className="session-footer" role="status" aria-label="Session timing">
    {isWorking && <span className="session-working" style={{ color: ink }}><WorkingIndicator ink={ink} /><span className="session-worked" style={{ color: ink }}>Working{worked ? " " + worked : ""}</span>{onStop && <button type="button" className="labelled-button session-stop" onClick={onStop}><Icon name="pause" />Stop</button>}</span>}
    {lastSentAt && <span className="session-fact">Sent {formatAge(lastSentAt, now)}</span>}
    {lastReplyAt && <span className="session-fact">Replied {formatAge(lastReplyAt, now)}</span>}
    {(!hasUsage || why) && <Notice>{why ? why + "." : "This session's usage could not be read."}</Notice>}
  </div>;
}
