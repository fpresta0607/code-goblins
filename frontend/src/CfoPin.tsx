import type { Snapshot } from "./types";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { cfoSummary } from "./cfoSummary";

// The CFO pinned above the board's columns: what it needs from the Overlord,
// or how many goblins it supervises, and a button that opens its terminal.
export function CfoPin({ snapshot, onOpen }: { snapshot: Snapshot; onOpen: (source: HTMLElement) => void }) {
  const { asking, line } = cfoSummary(snapshot);
  return <div className={"cfo-pin" + (asking ? " asking" : "")} role="group" aria-label="CFO">
    <Avatar persona="cfo" />
    <span className="pin-copy"><strong>CFO</strong><span title={line}>{line}</span></span>
    <button className="primary open-terminal" onClick={(event) => onOpen(event.currentTarget)}><Icon name="terminal" />Open terminal</button>
  </div>;
}
