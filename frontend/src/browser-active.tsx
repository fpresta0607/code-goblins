// A card's Browser active opens what is active (#2559): while its goblin uses
// its own browser (#2537), onScreen shows that goblin's panel on its Screen
// view, the view the panel's switch and a chat's browser row show (#2930);
// else the page it presents, in a new tab. A card with neither shows none.
export function BrowserActive({ name, page, onScreen }: { name: string; page: string; onScreen?: () => void }) {
  if (onScreen) return <button type="button" className="browser-indicator screen-open" aria-label={"Browser active: show " + name + "'s screen"}
    data-tip="Show its screen in its panel" onClick={onScreen}>Browser active</button>;
  if (!page) return null;
  return <button type="button" className="browser-indicator screen-open" aria-label={"Browser active: open the page " + name + " presents in a new tab"}
    data-tip="Open the page it presents in a new tab" onClick={() => window.open(page, "_blank", "noopener,noreferrer")}>Browser active</button>;
}
