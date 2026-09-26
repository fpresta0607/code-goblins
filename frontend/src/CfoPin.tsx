import type { Snapshot } from "./types";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { cfoSummary } from "./cfoSummary";

// The CFO pinned above the board's columns: what it needs from the Overlord,
// or how many goblins it supervises, and a button that opens its terminal.
// With no CFO to reach, neither registered nor starting in its native
// terminal, the button leads to the first-run page to start one.
export function CfoPin({ snapshot, onOpen, onStart }: { snapshot: Snapshot; onOpen: (source: HTMLElement) => void; onStart: () => void }) {
  const { asking, line } = cfoSummary(snapshot);
  const absent = !!snapshot.registration && !snapshot.cfo_terminal;
  return <div className={"cfo-pin" + (asking ? " asking" : "")} role="group" aria-label="CFO">
    <Avatar persona="cfo" />
    <span className="pin-copy"><strong>CFO</strong><span title={line}>{absent ? "No CFO is running" : line}</span></span>
    {absent ? <a className="button primary open-terminal" href="/start" onClick={(event) => { event.preventDefault(); onStart(); }}><Icon name="play" />Start the CFO</a>
      : <button className="primary open-terminal" onClick={(event) => onOpen(event.currentTarget)}><Icon name="terminal" />Open terminal</button>}
  </div>;
}
