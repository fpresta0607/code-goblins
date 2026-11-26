import { ago } from "./browser-logins";
import { ConnectorGroup } from "./connector-group";
import { ConnectorLevelHead } from "./connector-level-head";
import { groupByGroup } from "./connector-rows";
import { hasChecked } from "./connector-status";
import type { ConnectorViewProps } from "./connector-view-props";
import { Disclosure } from "./Disclosure";

// One goblin, read from its own session record, showing only what differs
// from its project and this PC: the servers its session loaded or failed to
// load, the services it carries and those it is not given, each with Connect
// or Disconnect. A goblin whose session does not say what it loaded says so.
export function ConnectorGoblinView({ snapshot, rows, now, actions, nameOf, task }: ConnectorViewProps & { task: string }) {
  const goblin = snapshot.goblins.find((each) => each.task === task);
  if (!goblin) return <p className="connector-empty">This goblin is not running.</p>;
  const name = nameOf(task);
  const groups = groupByGroup(rows.filter((row) => row.level === "goblin" && row.task === task), "goblin");
  return <>
    <ConnectorLevelHead text={name + " · " + goblin.harness + (hasChecked(goblin.checkedAt) ? " · read from its session record " + ago(goblin.checkedAt, now) : " · not read yet")}
      refresh={{ level: "goblin", scope: task }} refreshLabel={"Refresh " + name} isChecking={goblin.isChecking} actions={actions} />
    {goblin.error && <p className="connector-note is-warning"><strong>Read failed</strong> {goblin.error}</p>}
    {goblin.note && <p className="connector-note">{goblin.note}</p>}
    {groups.length > 0
      ? groups.map((group) => <ConnectorGroup key={group.group} title={group.title} why={group.why} rows={group.rows} now={now} actions={actions} goblinName={name} />)
      : <p className="connector-note">Nothing differs: it has what its project and this PC give every goblin.</p>}
    <Disclosure kind="connector-same" title="Same as its project and this PC">
      <p>The CLI tools and credential names of this PC, the built-in connectors and the harness accounts are the same for every goblin, and so are the services and MCP servers its project declares, except where this page lists them above.</p>
    </Disclosure>
  </>;
}
