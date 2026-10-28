import type { ReactNode } from "react";
import { Notice } from "./notice";

// The line at the very bottom of the conversation: usage, the session's
// context ring, which opens its statistics; the mark of the session's harness
// sits under its last reply instead (the Overlord, 2026-10-28: "Don't need
// the Claude icon by the dither"). Whether it works shows at the end of the reply in progress,
// and when he sent and it replied under his message (the Overlord,
// 2026-10-27: "this goes under my messages, not above the chat bar"). Usage
// that cannot be read is said once, in one plain sentence on the board's
// notice (the CFO's review, 2026-10-27), and why names the provider that was
// not.
export function SessionFooter({ hasUsage, why, usage }: { hasUsage: boolean; why: string; usage?: ReactNode }) {
  return <div className="session-footer">
    {(!hasUsage || why) && <Notice>{why ? why + "." : "Usage unavailable"}</Notice>}
    {usage}
  </div>;
}
