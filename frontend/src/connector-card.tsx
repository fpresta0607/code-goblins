import { BrandGlyph } from "./brand-glyph";
import { connectorAbout, connectorLogo } from "./connectors";
import { connectionAction, connectionTrouble, type ConnectionEntry } from "./connections";
import { Icon } from "./Icon";

// One connector as a card, as Claude lists its connectors (the Overlord,
// 2026-10-13): the service's own logo on a tile, its name, one line on what it
// does, and one square button, + when the task does not carry it and a check
// when it does. A connector that is failing keeps a faint mark with its reason
// in the tooltip, and the repairs it offers beside it.
export function ConnectorCard({ entry, isCarried, isBusy, isChanging, onFix, onToggle }: {
  entry: ConnectionEntry; isCarried: boolean; isBusy: boolean; isChanging: boolean;
  onFix: (entry: ConnectionEntry, action: string) => void; onToggle: (entry: ConnectionEntry) => void;
}) {
  const logo = connectorLogo(entry.name);
  const about = connectorAbout(entry.name);
  const trouble = connectionTrouble(entry);
  const toggle = isCarried ? "Take " + entry.name + " back" : "Give " + entry.name;
  return <li className="connector-card">
    <span className="connector-logo" aria-hidden="true">{"brand" in logo ? <BrandGlyph brand={logo.brand} /> : <span className="connector-initial">{logo.initial}</span>}</span>
    <span className="connector-copy">
      <span className="connector-name"><strong data-tip={entry.name}>{entry.name}</strong>{trouble && <span className="connector-trouble" role="img" tabIndex={0} aria-label={trouble} data-tip={trouble}><Icon name="warning" /></span>}</span>
      {about && <span className="connector-about">{about}</span>}
    </span>
    {trouble && entry.actions.map((action) => <button key={action} type="button" className="icon-button connector-repair" disabled={isBusy} aria-label={connectionAction(action, entry.name)} data-tip={connectionAction(action, entry.name)} onClick={() => onFix(entry, action)}><Icon name={action.startsWith("store:") ? "key" : action === "cli" ? "terminal" : "external"} /></button>)}
    {entry.grant && <button type="button" className={"connector-toggle" + (isCarried ? " is-carried" : "")} aria-label={toggle} data-tip={toggle} disabled={isChanging} onClick={() => onToggle(entry)}><Icon name={isCarried ? "check" : "plus"} /></button>}
  </li>;
}
