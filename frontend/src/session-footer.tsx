import type { ReactNode } from "react";
import { useNow } from "./clock";
import { HarnessMark } from "./harness-mark";
import { Notice } from "./notice";
import { elapsedText, formatAge } from "./session-usage";
import { WorkingIndicator } from "./working-indicator";

// The line at the very bottom of the conversation: the session's working
// state in its harness's colour and no icon, then the time facts and not a
// wall of numbers (the Overlord, 2026-10-26): how long it has been working,
// when he last sent a message, and when the last reply arrived; it starts
// with the mark of the session's harness, and usage is the session's context
// ring at its end, which opens its statistics. Usage that cannot be read is
// said once, in one plain sentence on the board's notice (the CFO's review,
// 2026-10-27), and why names the provider that was not.
export function SessionFooter({ harness, ink, isWorking, workingSince, lastSentAt, lastReplyAt, now, hasUsage, why, usage }: {
  harness: string; ink: string; isWorking: boolean; workingSince: string; lastSentAt: string; lastReplyAt: string; now: number; hasUsage: boolean; why: string; usage?: ReactNode;
}) {
  // The board's clock reads whole minutes; how long the session has worked
  // shows seconds, so the footer reads its own clock each second, and only
  // while the session works.
  const clock = Math.max(now, useNow(isWorking ? 1_000 : 60_000));
  const worked = isWorking ? elapsedText(workingSince, clock) : "";
  return <div className="session-footer" role="status" aria-label="Session timing">
    {harness && <HarnessMark harness={harness} />}
    {isWorking && <span className="session-working" style={{ color: ink }}><WorkingIndicator ink={ink} /><span className="session-worked" style={{ color: ink }}>Working{worked ? " " + worked : ""}</span></span>}
    {lastSentAt && <span className="session-fact">Sent {formatAge(lastSentAt, now)}</span>}
    {lastReplyAt && <span className="session-fact">Replied {formatAge(lastReplyAt, now)}</span>}
    {(!hasUsage || why) && <Notice>{why ? why + "." : "Usage unavailable"}</Notice>}
    {usage}
  </div>;
}
