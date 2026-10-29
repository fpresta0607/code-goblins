import { useState } from "react";
import { message, request } from "./api";
import { copyText } from "./clipboard";
import { inDesktopApp } from "./dictation";
import { Icon } from "./Icon";
import type { PathRenderer } from "./messageText";
import "./path-link.css";

const SAID_MS = 1400;
const FAILED_MS = 6000;

// The card a path link belongs to, which the supervisor reads the path from
// again, and the board's token.
export interface PathCard { kind: "credential" | "question" | "review" | "run"; id: string; instance: string }

// A local file or folder a card names (the Overlord, 2026-10-17: "if there is
// a folder, there should be a link to the open the file explorer folder on my
// PC"). In the desktop window it opens File Explorer at its folder, with a
// file selected, through the supervisor, which opens only a path the card
// itself names. A plain browser cannot open a folder on his PC, so there the
// link copies the path instead. A click that worked turns its folder into a
// check for a moment, and one that failed says why in a few muted words
// beside it. Beside a code value, which copies itself, it is the folder icon
// alone.
export function PathLink({ path, card, isIcon = false }: { path: string; card: PathCard; isIcon?: boolean }) {
  const [said, setSaid] = useState({ text: "", isFailure: false });
  const isDesktop = inDesktopApp();
  const say = (text: string, isFailure = false) => {
    setSaid({ text, isFailure });
    setTimeout(() => setSaid({ text: "", isFailure: false }), isFailure ? FAILED_MS : SAID_MS);
  };
  const open = async () => {
    if (!isDesktop) return copyText(path).then(() => say("Copied"), () => say("Not copied. Select the path and press Ctrl+C.", true));
    try {
      await request("/api/cards/open-path", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": card.instance }, body: JSON.stringify({ kind: card.kind, id: card.id, path }) });
      say("Opened in File Explorer");
    } catch (error: unknown) { say(message(error), true); }
  };
  return <><button type="button" className={"path-link" + (isIcon ? " icon" : "")} aria-label={isDesktop ? "Open " + path + " in File Explorer" : "Copy the path " + path} onClick={() => void open()}>
    <Icon name={said.text && !said.isFailure ? "check" : "folder"} />{!isIcon && <span>{path}</span>}
  </button>{said.text && <span className={said.isFailure ? "path-link-said" : "sr-only"} role="status">{said.text}</span>}</>;
}

export const pathLinks = (card: PathCard): PathRenderer => (path, key, isIcon) => <PathLink key={key} path={path} card={card} isIcon={isIcon} />;
