import { ConnectorGroup } from "./connector-group";
import { goblinRows } from "./connector-rows";
import type { ConnectorViewProps } from "./connector-view-props";

// Every goblin that runs, a row each, each read from its own session record.
export function ConnectorGoblinsView({ snapshot, now, actions, nameOf, onOpen }: ConnectorViewProps) {
  return <ConnectorGroup title="Goblins" why="Each read from its own session record." rows={goblinRows(snapshot, nameOf)} now={now} actions={actions} onOpen={onOpen} />;
}
