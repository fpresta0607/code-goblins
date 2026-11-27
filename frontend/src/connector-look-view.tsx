import { ConnectorNeedsLook, NEEDS_LOOK_FOCUS } from "./connector-needs-look";
import { needsLook } from "./connector-rows";
import type { ConnectorViewProps } from "./connector-view-props";

// Needs a look, every item of any scope whose status is trouble, each opening
// its place. Where the page is wide it shows beside the tree at the home.
export function ConnectorLookView({ rows, now, actions, onOpen }: ConnectorViewProps) {
  const attention = needsLook(rows);
  return attention.length > 0
    ? <ConnectorNeedsLook why="Only what failed or is out of place, from every scope." rows={attention} now={now} actions={actions} onOpen={onOpen} />
    : <p id={NEEDS_LOOK_FOCUS} className="connector-empty" tabIndex={-1}>Nothing needs a look.</p>;
}
