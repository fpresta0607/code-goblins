import type { Connector } from "./connector-list";
import { ConnectorLogo } from "./connector-logo";
import { Icon } from "./Icon";

// One connector in the Connectors page's list, laid out as Claude lists its
// own (the Overlord, 2026-11-10): its logo and name, its type, and its state
// in one word, Verified with a check only when a check confirmed it, its
// trouble's word in amber when it fails for a goblin, and Connect when no
// goblin carries it. A change waiting for a goblin's next terminal says so in
// one quiet line, and the connector's page says why. The whole row opens the
// connector, and Connect opens it too, since which goblin takes it is chosen
// there.
export function ConnectorRow({ connector, onOpen }: { connector: Connector; onOpen: (connector: Connector, source: HTMLElement) => void }) {
  return <li className="connector-row">
    <ConnectorLogo name={connector.reported} />
    <button type="button" className="connector-open" onClick={(event) => onOpen(connector, event.currentTarget)}>{connector.name}</button>
    <span className="connector-type">{connector.type}</span>
    <span className="connector-status">
      {connector.status === "available"
        ? <button type="button" className="connector-connect" aria-label={"Connect " + connector.name} onClick={(event) => onOpen(connector, event.currentTarget)}>Connect</button>
        : <span className={"connector-state is-" + connector.status}>{connector.status === "verified" && <Icon name="check" />}{connector.word}</span>}
    </span>
    {connector.isWaiting && <p className="connector-waiting">Applies at its next terminal</p>}
  </li>;
}
