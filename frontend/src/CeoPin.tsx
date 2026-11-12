import type { Snapshot } from "./types";
import { Avatar } from "./Avatar";
import { DialogueBox } from "./DialogueBox";
import { Icon } from "./Icon";
import { ceoSummary } from "./ceoSummary";
import { SubscriptionDials } from "./subscription-dials";
import "./ceo-pin.css";
import { ReopenCeoButton } from "./reopen-ceo-button";
import { ActionsSpendHeader } from "./actions-spend-header";
import { AfkToggle } from "./afk-toggle";
import { ceoDoor, useChatStyle } from "./chat-style";
import { messagesQuery } from "./session-messages";
import type { AfkToggleState } from "./useAfkToggle";

// The CEO pinned above the board's columns: at rest a plain bar, a card like
// the columns under it, that says how many goblins are at work, with its
// chat or terminal, as he reads it, a click away on its icon button and on its
// portrait, and its AFK switch right beside that button in either form (the
// Overlord, 2026-11-05: "Put afk toggle in main header next to terminal
// button"). While
// something waits on the Overlord it is the CEO's lantern box, its dialogue
// box with its name on the tab, which says nothing of what waits: its lantern
// Open Command Center carries how many wait, and that is the whole signal, as
// he chose on 2026-10-07 ("i liked the lantern box button"). With no CEO
// running in a home with no saved harness, its button leads back to the
// first-run page to start one. A saved CEO that is not running offers Reopen.
// A CEO that was closed, however it ended, is said to be closed, with Reopen
// to bring it back as goblins does, and the AFK switch beside it only while it
// is not off, since no panel is left to turn it off from, or while his press
// still has an answer to show. Restart is in the CEO's panel, off the bar.
// A CEO still starting has not registered yet: Claude Code registers through
// its SessionStart hook after onboarding and sign-in, and a Codex or pi CEO
// when its first prompt runs goblins register. While the board cannot reach the
// CEO the bar says so, with why and the fix in its tip.
// The weekly allowance of each subscription in use is a dial beside the
// bar's buttons, its provider's icon inside its ring, as on the CEO's card on
// the canvas, and the bar draws no mark of the harness the CEO runs beside
// its portrait (the Overlord, 2026-10-28: "remove the redundant claude or
// codex or pi icons in cfo banner in top left of board, only need usage").
// While AFK mode is on nothing prompts him and nothing is held for him: the
// bar stays at rest, says since when and how much was decided, and an outline
// Open Command Center with how many wait is its one way to whatever is there.
export function CeoPin({ snapshot, now, afkToggle, onOpen, onCommand, onStart }: { snapshot: Snapshot; now: number; afkToggle: AfkToggleState; onOpen: (source: HTMLElement) => void; onCommand: () => void; onStart: () => void }) {
  const [style] = useChatStyle();
  const door = ceoDoor(style, !!messagesQuery(snapshot));
  const { waiting, line } = ceoSummary(snapshot);
  const afkSwitch = <AfkToggle afk={snapshot.afk} toggle={afkToggle} />;
  const shouldKeepSwitch = snapshot.afk.state !== "off" || afkToggle.pending || !!afkToggle.problem;
  const date = new Date(now);
  const month = `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}`;
  const isClosed = snapshot.ceo_closed || snapshot.ceo_configured && !snapshot.ceo_runs;
  const absent = !snapshot.ceo_runs;
  const unreachable = !absent && !snapshot.ceo_starting && !!snapshot.registration;
  const shown = absent ? "No CEO is running." : snapshot.ceo_starting ? "Starting: answer anything it asks in its terminal." : unreachable ? "The board cannot reach the CEO." : line;
  const command = "Open Command Center";
  const away = snapshot.afk.state === "on";
  const status = <p {...unreachable ? { "data-tip": snapshot.registration } : {}}>{shown}</p>;
  const dials = !absent && <SubscriptionDials snapshot={snapshot} now={now} />;
  const waits = command + ": " + waiting + " waiting on you";
  return <div className="ceo-pin">
    {isClosed ? <div className="ceo-rest" role="group" aria-label="CEO">
      <span className="ceo-rest-portrait"><Avatar persona="ceo" /></span>
      <p>{snapshot.ceo_closed ? "The CEO is closed." : "The CEO is not running."}</p>
      <ReopenCeoButton snapshot={snapshot} />
      <ActionsSpendHeader usage={snapshot.actions_spend} month={month} instance={snapshot.instance} now={now} />
      {shouldKeepSwitch && afkSwitch}
    </div> : !absent && waiting > 0 && !away ? <DialogueBox persona="ceo" speaker="CEO" label="CEO" portrait={{ label: door.label, onClick: onOpen }}
      actions={<>
        {dials}
        <ActionsSpendHeader usage={snapshot.actions_spend} month={month} instance={snapshot.instance} now={now} />
        <button className="pixel-button ceo-command" aria-label={waits} onClick={onCommand}>{command}<span className="ceo-command-count" aria-hidden="true">{waiting}</span></button>
        <div className="ceo-pin-end">
          {afkSwitch}
          <button className="icon-button pixel-icon" aria-label={door.label} data-tip={door.label} onClick={(event) => onOpen(event.currentTarget)}><Icon name={door.icon} /></button>
        </div>
      </>}>
      {status}
    </DialogueBox> : <div className="ceo-rest" role="group" aria-label="CEO">
      {absent
        ? <span className="ceo-rest-portrait"><Avatar persona="ceo" /></span>
        : <button className="ceo-rest-portrait" aria-label={door.label} data-tip={door.label} onClick={(event) => onOpen(event.currentTarget)}><Avatar persona="ceo" /></button>}
      {status}
      {dials}
      <ActionsSpendHeader usage={snapshot.actions_spend} month={month} instance={snapshot.instance} now={now} />
      {absent && <button className="labelled-button primary" onClick={onStart}><Icon name="play" />Start the CEO</button>}
      {absent && shouldKeepSwitch && afkSwitch}
      {!absent && waiting > 0 && <button className="pixel-button outline ceo-command" aria-label={waits} onClick={onCommand}>{command}<span className="ceo-command-count" aria-hidden="true">{waiting}</span></button>}
      {!absent && <div className="ceo-pin-end">
        {afkSwitch}
        <button className="icon-button raised" aria-label={door.label} data-tip={door.label} onClick={(event) => onOpen(event.currentTarget)}><Icon name={door.icon} /></button>
      </div>}
    </div>}
  </div>;
}
