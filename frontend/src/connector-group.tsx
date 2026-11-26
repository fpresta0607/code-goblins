import { ConnectorRow } from "./connector-row";
import type { Place } from "./connector-place";
import type { ConnectorRow as Row } from "./connector-rows";
import type { ConnectorActions } from "./use-connectors";

// A titled list of rows, with a line on why they are listed. With onOpen each
// row opens its place.
export function ConnectorGroup({ title, why = "", rows, now, actions, showsOrigin = false, goblinName = "", onOpen }: {
  title: string; why?: string; rows: Row[]; now: number; actions: ConnectorActions; showsOrigin?: boolean; goblinName?: string; onOpen?: (place: Place) => void;
}) {
  return <section className="connector-group" aria-label={title}>
    <h3>{title}<small>{rows.length}</small></h3>
    {why && <p className="connector-why">{why}</p>}
    <ul className="connector-rows">
      {rows.map((row) => <ConnectorRow key={row.key} row={row} now={now} showsOrigin={showsOrigin} goblinName={goblinName} actions={actions} onOpen={onOpen} />)}
    </ul>
  </section>;
}
