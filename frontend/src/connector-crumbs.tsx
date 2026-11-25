import { placeKey, type Crumb, type Place } from "./connector-place";

// Where the page is, as the way back up: each step but the last is a button.
export function ConnectorCrumbs({ crumbs, onOpen }: { crumbs: Crumb[]; onOpen: (place: Place) => void }) {
  return <nav className="connector-crumbs" aria-label="Where you are">
    {crumbs.map((crumb, at) => at === crumbs.length - 1
      ? <span key={placeKey(crumb.place)} className="here" aria-current="page">{crumb.label}</span>
      : <span key={placeKey(crumb.place)} className="connector-crumb"><button type="button" onClick={() => onOpen(crumb.place)}>{crumb.label}</button><span aria-hidden="true">›</span></span>)}
  </nav>;
}
