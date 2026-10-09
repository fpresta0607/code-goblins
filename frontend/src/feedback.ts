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
// A delivery typed for a reader inside a turn is on its way, and says when
// it will be read. Only trouble spells itself out, and one that never
// arrived says what to do in the supervisor's own plain words.
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
    case "uncertain": return { icon: "warning", label: action.advice || "Not confirmed. Check " + (cfo ? "the CFO's terminal" : "the goblin's terminal") + " before sending it again.", trouble: true };
    case "queued": return { icon: "check", label: action.message || "Queued", trouble: false };
    default: return { icon: "check", label: action.awaiting && action.message ? action.message : "Sending", trouble: false };
  }
}

// A run item's state in plain words, as the Overlord said on 2026-10-08:
// "instead of finished exit zero, just have the same complete notification".
// A command that ends cleanly is Complete, and one that failed says why: the
// last line it printed, or why it never finished. Its exit code is History's.
export function runMark(run: Run): { icon: IconName; label: string; trouble: boolean } {
  switch (run.state) {
    case "ready": return { icon: "play", label: "Ready to run", trouble: false };
    case "running": return { icon: "clock", label: "Running", trouble: false };
    case "succeeded": return { icon: "check", label: "Complete", trouble: false };
    case "failed": {
      const why = run.exit_code === null ? run.reason : lastLine(run.output) || "it exited with code " + run.exit_code;
      return { icon: "warning", label: why ? "Failed: " + why : "Failed", trouble: true };
    }
    // He stopped it himself, which is no trouble to show him.
    case "stopped": return { icon: "stop-circle", label: "Stopped", trouble: false };
    case "expired": return { icon: "close", label: "Expired", trouble: false };
    // A goblin's own command is withdrawn when the goblin moves past it.
    case "withdrawn": return { icon: "close", label: run.task ? "Withdrawn" : "Withdrawn by the CFO", trouble: false };
    default: return { icon: "clock", label: "Waiting", trouble: false };
  }
}

// runFailure is the one plain line a failed run item's card says, and empty
// for any other: why it failed, and what happens next. Its result goes to
// whoever asked for it, the CFO or the goblin, which takes the next step.
export function runFailure(run: Run): string {
  if (run.state !== "failed") return "";
  const why = (run.exit_code === null ? run.reason : lastLine(run.output)) || "It exited with code " + run.exit_code;
  const asker = run.task ? "The goblin that asked" : "The CFO";
  const next = /could not be told/.test(run.reason) ? asker + " may not have its output yet: telling it failed." : asker + " has its output and takes the next step.";
  return why.replace(/\.$/, "") + ". " + next;
}

// lastLine is the last line of output that holds text, cut to fit a status
// line.
function lastLine(output: string): string {
  const line = output.split(/\r?\n/).map((each) => each.trim()).filter(Boolean).at(-1) || "";
  return line.length > 160 ? line.slice(0, 157) + "..." : line;
}
