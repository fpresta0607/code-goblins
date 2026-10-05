import { useState } from "react";
import type { Snapshot } from "./types";
import { message, request } from "./api";
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
// one. A CFO that was closed, however it ended, is said to be closed, with
// Reopen as its one action, which brings it back as goblins does. A CFO still
// starting has not registered yet: Claude Code registers through its
// SessionStart hook after onboarding and sign-in, and a Codex or pi CFO when
// its first prompt runs cfo register.
// The mark of the harness the registered CFO runs sits beside its portrait,
// with the model of its newest session in that harness in its tip. While AFK
// mode is on nothing glows: the bar says since when and how much was decided
// and held, and lists under itself what is held for him once he opens the
// list, which never opens by itself, each item a click from the Command
// Center.
export function CfoPin({ snapshot, onOpen, onCommand, onStart }: { snapshot: Snapshot; onOpen: (source: HTMLElement) => void; onCommand: () => void; onStart: () => void }) {
  const { waiting, line } = cfoSummary(snapshot);
  const { answer } = useAfkActions();
  const [reopening, setReopening] = useState(false);
  const [failure, setFailure] = useState("");
  // The board shows the reopened CFO from its next snapshot, so a reopen
  // that worked has nothing more to do here.
  const reopen = async () => {
    setReopening(true);
    setFailure("");
    try {
      await request("/api/cfo/reopen", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: "{}" });
    } catch (error) {
      setFailure(message(error));
    } finally {
      setReopening(false);
    }
  };
  if (snapshot.cfo_closed) return <div className="cfo-pin">
    <div className="cfo-rest" role="group" aria-label="CFO">
      <span className="cfo-rest-portrait"><Avatar persona="cfo" /></span>
      <p>The CFO is closed. Goblins keep running.{failure && <span className="warning-text" role="alert"> {failure}</span>}</p>
      <button className="labelled-button primary" disabled={reopening} onClick={reopen}><Icon name="play" />{reopening ? "Reopening the CFO…" : "Reopen the CFO"}</button>
    </div>
  </div>;
  const absent = !snapshot.cfo_runs;
  const shown = absent ? "No CFO is running." : snapshot.cfo_starting ? "Starting: answer anything it asks in its terminal." : line;
  const terminal = "Open the CFO's terminal";
  const harness = snapshot.cfo_harness;
  const model = snapshot.sessions.filter((session) => session.role === "cfo" && session.harness === harness).at(-1)?.model || "";
  const held = stillHeld(snapshot.afk);
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
      {snapshot.afk.state === "on" && <Disclosure kind="afk-held-panel" title={<>Held for you <span className="column-count">{held.length}</span></>}>
        <AfkHeldList held={held} tasks={snapshot.tasks} onOpen={answer} />
      </Disclosure>}
    </div>
  </div>;
}
