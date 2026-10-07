import { useState } from "react";
import { Icon } from "./Icon";
import type { Snapshot } from "./types";
import { releaseBanner } from "./update-progress";

// The version the Overlord hid the banner for, kept per browser: hiding it is
// a viewing choice, so the next version shows it again.
const HIDDEN_KEY = "cfo-release-banner-hidden";

function readHidden(): string {
  try {
    return localStorage.getItem(HIDDEN_KEY) || "";
  } catch {
    return "";
  }
}

// The slim line under the board's header while a newer release waits. It
// points to the Update item, which is the way to update; on a board built
// from a clone it says the clone updates it instead.
export function ReleaseBanner({ snapshot, onOpen }: { snapshot: Snapshot; onOpen: (key: string) => void }) {
  const [hidden, setHidden] = useState(readHidden);
  const banner = releaseBanner(snapshot, hidden);
  if (!banner) return null;
  const hide = () => {
    setHidden(banner.tag);
    try {
      localStorage.setItem(HIDDEN_KEY, banner.tag);
    } catch {
      // Hidden for this page only.
    }
  };
  return <div className="release-banner" role="status">
    <img src="/assets/goblin-app.png" alt="" width="30" height="30" />
    {banner.kind === "update"
      ? <span className="what">Code Goblins {banner.tag} is ready <span>· you run {banner.installed}</span></span>
      : <span className="what">Code Goblins {banner.tag} is out <span>· this board was built from a clone: run git pull, then .\install.cmd -Dev in the clone</span></span>}
    {banner.page && <a href={banner.page} target="_blank" rel="noreferrer">What's new<Icon name="external" /></a>}
    {banner.kind === "update" && <button className="primary" type="button" onClick={() => onOpen(banner.item)}><Icon name="download" />Open</button>}
    <button className="icon-button" type="button" aria-label="Hide until the next version" data-tip="Hide until the next version" data-tip-align="end" onClick={hide}><Icon name="close" /></button>
  </div>;
}
