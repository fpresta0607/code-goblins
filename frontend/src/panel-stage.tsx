import type { ReactNode } from "react";

// PanelStage is the one box a session's panel shows its messages and its
// terminal in: the messages over the terminal laid out behind them, or the
// terminal stepped into their place, with a bar floating over its top edge
// that takes none of its height, whose Back to messages returns to them. The
// terminal keeps its box either way, so a step asks it for no new size.
export function PanelStage({ hidden, isSteppedIn, onBack, children }: {
  hidden: boolean; isSteppedIn: boolean; onBack: () => void; children: ReactNode;
}) {
  return <div className={"panel-stage" + (isSteppedIn ? " stepped-in" : "")} hidden={hidden}>
    {children}
    {isSteppedIn && <div className="stepped-in-bar">
      <button type="button" className="pixel-button" onClick={onBack}>Back to messages</button>
    </div>}
  </div>;
}
