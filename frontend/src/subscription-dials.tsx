import type { Snapshot } from "./types";
import { KeySpendDial } from "./key-spend-dial";
import { SubscriptionDial } from "./subscription-dial";

// The usage of each subscription and API key in use, a dial each, on the
// board's CFO bar and on the CFO's card on the canvas: a ring of the weekly
// allowance a subscription leaves, and the dollars an API key has spent
// against its own limit and window. The card marks each ring with its
// provider's brand; the bar draws no brand at all and only the usage (the
// Overlord, 2026-10-28: "remove the redundant claude or codex or pi icons in
// cfo banner in top left of board, only need usage"), so there two
// subscriptions' rings each name their provider in words.
export function SubscriptionDials({ snapshot, now, hasMarks = true }: { snapshot: Snapshot; now: number; hasMarks?: boolean }) {
  const subscriptions = snapshot.subscriptions ?? [], keySpend = snapshot.key_spend ?? [];
  if (!subscriptions.length && !keySpend.length) return null;
  return <div className="subscription-dials" role="group" aria-label="Subscription and API key usage">
    {subscriptions.map((usage) => <SubscriptionDial key={usage.provider} usage={usage} now={now} hasMark={hasMarks} isNamed={!hasMarks && subscriptions.length > 1} />)}
    {keySpend.map((usage, index) => <KeySpendDial key={`${usage.provider}-${index}`} usage={usage} now={now} isSeveral={keySpend.length > 1} hasMark={hasMarks} />)}
  </div>;
}
