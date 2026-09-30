import { ConnectorMark } from "./ConnectorMark";
import { connectorMark } from "./connectors";
import { checkedTime, connectionAction, connectionStatus, type ConnectionEntry } from "./connections";
import { Icon } from "./Icon";

export function ConnectionRow({ entry, isBusy, onFix }: { entry: ConnectionEntry; isBusy: boolean; onFix: (entry: ConnectionEntry, action: string) => void }) {
  const isConnected = entry.status === "connected";
  const isProvided = entry.status === "provided";
  return <li className="connection-row">
    <ConnectorMark mark={connectorMark(entry.name, entry.kind === "credential" ? "credential" : "mcp")} label={entry.name} />
    <div className="connection-copy">
      <strong>{entry.name}</strong>
      <span className={"connection-verdict " + (isConnected ? "is-connected" : isProvided ? "is-provided" : "needs-attention")}><Icon name={isConnected ? "check" : isProvided ? "key" : "warning"} />{connectionStatus(entry.status)}</span>
      {entry.detail && <p>{entry.detail}</p>}
      <time dateTime={entry.checkedAt}>{checkedTime(entry.checkedAt)}</time>
    </div>
    {entry.actions.length > 0 && <div className="connection-actions">{entry.actions.map((action) => <button key={action} type="button" className="icon-button" disabled={isBusy} aria-label={connectionAction(action, entry.name)} data-tip={connectionAction(action, entry.name)} data-tip-align="end" onClick={() => onFix(entry, action)}><Icon name={action.startsWith("store:") ? "key" : action === "cli" ? "terminal" : "external"} /></button>)}</div>}
  </li>;
}
