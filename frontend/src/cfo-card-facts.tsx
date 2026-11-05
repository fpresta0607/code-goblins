import type { Snapshot } from "./types";
import { Icon } from "./Icon";
import { AfkOnDialog } from "./afk-on-dialog";
import { AfkToggle } from "./afk-toggle";
import { CfoHarnessMark } from "./cfo-harness-mark";
import { cfoDoor, useChatStyle } from "./chat-style";
import { messagesQuery } from "./session-messages";
import { SubscriptionDials } from "./subscription-dials";
import { useAfkToggle } from "./useAfkToggle";

// What the board's CFO bar has beside its words, on the CFO's card on the
// canvas and in the list the canvas becomes at phone width: the weekly
// allowance dials, its AFK switch beside the button that opens it, and its
// harness mark.
export function CfoCardFacts({ snapshot, now, onOpenCfo }: { snapshot: Snapshot; now: number; onOpenCfo: (source: HTMLElement) => void }) {
  const [style] = useChatStyle();
  const door = cfoDoor(style, !!messagesQuery(snapshot));
  const toggle = useAfkToggle(snapshot.afk, snapshot.instance);
  return <>
    <SubscriptionDials snapshot={snapshot} now={now} />
    <div className="cfo-pin-end">
      <AfkToggle afk={snapshot.afk} toggle={toggle} />
      {toggle.isAsking && <AfkOnDialog pending={toggle.pending} problem={toggle.problem} onTurnOn={toggle.turnOn} onClose={toggle.closeQuestion} />}
      <button className="icon-button" aria-label={door.label} data-tip={door.label} onClick={(event) => onOpenCfo(event.currentTarget)}><Icon name={door.icon} /></button>
    </div>
    {snapshot.cfo_harness && <span className="card-harness"><CfoHarnessMark snapshot={snapshot} /></span>}
  </>;
}
