import { ago } from "./browser-logins";
import { ConnectorLevelHead } from "./connector-level-head";
import { countWords } from "./connector-rows";
import { goblinScope, pcScope } from "./connector-scopes";
import { hasChecked } from "./connector-status";
import { ConnectorTitle } from "./connector-title";
import type { ConnectorViewProps } from "./connector-view-props";
import { ConnectorWhere } from "./connector-where";

// One goblin, read from its own session record, from the widest scope down
// (#3010): what this PC gives every goblin, the services of its project it is
// granted, each with Disconnect, and those it is not, folded, each with
// Connect, and the servers its own session loaded, failed to load or is
// starting. A goblin whose session does not say what it loaded says so.
export function ConnectorGoblinView({ snapshot, rows, now, actions, nameOf, task }: ConnectorViewProps & { task: string }) {
  const goblin = snapshot.goblins.find((each) => each.task === task);
  if (!goblin) return <p className="connector-empty">This goblin is not running.</p>;
  const name = nameOf(task);
  const { granted, notGiven, session } = goblinScope(rows, task);
  const pc = pcScope(snapshot);
  const failed = session.filter((row) => row.group === "failed").length;
  const where = { now, actions, goblinName: name };
  return <>
    <ConnectorTitle name={name} tone="goblin" tag="Goblin" about={"in " + goblin.project} />
    <ConnectorLevelHead text={name + " · " + goblin.harness + (hasChecked(goblin.checkedAt) ? " · read from its session record " + ago(goblin.checkedAt, now) : " · not read yet")}
      refresh={{ level: "goblin", scope: task }} refreshLabel={"Refresh " + name} isChecking={goblin.isChecking} actions={actions} />
    {goblin.error && <p className="connector-note is-warning"><strong>Read failed</strong> {goblin.error}</p>}
    {goblin.note && <p className="connector-note">{goblin.note}</p>}
    <p className="connector-empty">What {name} can reach, from the widest scope down.</p>
    <ConnectorWhere {...where} tone="global" title="From this PC" count={String(pc.total)} why={"Every goblin gets these: " + pc.words + "."} />
    <ConnectorWhere {...where} tone="local" title={"Granted from " + goblin.project} count={granted.length + " of " + (granted.length + notGiven.length)}
      why="Services of the project's auth.json its task record gives it." rows={granted} empty="It is given none of them."
      fold={{ title: countWords(notGiven.length, "service") + " of " + goblin.project + " it is not given", rows: notGiven }} />
    <ConnectorWhere {...where} tone="goblin" title="Its session" count={failed + " failed"}
      why="Servers its session loaded, failed or is still starting, from its own record." rows={session} empty="Its session has recorded no servers yet." />
  </>;
}
