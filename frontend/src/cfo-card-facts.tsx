import type { Snapshot } from "./types";
import { Icon } from "./Icon";
import { CfoHarnessMark } from "./cfo-harness-mark";
import { SubscriptionDials } from "./subscription-dials";

// What the board's CFO bar has beside its words, on the CFO's card on the
// canvas and in the list the canvas becomes at phone width: the weekly
// allowance dials, the button that opens its terminal and its harness mark.
export function CfoCardFacts({ snapshot, now, onOpenCfo }: { snapshot: Snapshot; now: number; onOpenCfo: (source: HTMLElement) => void }) {
  const terminal = "Open the CFO's terminal";
  return <>
    <SubscriptionDials snapshot={snapshot} now={now} />
    <button className="icon-button" aria-label={terminal} data-tip={terminal} onClick={(event) => onOpenCfo(event.currentTarget)}><Icon name="terminal" /></button>
    {snapshot.cfo_harness && <span className="card-harness"><CfoHarnessMark snapshot={snapshot} /></span>}
  </>;
}
