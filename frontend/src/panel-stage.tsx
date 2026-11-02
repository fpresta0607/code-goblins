import type { ReactNode } from "react";

// PanelStage gives a session's messages and terminal the same panel box.
// Its Back to messages bar floats over the terminal without taking height
// from the grid.
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
