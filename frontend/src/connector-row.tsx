import type { Connector } from "./connector-list";
import { ConnectorLogo } from "./connector-logo";
import { Icon } from "./Icon";

// One connector in the Connectors page's list, laid out as Claude lists its
// own (the Overlord, 2026-11-10): its logo and name, its type, and its state
// in one word, Verified with a check only when a check confirmed it and its
// trouble's word in amber when a check failed. Where he must act it is a
// button in the board's look, never coloured words: Sign in when a goblin
// must sign in, and Connect when no goblin carries it (the Overlord,
// 2026-11-11). A change waiting for a goblin's next terminal says so in one
// quiet line, and the connector's page says why. The whole row opens the
// connector, and each button opens it too, since which goblin signs in or
// takes it is chosen there.
export function ConnectorRow({ connector, onOpen }: { connector: Connector; onOpen: (connector: Connector, source: HTMLElement) => void }) {
  const action = connector.status === "available" ? "Connect" : connector.status === "sign-in" ? "Sign in" : "";
  return <li className="connector-row">
    <ConnectorLogo name={connector.reported} />
    <button type="button" className="connector-open" onClick={(event) => onOpen(connector, event.currentTarget)}>{connector.name}</button>
    <span className="connector-type">{connector.type}</span>
    <span className="connector-status">
      {action
        ? <button type="button" className="connector-connect" aria-label={(action === "Connect" ? "Connect " : "Sign in to ") + connector.name} onClick={(event) => onOpen(connector, event.currentTarget)}>{action}</button>
        : <span className={"connector-state is-" + connector.status}>{connector.status === "verified" && <Icon name="check" />}{connector.word}</span>}
    </span>
    {connector.isWaiting && <p className="connector-waiting">Applies at its next terminal</p>}
  </li>;
}
