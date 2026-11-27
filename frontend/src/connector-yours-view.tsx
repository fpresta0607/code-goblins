import { ago } from "./browser-logins";
import { ConnectorGroup } from "./connector-group";
import { ConnectorLevelHead } from "./connector-level-head";
import { groupByGroup, YOURS_GROUPS } from "./connector-rows";
import { hasChecked } from "./connector-status";
import { ConnectorTitle } from "./connector-title";
import type { ConnectorViewProps } from "./connector-view-props";

// Your Claude and Codex: the connectors of the Overlord's Claude account and
// the servers of his Claude and Codex user config, which his own sessions load
// and goblins start without. They are checked with this PC.
export function ConnectorYoursView({ snapshot, rows, now, actions }: ConnectorViewProps) {
  const { machine } = snapshot;
  const groups = groupByGroup(rows.filter((row) => row.scope === "yours"), YOURS_GROUPS);
  return <>
    <ConnectorTitle name="Your Claude and Codex" tone="yours" tag="Yours only" about="Connectors and servers of your own sessions. Goblins start without them." />
    <ConnectorLevelHead text={hasChecked(machine.checkedAt) ? "Checked with this PC " + ago(machine.checkedAt, now) : "Not checked yet: it is checked with this PC"} refresh={{ level: "machine", scope: "" }}
      refreshLabel="Refresh this PC" isChecking={machine.isChecking} actions={actions} />
    {machine.error && <p className="connector-note is-warning"><strong>Check failed</strong> {machine.error}</p>}
    {groups.length > 0
      ? groups.map((group) => <ConnectorGroup key={group.group} title={group.title} why={group.why} rows={group.rows} now={now} actions={actions} />)
      : <p className="connector-empty">Your Claude and Codex sessions have no connector or server of their own.</p>}
  </>;
}
