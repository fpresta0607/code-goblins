import { screenAddress } from "./screen-address";

// A card's Browser active opens what is active, in a new tab (#2559): the
// Screen view of the goblin's own browser while its goblin uses it (screen
// names its task, #2537), else the page it presents. A card with neither
// shows none.
export function BrowserActive({ name, screen, page }: { name: string; screen: string; page: string }) {
  if (!screen && !page) return null;
  const opens = screen ? name + "'s screen" : "the page " + name + " presents";
  return <button type="button" className="browser-indicator screen-open" aria-label={"Browser active: open " + opens + " in a new tab"}
    data-tip={screen ? "Open its screen in a new tab" : "Open the page it presents in a new tab"}
    onClick={() => window.open(screen ? screenAddress(screen) : page, "_blank", screen ? "noopener" : "noopener,noreferrer")}>Browser active</button>;
}
