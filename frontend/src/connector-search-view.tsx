import { ConnectorGroup } from "./connector-group";
import { countWords, groupByLevel, searchRows } from "./connector-rows";
import { LEVEL_WORDS } from "./connector-status";
import type { ConnectorViewProps } from "./connector-view-props";

// A search over the whole snapshot, in the browser: every level's matches
// grouped by level, each opening its place. Same-named items of different
// projects say their project and target. Nothing is read or checked to
// answer it.
export function ConnectorSearchView({ rows, now, actions, onOpen, search }: ConnectorViewProps & { search: string }) {
  const found = searchRows(rows, search);
  if (found.length === 0) return <p className="connector-empty">Nothing matches "{search.trim()}".</p>;
  return <>
    {groupByLevel(found).map(({ level, rows: matches }) => <ConnectorGroup key={level} title={LEVEL_WORDS[level]} rows={matches} now={now} actions={actions} showsOrigin onOpen={onOpen} />)}
    <p className="connector-empty">{countWords(found.length, "match", "matches")}, searched in the saved snapshot: nothing was checked to answer it.</p>
  </>;
}
