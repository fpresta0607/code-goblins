import { useState } from "react";
import type { Snapshot } from "./types";
import { message, request } from "./api";
import { Avatar } from "./Avatar";
import { ConnectorMark } from "./ConnectorMark";
import { DialogueBox } from "./DialogueBox";
import { Icon } from "./Icon";
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
// start one. A CFO that was closed, however it ended, is said to be closed,
// with Reopen as its one action, which brings it back as goblins does. A CFO
// still starting has not registered yet: Claude Code registers through its
// SessionStart hook after its onboarding and sign-in, a Codex or pi CFO when
// its first prompt runs cfo register. The mark of the harness the registered
// CFO runs sits beside its portrait, with the model of its newest session in
// that harness in its tip.
export function CfoPin({ snapshot, onOpen, onCommand, onStart }: { snapshot: Snapshot; onOpen: (source: HTMLElement) => void; onCommand: () => void; onStart: () => void }) {
  const { asking, line } = cfoSummary(snapshot);
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
      <button className="pixel-button" disabled={reopening} onClick={reopen}><Icon name="play" />{reopening ? "Reopening the CFO…" : "Reopen the CFO"}</button>
    </div>
  </div>;
  const absent = !snapshot.cfo_runs;
  const shown = absent ? "No CFO is running." : snapshot.cfo_starting ? "Starting: answer anything it asks in its terminal." : line;
  const terminal = "Open the CFO's terminal";
  const harness = snapshot.cfo_harness;
  const model = snapshot.sessions.filter((session) => session.role === "cfo" && session.harness === harness).at(-1)?.model || "";
  const mark = harness && !absent ? <ConnectorMark mark={harnessMark(harness)} label={harnessTip(harness, model, "")} /> : undefined;
  if (!asking && !absent) return <div className="cfo-pin">
    <div className="cfo-rest" role="group" aria-label="CFO">
      <button className="cfo-rest-portrait" aria-label={terminal} data-tip={terminal} data-tip-align="start" onClick={(event) => onOpen(event.currentTarget)}><Avatar persona="cfo" /></button>
      {mark}
      <p title={shown}>{shown}</p>
      <button className="icon-button raised" aria-label={terminal} data-tip={terminal} data-tip-align="end" onClick={(event) => onOpen(event.currentTarget)}><Icon name="terminal" /></button>
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
