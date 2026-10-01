import type { Snapshot } from "./types";
import { DialogueBox } from "./DialogueBox";
import { Icon } from "./Icon";
import { cfoSummary } from "./cfoSummary";

// The CFO pinned above the board's columns, as its dialogue box: what it needs
// from the Overlord, or that all is quiet and how many goblins it supervises,
// and Open Command Center, where its questions are, filled only while
// something waits on him. Its terminal is a click away, on its icon button and
// on its portrait. With no CFO running, which he sees only after choosing the
// board without one, the button leads back to the first-run page to start one.
// A CFO still starting waits in its terminal for Claude Code's sign-in.
export function CfoPin({ snapshot, onOpen, onCommand, onStart }: { snapshot: Snapshot; onOpen: (source: HTMLElement) => void; onCommand: () => void; onStart: () => void }) {
  const { asking, line } = cfoSummary(snapshot);
  const absent = !snapshot.cfo_runs;
  const shown = absent ? "No CFO is running." : snapshot.cfo_starting ? "Starting: sign in to Claude Code in its terminal." : line;
  const terminal = "Open the CFO's terminal";
  return <div className="cfo-pin">
    <DialogueBox persona="cfo" speaker="CFO" tone={asking || absent ? "needs" : "quiet"} label="CFO" portrait={absent ? undefined : { label: terminal, onClick: onOpen }}
      actions={absent ? <button className="pixel-button" onClick={onStart}><Icon name="play" />Start the CFO</button> : <>
        <button className={"pixel-button" + (asking ? "" : " outline")} onClick={onCommand}>Open Command Center</button>
        <button className="icon-button pixel-icon" aria-label={terminal} data-tip={terminal} data-tip-align="end" onClick={(event) => onOpen(event.currentTarget)}><Icon name="terminal" /></button>
      </>}>
      <p title={shown}>{shown}</p>
    </DialogueBox>
  </div>;
}
