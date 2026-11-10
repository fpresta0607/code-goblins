import type { Connector } from "./connector-list";
import { ConnectorLogo } from "./connector-logo";
import { Icon } from "./Icon";

// One connector in the Connectors page's list, laid out as Claude lists its
// own (the Overlord, 2026-11-10): its logo and name, its type, and its
// status, a check when a goblin carries it and it works, the word of its
// trouble when it fails for one, and Connect when no goblin carries it. The
// whole row opens the connector, and Connect opens it too, since which
// goblin takes it is chosen there.
export function ConnectorRow({ connector, onOpen }: { connector: Connector; onOpen: (connector: Connector, source: HTMLElement) => void }) {
  return <li className="connector-row">
    <ConnectorLogo name={connector.reported} />
    <button type="button" className="connector-open" onClick={(event) => onOpen(connector, event.currentTarget)}>{connector.name}</button>
    <span className="connector-type">{connector.type}</span>
    <span className="connector-status">
      {connector.status === "connected" && <span className="connector-state is-connected"><Icon name="check" /><span className="sr-only">Connected</span></span>}
      {connector.status === "trouble" && <span className="connector-state is-trouble"><Icon name="warning" />{connector.troubleWord}</span>}
      {connector.status === "checking" && <span className="connector-state">Checking</span>}
      {connector.status === "available" && <button type="button" className="connector-connect" aria-label={"Connect " + connector.name} onClick={(event) => onOpen(connector, event.currentTarget)}>Connect</button>}
    </span>
  </li>;
}
