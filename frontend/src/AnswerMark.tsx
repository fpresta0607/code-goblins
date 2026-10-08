import { Avatar } from "./Avatar";
import { Icon } from "./Icon";

const WORDS = { you: "You answered", cfo: "The CFO answered", away: "The CFO answered while you were away" } as const;

// Who answered a question, told apart at a glance: the crown for the
// Overlord, the CFO's face for the CFO, and the CFO's face with a moon for an
// answer it gave while he was away.
export function AnswerMark({ who }: { who: keyof typeof WORDS }) {
  return <span className={"answer-mark " + who} role="img" aria-label={WORDS[who]} data-tip={WORDS[who]}>
    {who === "you" ? <Icon name="crown" /> : <Avatar persona="cfo" small />}
    {who === "away" && <span className="away-badge"><Icon name="moon" /></span>}
  </span>;
}
