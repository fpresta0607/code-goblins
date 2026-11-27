import { ConnectorGroup } from "./connector-group";
import { countWords, projectRows } from "./connector-rows";
import { ConnectorTitle } from "./connector-title";
import type { ConnectorViewProps } from "./connector-view-props";

// Every project, a row each, those with a goblin running first.
export function ConnectorProjectsView({ snapshot, now, actions, onOpen }: ConnectorViewProps) {
  return <>
    <ConnectorTitle name="Projects" tone="local" tag="Local" about={countWords(snapshot.projects.length, "project") + ", one git repository each. Each project's goblins get what it declares, when granted."} />
    <ConnectorGroup title="Every project" why="Those with a goblin running first. Open one for its MCP servers, its services and its goblins." rows={projectRows(snapshot)} now={now} actions={actions} onOpen={onOpen} />
  </>;
}
