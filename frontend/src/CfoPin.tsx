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
import { SubscriptionDial } from "./subscription-dial";
import "./cfo-pin.css";

// The CFO pinned above the board's columns: at rest a plain bar, a card like
// the columns under it, that says how many goblins are at work, with its
// terminal a click away on its icon button and on its portrait. While
// something waits on the Overlord it is the CFO's lantern box, its dialogue
// box with its name on the tab, which says nothing of what waits: its lantern
// Open Command Center carries how many wait, and that is the whole signal, as
// he chose on 2026-10-07 ("i liked the lantern box button"). With no CFO
// running, which he sees only after choosing the board without one, its button
// leads back to the first-run page to start one. A CFO that was closed,
// however it ended, is said to be closed, with Reopen as its one action, which
// brings it back as goblins does. Restart is in the CFO's panel, off the bar.
// A CFO still starting has not registered yet: Claude Code registers through
// its SessionStart hook after onboarding and sign-in, and a Codex or pi CFO
// when its first prompt runs cfo register. While the board cannot reach the
// CFO the bar says so, with why and the fix in its tip.
// The mark of the harness the registered CFO runs sits beside its portrait,
// with the model of its newest session in that harness in its tip, and the
// weekly allowance of each subscription in use is a dial beside the bar's
// buttons, the one place the board shows it. While AFK mode is on nothing
// prompts him and nothing is held for him: the bar stays at rest, says since
// when and how much was decided, and an outline Open Command Center with how
// many wait is its one way to whatever is there.
export function CfoPin({ snapshot, now, onOpen, onCommand, onStart }: { snapshot: Snapshot; now: number; onOpen: (source: HTMLElement) => void; onCommand: () => void; onStart: () => void }) {
  const { waiting, line } = cfoSummary(snapshot);
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
  const unreachable = !absent && !snapshot.cfo_starting && !!snapshot.registration;
  const shown = absent ? "No CFO is running." : snapshot.cfo_starting ? "Starting: answer anything it asks in its terminal." : unreachable ? "The board cannot reach the CFO." : line;
  const terminal = "Open the CFO's terminal";
  const command = "Open Command Center";
  const harness = snapshot.cfo_harness;
  const model = snapshot.sessions.filter((session) => session.role === "cfo" && session.harness === harness).at(-1)?.model || "";
  const away = snapshot.afk.state === "on";
  const mark = harness && !absent ? <ConnectorMark mark={harnessMark(harness)} label={harnessTip(harness, model, "")} /> : undefined;
  const status = <p {...unreachable ? { "data-tip": snapshot.registration, "data-tip-align": "start" } : {}}>{shown}</p>;
  const dials = !absent && !!snapshot.subscriptions?.length && <div className="subscription-dials" role="group" aria-label="Weekly subscription allowance">
    {snapshot.subscriptions.map((usage) => <SubscriptionDial key={usage.provider} usage={usage} now={now} />)}
  </div>;
  const waits = command + ": " + waiting + " waiting on you";
  if (!absent && waiting > 0 && !away) return <div className="cfo-pin">
    <DialogueBox persona="cfo" speaker="CFO" label="CFO" portrait={{ label: terminal, onClick: onOpen }} badge={mark}
      actions={<>
        {dials}
        <button className="pixel-button cfo-command" aria-label={waits} onClick={onCommand}>{command}<span className="cfo-command-count" aria-hidden="true">{waiting}</span></button>
        <button className="icon-button pixel-icon" aria-label={terminal} data-tip={terminal} data-tip-align="end" onClick={(event) => onOpen(event.currentTarget)}><Icon name="terminal" /></button>
      </>}>
      {status}
    </DialogueBox>
  </div>;
  return <div className="cfo-pin">
    <div className="cfo-rest" role="group" aria-label="CFO">
      {absent
        ? <span className="cfo-rest-portrait"><Avatar persona="cfo" /></span>
        : <button className="cfo-rest-portrait" aria-label={terminal} data-tip={terminal} data-tip-align="start" onClick={(event) => onOpen(event.currentTarget)}><Avatar persona="cfo" /></button>}
      {mark}
      {status}
      {dials}
      {absent && <button className="labelled-button primary" onClick={onStart}><Icon name="play" />Start the CFO</button>}
      {!absent && waiting > 0 && <button className="pixel-button outline cfo-command" aria-label={waits} onClick={onCommand}>{command}<span className="cfo-command-count" aria-hidden="true">{waiting}</span></button>}
      {!absent && <button className="icon-button raised" aria-label={terminal} data-tip={terminal} data-tip-align="end" onClick={(event) => onOpen(event.currentTarget)}><Icon name="terminal" /></button>}
    </div>
  </div>;
}
