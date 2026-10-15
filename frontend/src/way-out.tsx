import { Icon } from "./Icon";

// The two ways out of every Command Center card, at its top beside who asks,
// so he reaches them without scrolling: Skip clears the card and tells its
// asker to go on without it, and Other opens a box for his own answer, typed
// or dictated, which reaches the asker as his word. A card without its own
// box for Other, such as a question with an Other choice, passes no onOther.
export function WayOut({ skipTip, isOtherOpen, disabled, onSkip, onOther }: {
  skipTip: string; isOtherOpen: boolean; disabled: boolean; onSkip: () => void; onOther?: () => void;
}) {
  return <span className="way-out">
    <button type="button" className="card-skip" aria-label="Skip this card" data-tip={skipTip} disabled={disabled} onClick={onSkip}><Icon name="close" />Skip</button>
    {onOther && <button type="button" className={"card-other" + (isOtherOpen ? " chosen" : "")} aria-pressed={isOtherOpen} data-tip="Write or say your own answer" disabled={disabled} onClick={onOther}><Icon name="edit" />Other</button>}
  </span>;
}
