import { startTransition, Suspense, useEffect, useRef, useState } from "react";
import type { BoardActivity, Snapshot } from "./types";
import type { CommandFocus } from "./CommandCenter";
import type { Sent } from "./item-state";
import { message, reportToCfo } from "./api";
import { countedTitle, showBadge } from "./arrivals";
import { boardPage } from "./board-page";
import { CommandCenterButton } from "./command-center-button";
import { waitingItems } from "./commandQueue";

const CommandCenter = boardPage(() => import("./CommandCenter").then((module) => module.CommandCenter), "Command Center");

interface Opening { isInboxOpen: boolean; hasFocus: boolean }

// The Command Center's place in the bar: its button, which counts what waits
// on him, as the tab's title and the app's badge do, until the Command Center
// is first wanted, when he opens its inbox or something asks it to open an
// item. Its code arrives then and takes the button's place, the button
// showing until it has, open as he left it and with the focus it had.
export function CommandCenterMenu({ snapshot, connected, presentations, now, focus, onUnsent, onSent }: { snapshot: Snapshot; connected: boolean; presentations: BoardActivity[]; now: number; focus: CommandFocus | null; onUnsent: (unsent: boolean) => void; onSent?: (key: string, sent: Sent | null) => void }) {
  const summary = useRef<HTMLElement>(null);
  const [opening, setOpening] = useState<Opening | null>(null);
  if (focus && !opening) setOpening({ isInboxOpen: false, hasFocus: false });
  // A presentation reaches him only when its goblin asks him to watch it; a
  // goblin's own test run stays off the Command Center.
  const needing = waitingItems(snapshot).length + presentations.filter((event) => event.watch).length;
  const baseTitle = useRef(document.title);
  useEffect(() => {
    document.title = countedTitle(baseTitle.current, needing);
    showBadge(needing)?.catch((error: unknown) => reportToCfo("the app badge", message(error)));
  }, [needing]);
  useEffect(() => () => {
    document.title = baseTitle.current;
    showBadge(0)?.catch((error: unknown) => reportToCfo("the app badge", message(error)));
  }, []);
  const button = <details className="command-center-menu" onToggle={(event) => {
    const isInboxOpen = event.currentTarget.open;
    const hasFocus = document.activeElement === summary.current;
    startTransition(() => setOpening({ isInboxOpen, hasFocus }));
  }}><CommandCenterButton ref={summary} needing={needing} /></details>;
  return <Suspense fallback={button}>{opening
    ? <CommandCenter snapshot={snapshot} connected={connected} presentations={presentations} now={now} focus={focus} needing={needing} isInboxOpen={opening.isInboxOpen} hasFocus={opening.hasFocus} onUnsent={onUnsent} onSent={onSent} />
    : button}</Suspense>;
}
