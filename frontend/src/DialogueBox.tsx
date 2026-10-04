import type { ReactNode } from "react";
import { Avatar } from "./Avatar";
import type { Persona } from "./workflow";

// A goblin, or the CFO, coming up to the Overlord with something to say, drawn
// as a game dialogue box: the speaker's portrait, what it says, which names
// the speaker, and what he can do about it.
export function DialogueBox({ persona, label, actions, children }: { persona: Persona; label: string; actions: ReactNode; children: ReactNode }) {
  return <div className="dialogue" role="group" aria-label={label}>
    <div className="dialogue-box">
      <span className="dialogue-portrait"><Avatar persona={persona} /></span>
      <div className="dialogue-text">{children}</div>
      <div className="dialogue-actions">{actions}</div>
    </div>
  </div>;
}
