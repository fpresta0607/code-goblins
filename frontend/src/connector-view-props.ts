import type { ConnectorActions } from "./use-connectors";
import type { Place } from "./connector-place";
import type { ConnectorRow } from "./connector-rows";
import type { ConnectorSnapshot } from "./connector-snapshot";

// What every view of the Connectors page is given: the snapshot, every row of
// it, the clock, what a click can do and how to open a place.
export interface ConnectorViewProps {
  snapshot: ConnectorSnapshot;
  rows: ConnectorRow[];
  now: number;
  actions: ConnectorActions;
  nameOf: (task: string) => string;
  onOpen: (place: Place) => void;
}
