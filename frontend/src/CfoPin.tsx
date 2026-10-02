import type { Snapshot } from "./types";
import { Avatar } from "./Avatar";
import { ConnectorMark } from "./ConnectorMark";
import { DialogueBox } from "./DialogueBox";
import { Disclosure } from "./Disclosure";
import { Icon } from "./Icon";
import { stillHeld } from "./afk";
import { useAfkActions } from "./afk-actions";
import { AfkHeldList } from "./afk-held";
import { cfoSummary } from "./cfoSummary";
import { harnessMark } from "./connectors";
import { harnessTip } from "./workflow";

// The CFO pinned above the board's columns. At rest it is a plain bar, a card
// like the columns under it: that all is quiet and how many goblins the CFO
// supervises, and its terminal, a click away on its icon button and on its
// portrait. Only while something waits on the Overlord is it the CFO's
// dialogue box, saying what it needs from him, with Open Command Center, where
// its questions are. With no CFO running, which he sees only after choosing
// the board without one, the box's button leads back to the first-run page to
// start one. A CFO still starting waits in its terminal for Claude Code's
// sign-in. The mark of the harness the registered CFO runs sits beside its
// portrait, with the model of its newest session in that harness in its tip.
// While AFK mode is on the bar stays plain whatever waits on him, says since
// when and how much was decided and held, and lists under itself what is held
// for him, each item a click from the Command Center.
export function CfoPin({ snapshot, onOpen, onCommand, onStart }: { snapshot: Snapshot; onOpen: (source: HTMLElement) => void; onCommand: () => void; onStart: () => void }) {
  const { asking, line } = cfoSummary(snapshot);
  const absent = !snapshot.cfo_runs;
  const shown = absent ? "No CFO is running." : snapshot.cfo_starting ? "Starting: sign in to Claude Code in its terminal." : line;
  const terminal = "Open the CFO's terminal";
  const harness = snapshot.cfo_harness;
  const model = snapshot.sessions.filter((session) => session.role === "cfo" && session.harness === harness).at(-1)?.model || "";
  const mark = harness && !absent ? <ConnectorMark mark={harnessMark(harness)} label={harnessTip(harness, model, "")} /> : undefined;
  const held = stillHeld(snapshot.afk);
  const { answer } = useAfkActions();
  if (!asking && !absent) return <div className="cfo-pin">
    <div className="cfo-rest" role="group" aria-label="CFO">
      <button className="cfo-rest-portrait" aria-label={terminal} data-tip={terminal} data-tip-align="start" onClick={(event) => onOpen(event.currentTarget)}><Avatar persona="cfo" /></button>
      {mark}
      <p title={shown}>{shown}</p>
      <button className="icon-button raised" aria-label={terminal} data-tip={terminal} data-tip-align="end" onClick={(event) => onOpen(event.currentTarget)}><Icon name="terminal" /></button>
      {snapshot.afk.state === "on" && <Disclosure kind="afk-held-panel" defaultOpen={held.length > 0} title={<>Held for you <span className="column-count">{held.length}</span></>}>
        <AfkHeldList held={held} tasks={snapshot.tasks} onOpen={answer} />
      </Disclosure>}
    </div>
  </div>;
  return <div className="cfo-pin">
    <DialogueBox persona="cfo" speaker="CFO" tone="needs" label="CFO" portrait={absent ? undefined : { label: terminal, onClick: onOpen }} badge={mark}
      actions={absent ? <button className="pixel-button" onClick={onStart}><Icon name="play" />Start the CFO</button> : <>
        <button className="pixel-button" onClick={onCommand}>Open Command Center</button>
        <button className="icon-button pixel-icon" aria-label={terminal} data-tip={terminal} data-tip-align="end" onClick={(event) => onOpen(event.currentTarget)}><Icon name="terminal" /></button>
      </>}>
      <p title={shown}>{shown}</p>
    </DialogueBox>
  </div>;
}
