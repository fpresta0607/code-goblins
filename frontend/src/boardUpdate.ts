import { deliveryMark } from "./feedback.ts";
import type { Action } from "./types.ts";

// updateAction is what a tab does once the supervisor serves another board
// build than the one it loaded: nothing while the builds match or either is
// unknown; reload by itself only while the tab is hidden, the Overlord is not
// in the middle of an answer and the supervisor is live, so a reload never
// lands on a board that is down; otherwise offer a reload, so the board never
// reloads under his hands.
export function updateAction({ loaded, served, hidden, answering, connected }: { loaded: string; served: string; hidden: boolean; answering: boolean; connected: boolean }): "none" | "banner" | "reload" {
  if (!loaded || !served || loaded === served) return "none";
  return hidden && !answering && connected ? "reload" : "banner";
}

// unsentComment says whether a code-review comment on a diff holds text the
// Overlord typed and the CFO has not received: not cancelled, and still
// sending, not sent at all, or sent and not delivered.
export function unsentComment(draft: { hidden: boolean; text: string; sending: boolean }, outcome: Action | undefined): boolean {
  return !draft.hidden && !!draft.text.trim() && (draft.sending || !outcome || deliveryMark(outcome).trouble);
}
