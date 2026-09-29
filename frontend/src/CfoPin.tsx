import type { Snapshot } from "./types";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { cfoSummary } from "./cfoSummary";
import { CfoRestart } from "./cfo-restart";

// The CFO pinned above the board's columns: what it needs from the Overlord,
// or how many goblins it supervises, and a button that opens its terminal.
// With no CFO running, the button returns to setup.
export function CfoPin({ snapshot, onOpen, onStart }: { snapshot: Snapshot; onOpen: (source: HTMLElement) => void; onStart: () => void }) {
  const { asking, line } = cfoSummary(snapshot);
  const absent = !snapshot.cfo_runs;
  const shown = absent ? "No CFO is running" : snapshot.cfo_starting ? "Starting: check the CFO terminal for setup prompts" : line;
  return <div className={"cfo-pin" + (asking ? " asking" : "")} role="group" aria-label="CFO">
    <Avatar persona="cfo" />
    <span className="pin-copy"><strong>CFO</strong><span title={shown}>{shown}</span></span>
    {absent ? <button className="primary open-terminal" onClick={onStart}><Icon name="play" />Start the CFO</button>
      : <button className="primary open-terminal" onClick={(event) => onOpen(event.currentTarget)}><Icon name="terminal" />Open terminal</button>}
	{!absent && <CfoRestart instance={snapshot.instance} onRestarted={onOpen} />}
  </div>;
}
