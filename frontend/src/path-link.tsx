import { useState } from "react";
import { message, request } from "./api";
import { copyText } from "./clipboard";
import { inDesktopApp } from "./dictation";
import { Icon } from "./Icon";
import type { PathRenderer } from "./messageText";
import "./path-link.css";

const SAID_MS = 1400;

// The card a path link belongs to, which the supervisor reads the path from
// again, and the board's token.
export interface PathCard { kind: "credential" | "question" | "review" | "run"; id: string; instance: string }

// A local file or folder a card names (the Overlord, 2026-10-17: "if there is
// a folder, there should be a link to the open the file explorer folder on my
// PC"). In the desktop window it opens File Explorer at its folder, with a
// file selected, through the supervisor, which opens only a path the card
// itself names. A plain browser cannot open a folder on his PC, so there the
// link copies the path instead, and its tip says so.
export function PathLink({ path, card }: { path: string; card: PathCard }) {
  const [said, setSaid] = useState("");
  const isDesktop = inDesktopApp();
  const say = (text: string) => { setSaid(text); setTimeout(() => setSaid(""), SAID_MS); };
  const open = async () => {
    if (!isDesktop) return copyText(path).then(() => say("Copied"), () => setSaid("Not copied. Select the path and press Ctrl+C."));
    try {
      await request("/api/cards/open-path", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": card.instance }, body: JSON.stringify({ kind: card.kind, id: card.id, path }) });
      say("Opened in File Explorer");
    } catch (error: unknown) { setSaid(message(error)); }
  };
  return <button type="button" className="path-link" aria-label={isDesktop ? "Open " + path + " in File Explorer" : "Copy the path " + path}
    data-tip={said || (isDesktop ? "Open in File Explorer" : "Copy the path: File Explorer opens it only from the desktop window")} onClick={() => void open()}>
    <Icon name="folder" /><span>{path}</span>
  </button>;
}

export const pathLinks = (card: PathCard): PathRenderer => (path, key) => <PathLink key={key} path={path} card={card} />;
