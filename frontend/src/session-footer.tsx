import type { ReactNode } from "react";
import { HarnessMark } from "./harness-mark";
import { Notice } from "./notice";

// The line at the very bottom of the conversation: the mark of the session's
// harness, and usage, the session's context ring at its end, which opens its
// statistics. Whether it works shows at the end of the reply in progress,
// and when he sent and it replied under his message (the Overlord,
// 2026-10-27: "this goes under my messages, not above the chat bar"). Usage
// that cannot be read is said once, in one plain sentence on the board's
// notice (the CFO's review, 2026-10-27), and why names the provider that was
// not.
export function SessionFooter({ harness, hasUsage, why, usage }: { harness: string; hasUsage: boolean; why: string; usage?: ReactNode }) {
  return <div className="session-footer">
    {harness && <HarnessMark harness={harness} />}
    {(!hasUsage || why) && <Notice>{why ? why + "." : "Usage unavailable"}</Notice>}
    {usage}
  </div>;
}
