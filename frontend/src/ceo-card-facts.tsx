import type { Snapshot } from "./types";
import { Icon } from "./Icon";
import { AfkToggle } from "./afk-toggle";
import { CeoHarnessMark } from "./ceo-harness-mark";
import { ceoDoor, useChatStyle } from "./chat-style";
import { messagesQuery } from "./session-messages";
import { SubscriptionDials } from "./subscription-dials";
import type { AfkToggleState } from "./useAfkToggle";

// What the board's CEO bar has beside its words, on the CEO's card on the
// canvas and in the list the canvas becomes at phone width: the weekly
// allowance dials, its AFK switch beside the button that opens it, and its
// harness mark.
export function CeoCardFacts({ snapshot, now, afkToggle, onOpenCeo }: { snapshot: Snapshot; now: number; afkToggle: AfkToggleState; onOpenCeo: (source: HTMLElement) => void }) {
  const [style] = useChatStyle();
  const door = ceoDoor(style, !!messagesQuery(snapshot));
  return <>
    <SubscriptionDials snapshot={snapshot} now={now} />
    <div className="ceo-pin-end">
      <AfkToggle afk={snapshot.afk} toggle={afkToggle} />
      <button className="icon-button" aria-label={door.label} data-tip={door.label} onClick={(event) => onOpenCeo(event.currentTarget)}><Icon name={door.icon} /></button>
    </div>
    {snapshot.ceo_harness && <span className="card-harness"><CeoHarnessMark snapshot={snapshot} /></span>}
  </>;
}
