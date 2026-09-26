import type { IconName } from "./Icon.tsx";
import type { Action, Run } from "./types.ts";

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

// deliveryMark shows an action's delivery as a mark: one check while it is on
// its way, two once delivered, naming the goblin when the caller knows it.
// Only trouble spells itself out.
export function deliveryMark(action: Action, goblin = "the goblin"): { icon: IconName; label: string; trouble: boolean } {
  const cfo = action.kind === "review" || action.kind.startsWith("cfo_");
  switch (action.status) {
    case "succeeded":
      if (action.kind.endsWith("_clear")) return { icon: "check", label: "Cleared", trouble: false };
      // A review answer for a replaced goblin goes to the CFO, so the action
      // alone cannot say it was delivered; the review item's flag does.
      if (action.kind === "review_answer") return { icon: "check", label: "Sent to the goblin or the CFO", trouble: false };
      return { icon: "check-double", label: cfo ? "CFO received" : action.kind === "goblin_answer" ? "Delivered to " + goblin : "Done", trouble: false };
    case "failed": return { icon: "close", label: "Could not deliver", trouble: true };
    case "uncertain": return { icon: "warning", label: "Delivery unconfirmed. Inspect " + (cfo ? "the CFO queue" : "the terminal") + " before sending again.", trouble: true };
    default: return { icon: "check", label: "Sending", trouble: false };
  }
}

// A run item's state in plain words, with its exit code once it finished.
export function runMark(run: Run): { icon: IconName; label: string; trouble: boolean } {
  const exit = run.exit_code === null ? "" : " · exit " + run.exit_code;
  switch (run.state) {
    case "ready": return { icon: "play", label: "Ready to run", trouble: false };
    case "running": return { icon: "clock", label: "Running", trouble: false };
    case "succeeded": return { icon: "check", label: "Finished" + exit, trouble: false };
    case "failed": return { icon: "warning", label: "Failed" + exit, trouble: true };
    case "expired": return { icon: "close", label: "Expired", trouble: false };
    default: return { icon: "clock", label: "Waiting", trouble: false };
  }
}
