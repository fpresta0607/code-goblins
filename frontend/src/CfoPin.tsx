import type { Snapshot } from "./types";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { cfoSummary } from "./cfoSummary";

// The CFO pinned above the board's columns: what it needs from the Overlord,
// or how many goblins it supervises, and a button that opens its terminal.
// With no CFO running, which he sees only after choosing the board without
// one, the button leads back to the first-run page to start one.
export function CfoPin({ snapshot, onOpen, onStart }: { snapshot: Snapshot; onOpen: (source: HTMLElement) => void; onStart: () => void }) {
  const { asking, line } = cfoSummary(snapshot);
  const absent = !snapshot.cfo_runs;
  return <div className={"cfo-pin" + (asking ? " asking" : "")} role="group" aria-label="CFO">
    <Avatar persona="cfo" />
    <span className="pin-copy"><strong>CFO</strong><span title={line}>{absent ? "No CFO is running" : line}</span></span>
    {absent ? <button className="primary open-terminal" onClick={onStart}><Icon name="play" />Start the CFO</button>
      : <button className="primary open-terminal" onClick={(event) => onOpen(event.currentTarget)}><Icon name="terminal" />Open terminal</button>}
  </div>;
}
