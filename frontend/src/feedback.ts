import type { IconName } from "./Icon.tsx";
import type { Action } from "./types.ts";

export interface Submission {
  id: string;
  payload: string;
}
// The payload, not whether HTTP or SSE arrived first, owns request identity.
export function submissionFor(
  payload: string,
  prior: Submission | null,
  createID: () => string,
): Submission {
  return prior?.payload === payload ? prior : { id: createID(), payload };
}
export function alreadyKnown(
  submission: Submission | null,
  payload: string,
  actions: Action[],
): Action | undefined {
  return submission?.payload === payload
    ? actions.find((action) => action.id === submission.id)
    : undefined;
}

// deliveryMark shows an action's delivery as a mark: one check while it is
// sent, two once accepted. Only trouble spells itself out in words.
export function deliveryMark(action: Action): { icon: IconName; label: string; trouble: boolean } {
  const cfo = action.kind === "review" || action.kind.startsWith("cfo_");
  switch (action.status) {
    case "succeeded": return { icon: "check-double", label: cfo ? "Accepted by the CFO" : action.kind === "goblin_answer" ? "Delivered to the goblin" : "Done", trouble: false };
    case "running": return { icon: "check", label: "Sending", trouble: false };
    case "failed": return { icon: "close", label: "Could not deliver", trouble: true };
    case "uncertain": return { icon: "warning", label: "Delivery unconfirmed. Inspect " + (cfo ? "the CFO queue" : "the terminal") + " before sending again.", trouble: true };
    default: return { icon: "clock", label: "Queued", trouble: false };
  }
}
