import { ConnectorGroup } from "./connector-group";
import { levelSummary, needsLook, countWords } from "./connector-rows";
import { ConnectorTile } from "./connector-tile";
import type { ConnectorViewProps } from "./connector-view-props";

// The page's home: a tile for each of the three levels, and Needs a look, every
// item of any level whose status is trouble, each opening its place.
export function ConnectorHome({ snapshot, rows, now, actions, onOpen }: ConnectorViewProps) {
  const summary = levelSummary(snapshot);
  const attention = needsLook(rows);
  return <>
    <div className="connector-tiles">
      <ConnectorTile level="pc" step="Level 1" title="This PC and its accounts" about={summary.pc} place={{ kind: "pc" }} onOpen={onOpen} />
      <ConnectorTile level="project" step="Level 2" title={countWords(snapshot.projects.length, "project")} about={summary.projects} place={{ kind: "projects" }} onOpen={onOpen} />
      <ConnectorTile level="goblin" step="Level 3" title={countWords(snapshot.goblins.length, "goblin")} about={summary.goblins} place={{ kind: "goblins" }} onOpen={onOpen} />
    </div>
    {attention.length > 0
      ? <ConnectorGroup title="Needs a look" why="Only what failed or is out of place, from every level." rows={attention} now={now} actions={actions} showsOrigin onOpen={onOpen} />
      : <p className="connector-empty">Nothing needs a look.</p>}
  </>;
}
