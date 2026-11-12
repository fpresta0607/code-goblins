import type { Snapshot } from "./types";
import { KeySpendDial } from "./key-spend-dial";
import { SubscriptionDial } from "./subscription-dial";

// The usage of each subscription and API key in use, a dial each, on the
// board's CEO bar and on the CEO's card on the canvas: a ring of the weekly
// allowance a subscription leaves, and the dollars an API key has spent
// against its own limit and window. Each ring carries its provider's icon
// inside it, on the bar as on the card, and no name beside it (the Overlord,
// 2026-11-02, on the bar's "Claude 100%" and "Codex 4%"); a screen reader
// hears the provider's name.
export function SubscriptionDials({ snapshot, now }: { snapshot: Snapshot; now: number }) {
  const subscriptions = snapshot.subscriptions ?? [], keySpend = snapshot.key_spend ?? [];
  if (!subscriptions.length && !keySpend.length) return null;
  return <div className="subscription-dials" role="group" aria-label="Subscription and API key usage">
    {subscriptions.map((usage) => <SubscriptionDial key={usage.provider} usage={usage} now={now} />)}
    {keySpend.map((usage, index) => <KeySpendDial key={`${usage.provider}-${index}`} usage={usage} now={now} isSeveral={keySpend.length > 1} />)}
  </div>;
}
