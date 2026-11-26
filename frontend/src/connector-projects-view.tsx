import { ConnectorGroup } from "./connector-group";
import { projectRows } from "./connector-rows";
import type { ConnectorViewProps } from "./connector-view-props";

// Every project, a row each, the busiest first as the supervisor lists them.
export function ConnectorProjectsView({ snapshot, now, actions, onOpen }: ConnectorViewProps) {
  return <ConnectorGroup title="Projects" why="Open one for its services, its MCP servers and its goblins." rows={projectRows(snapshot)} now={now} actions={actions} onOpen={onOpen} />;
}
