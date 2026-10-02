import type { ReactNode } from "react";
import { Avatar } from "./Avatar";
import type { Persona } from "./workflow";

// A goblin, or the CFO, coming up to the Overlord with something to say, drawn
// as a game dialogue box: the speaker's portrait, the speaker's name on a tab
// coloured by what it is about (needs him, done or failed), what
// it says, and what he can do about it. A box whose words already name the
// speaker, such as an alert, has no tab. A portrait with an action is a
// button; a badge, such as the mark of the harness the speaker runs, sits
// beside it.
export type DialogueTone = "needs" | "done" | "failed";

export function DialogueBox({ persona, speaker, tone, label, portrait, badge, actions, children }: {
  persona: Persona; speaker?: string; tone: DialogueTone; label: string;
  portrait?: { label: string; onClick: (source: HTMLElement) => void };
  badge?: ReactNode;
  actions: ReactNode; children: ReactNode;
}) {
  const face = portrait
    ? <button className="dialogue-portrait" aria-label={portrait.label} data-tip={portrait.label} data-tip-align="start" onClick={(event) => portrait.onClick(event.currentTarget)}><Avatar persona={persona} /></button>
    : <span className="dialogue-portrait"><Avatar persona={persona} /></span>;
  return <div className={"dialogue " + tone} role="group" aria-label={label}>
    {speaker && <span className="dialogue-tab" aria-hidden="true">{speaker}</span>}
    <div className="dialogue-box">
      {badge ? <span className="dialogue-who">{face}{badge}</span> : face}
      <div className="dialogue-text">{children}</div>
      <div className="dialogue-actions">{actions}</div>
    </div>
  </div>;
}
