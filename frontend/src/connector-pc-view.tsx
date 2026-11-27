import { ago } from "./browser-logins";
import { ConnectorGroup } from "./connector-group";
import { ConnectorLevelHead } from "./connector-level-head";
import { groupByGroup, PC_GROUPS } from "./connector-rows";
import { hasChecked } from "./connector-status";
import { ConnectorTitle } from "./connector-title";
import type { ConnectorViewProps } from "./connector-view-props";

// This PC, checked once for the machine: what every goblin on it gets with no
// grant, its command-line tools, its harness sign-ins, the credential names in
// its user environment and what is built in.
export function ConnectorPcView({ snapshot, rows, now, actions }: ConnectorViewProps) {
  const { machine } = snapshot;
  return <>
    <ConnectorTitle name="This PC" tone="global" tag="Global" about="Every goblin on this PC gets these, with no grant." />
    <ConnectorLevelHead text={hasChecked(machine.checkedAt) ? "This PC checked " + ago(machine.checkedAt, now) : "This PC has not been checked yet"} refresh={{ level: "machine", scope: "" }}
      refreshLabel="Refresh this PC" isChecking={machine.isChecking} actions={actions} />
    {machine.error && <p className="connector-note is-warning"><strong>Check failed</strong> {machine.error}</p>}
    {groupByGroup(rows.filter((row) => row.scope === "global"), PC_GROUPS).map((group) => <ConnectorGroup key={group.group} title={group.title} why={group.why} rows={group.rows} now={now} actions={actions} />)}
  </>;
}
