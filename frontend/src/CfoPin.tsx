import type { Snapshot } from "./types";
import { Avatar } from "./Avatar";
import { DialogueBox } from "./DialogueBox";
import { Icon } from "./Icon";
import { cfoSummary } from "./cfoSummary";
import { SubscriptionDials } from "./subscription-dials";
import "./cfo-pin.css";
import { ReopenCfoButton } from "./reopen-cfo-button";
import { ActionsSpendHeader } from "./actions-spend-header";
import { AfkToggle } from "./afk-toggle";
import { cfoDoor, useChatStyle } from "./chat-style";
import { messagesQuery } from "./session-messages";

// The CFO pinned above the board's columns: at rest a plain bar, a card like
// the columns under it, that says how many goblins are at work, with its
// chat or terminal, as he reads it, a click away on its icon button and on its
// portrait, and its AFK switch right beside that button in either form (the
// Overlord, 2026-11-05: "Put afk toggle in main header next to terminal
// button"). While
// something waits on the Overlord it is the CFO's lantern box, its dialogue
// box with its name on the tab, which says nothing of what waits: its lantern
// Open Command Center carries how many wait, and that is the whole signal, as
// he chose on 2026-10-07 ("i liked the lantern box button"). With no CFO
// running in a home with no saved harness, its button leads back to the
// first-run page to start one. A saved CFO that is not running offers Reopen.
// A CFO that was closed, however it ended, is said to be closed, with Reopen
// to bring it back as goblins does, and the AFK switch beside it only while it
// is not off, since no panel is left to turn it off from. Restart is in the
// CFO's panel, off the bar.
// A CFO still starting has not registered yet: Claude Code registers through
// its SessionStart hook after onboarding and sign-in, and a Codex or pi CFO
// when its first prompt runs cfo register. While the board cannot reach the
// CFO the bar says so, with why and the fix in its tip.
// The weekly allowance of each subscription in use is a dial beside the
// bar's buttons, its provider's icon inside its ring, as on the CFO's card on
// the canvas, and the bar draws no mark of the harness the CFO runs beside
// its portrait (the Overlord, 2026-10-28: "remove the redundant claude or
// codex or pi icons in cfo banner in top left of board, only need usage").
// While AFK mode is on nothing prompts him and nothing is held for him: the
// bar stays at rest, says since when and how much was decided, and an outline
// Open Command Center with how many wait is its one way to whatever is there.
export function CfoPin({ snapshot, now, onOpen, onCommand, onStart }: { snapshot: Snapshot; now: number; onOpen: (source: HTMLElement) => void; onCommand: () => void; onStart: () => void }) {
  const [style] = useChatStyle();
  const door = cfoDoor(style, !!messagesQuery(snapshot));
  const { waiting, line } = cfoSummary(snapshot);
  const afkSwitch = <AfkToggle afk={snapshot.afk} instance={snapshot.instance} />;
  const date = new Date(now);
  const month = `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}`;
  if (snapshot.cfo_closed || snapshot.cfo_configured && !snapshot.cfo_runs) return <div className="cfo-pin">
    <div className="cfo-rest" role="group" aria-label="CFO">
      <span className="cfo-rest-portrait"><Avatar persona="cfo" /></span>
      <p>{snapshot.cfo_closed ? "The CFO is closed." : "The CFO is not running."}</p>
      <ReopenCfoButton snapshot={snapshot} />
      <ActionsSpendHeader usage={snapshot.actions_spend} month={month} instance={snapshot.instance} now={now} />
      {snapshot.afk.state !== "off" && afkSwitch}
    </div>
  </div>;
  const absent = !snapshot.cfo_runs;
  const unreachable = !absent && !snapshot.cfo_starting && !!snapshot.registration;
  const shown = absent ? "No CFO is running." : snapshot.cfo_starting ? "Starting: answer anything it asks in its terminal." : unreachable ? "The board cannot reach the CFO." : line;
  const command = "Open Command Center";
  const away = snapshot.afk.state === "on";
  const status = <p {...unreachable ? { "data-tip": snapshot.registration } : {}}>{shown}</p>;
  const dials = !absent && <SubscriptionDials snapshot={snapshot} now={now} />;
  const waits = command + ": " + waiting + " waiting on you";
  if (!absent && waiting > 0 && !away) return <div className="cfo-pin">
    <DialogueBox persona="cfo" speaker="CFO" label="CFO" portrait={{ label: door.label, onClick: onOpen }}
      actions={<>
        {dials}
        <ActionsSpendHeader usage={snapshot.actions_spend} month={month} instance={snapshot.instance} now={now} />
        <button className="pixel-button cfo-command" aria-label={waits} onClick={onCommand}>{command}<span className="cfo-command-count" aria-hidden="true">{waiting}</span></button>
        <div className="cfo-pin-end">
          {afkSwitch}
          <button className="icon-button pixel-icon" aria-label={door.label} data-tip={door.label} onClick={(event) => onOpen(event.currentTarget)}><Icon name={door.icon} /></button>
        </div>
      </>}>
      {status}
    </DialogueBox>
  </div>;
  return <div className="cfo-pin">
    <div className="cfo-rest" role="group" aria-label="CFO">
      {absent
        ? <span className="cfo-rest-portrait"><Avatar persona="cfo" /></span>
        : <button className="cfo-rest-portrait" aria-label={door.label} data-tip={door.label} onClick={(event) => onOpen(event.currentTarget)}><Avatar persona="cfo" /></button>}
      {status}
      {dials}
      <ActionsSpendHeader usage={snapshot.actions_spend} month={month} instance={snapshot.instance} now={now} />
      {absent && <button className="labelled-button primary" onClick={onStart}><Icon name="play" />Start the CFO</button>}
      {absent && snapshot.afk.state !== "off" && afkSwitch}
      {!absent && waiting > 0 && <button className="pixel-button outline cfo-command" aria-label={waits} onClick={onCommand}>{command}<span className="cfo-command-count" aria-hidden="true">{waiting}</span></button>}
      {!absent && <div className="cfo-pin-end">
        {afkSwitch}
        <button className="icon-button raised" aria-label={door.label} data-tip={door.label} onClick={(event) => onOpen(event.currentTarget)}><Icon name={door.icon} /></button>
      </div>}
    </div>
  </div>;
}
