import { deliveryMark } from "./feedback.ts";
import type { Action } from "./types.ts";

// updateAction is what a tab does once the supervisor serves another board
// build than the one it loaded. With a live connection and nothing unsent,
// it reloads a hidden, idle tab, or any tab after an update just installed,
// even with the Command Center open. Otherwise it offers the Reload banner;
// unknown or matching builds need no action.
export function updateAction({ loaded, served, hidden, answering, connected, unsent = false, updated = false }: { loaded: string; served: string; hidden: boolean; answering: boolean; connected: boolean; unsent?: boolean; updated?: boolean }): "none" | "banner" | "reload" {
  if (!loaded || !served || loaded === served) return "none";
  return connected && !unsent && (updated || hidden && !answering) ? "reload" : "banner";
}

// unsentComment says whether a code-review comment on a diff holds text the
// Overlord typed and the CFO has not received: not cancelled, and still
// sending, not sent at all, or sent and not delivered.
export function unsentComment(draft: { hidden: boolean; text: string; sending: boolean }, outcome: Action | undefined): boolean {
  return !draft.hidden && !!draft.text.trim() && (draft.sending || !outcome || deliveryMark(outcome).trouble);
}
