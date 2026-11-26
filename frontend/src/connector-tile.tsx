import { placeKey, type Place } from "./connector-place";
import type { ConnectorLevel } from "./connector-snapshot";

// One of the home page's three tiles: a level, what it holds and a way in.
export function ConnectorTile({ level, step, title, about, place, onOpen }: { level: ConnectorLevel; step: string; title: string; about: string; place: Place; onOpen: (place: Place) => void }) {
  return <button type="button" className="connector-tile" data-place={placeKey(place)} onClick={() => onOpen(place)}>
    <span className={"connector-level is-" + level}>{step}</span>
    <strong>{title}</strong>
    <span>{about}</span>
  </button>;
}
