import { ago } from "./browser-logins";
import { ConnectorGroup } from "./connector-group";
import { ConnectorLevelHead } from "./connector-level-head";
import { groupByGroup } from "./connector-rows";
import { hasChecked } from "./connector-status";
import type { ConnectorViewProps } from "./connector-view-props";

// This PC and its accounts, checked once for the machine: its CLI tools and
// credential names, what is built in, the harness accounts and the servers in
// their user config.
export function ConnectorPcView({ snapshot, rows, now, actions }: ConnectorViewProps) {
  const { machine } = snapshot;
  const groups = [...groupByGroup(rows.filter((row) => row.level === "pc"), "pc"), ...groupByGroup(rows.filter((row) => row.level === "account"), "account")];
  return <>
    <ConnectorLevelHead text={hasChecked(machine.checkedAt) ? "This PC checked " + ago(machine.checkedAt, now) : "This PC has not been checked yet"} refresh={{ level: "machine", scope: "" }}
      refreshLabel="Refresh this PC" isChecking={machine.isChecking} actions={actions} />
    {machine.error && <p className="connector-note is-warning"><strong>Check failed</strong> {machine.error}</p>}
    {groups.map((group) => <ConnectorGroup key={group.group} title={group.title} why={group.why} rows={group.rows} now={now} actions={actions} />)}
  </>;
}
