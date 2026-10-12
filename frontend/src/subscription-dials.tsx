import type { Snapshot } from "./types";
import { SubscriptionDial } from "./subscription-dial";

// The weekly allowance of each subscription in use, a dial each, on the
// board's CFO bar and on the CFO's card on the canvas.
export function SubscriptionDials({ snapshot, now }: { snapshot: Snapshot; now: number }) {
  if (!snapshot.subscriptions?.length) return null;
  return <div className="subscription-dials" role="group" aria-label="Weekly subscription allowance">
    {snapshot.subscriptions.map((usage) => <SubscriptionDial key={usage.provider} usage={usage} now={now} />)}
  </div>;
}
