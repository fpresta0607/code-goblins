import type { Snapshot } from "./types";
import { Avatar } from "./Avatar";
import { ConnectorMark } from "./ConnectorMark";
import { Disclosure } from "./Disclosure";
import { Icon } from "./Icon";
import { stillHeld } from "./afk";
import { useAfkActions } from "./afk-actions";
import { AfkHeldList } from "./afk-held";
import { cfoSummary } from "./cfoSummary";
import { harnessMark } from "./connectors";
import { harnessTip } from "./workflow";
import "./cfo-pin.css";

// The CFO pinned above the board's columns: a plain bar, a card like the
// columns under it, that says how many goblins the CFO supervises, with its
// terminal a click away on its icon button and on its portrait. While
// something waits on the Overlord the bar says nothing of what it is: Open
// Command Center appears on it and glows, with how many wait, and that is the
// whole signal. With no CFO running, which he sees only after choosing the
// board without one, its button leads back to the first-run page to start
// one. A CFO still starting has not registered yet: Claude Code registers
// through its SessionStart hook after onboarding and sign-in, and a Codex or
// pi CFO when its first prompt runs cfo register.
// The mark of the harness the registered CFO runs sits beside its portrait,
// with the model of its newest session in that harness in its tip. While AFK
// mode is on nothing glows: the bar says since when and how much was decided
// and held, and lists under itself what is held for him, each item a click
// from the Command Center.
export function CfoPin({ snapshot, onOpen, onCommand, onStart }: { snapshot: Snapshot; onOpen: (source: HTMLElement) => void; onCommand: () => void; onStart: () => void }) {
  const { waiting, line } = cfoSummary(snapshot);
  const absent = !snapshot.cfo_runs;
  const shown = absent ? "No CFO is running." : snapshot.cfo_starting ? "Starting: answer anything it asks in its terminal." : line;
  const terminal = "Open the CFO's terminal";
  const harness = snapshot.cfo_harness;
  const model = snapshot.sessions.filter((session) => session.role === "cfo" && session.harness === harness).at(-1)?.model || "";
  const held = stillHeld(snapshot.afk);
  const { answer } = useAfkActions();
  return <div className="cfo-pin">
    <div className="cfo-rest" role="group" aria-label="CFO">
      {absent
        ? <span className="cfo-rest-portrait"><Avatar persona="cfo" /></span>
        : <button className="cfo-rest-portrait" aria-label={terminal} data-tip={terminal} data-tip-align="start" onClick={(event) => onOpen(event.currentTarget)}><Avatar persona="cfo" /></button>}
      {harness && !absent && <ConnectorMark mark={harnessMark(harness)} label={harnessTip(harness, model, "")} />}
      <p title={shown}>{shown}</p>
      {absent && <button className="labelled-button primary" onClick={onStart}><Icon name="play" />Start the CFO</button>}
      {!absent && waiting > 0 && <button className="cfo-command" aria-label={"Open Command Center: " + waiting + " waiting on you"} onClick={onCommand}>Open Command Center<span className="cfo-command-count" aria-hidden="true">{waiting}</span></button>}
      {!absent && <button className="icon-button raised" aria-label={terminal} data-tip={terminal} data-tip-align="end" onClick={(event) => onOpen(event.currentTarget)}><Icon name="terminal" /></button>}
      {snapshot.afk.state === "on" && <Disclosure kind="afk-held-panel" defaultOpen={held.length > 0} title={<>Held for you <span className="column-count">{held.length}</span></>}>
        <AfkHeldList held={held} tasks={snapshot.tasks} onOpen={answer} />
      </Disclosure>}
    </div>
  </div>;
}
