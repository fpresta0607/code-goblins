import { ConnectorLogo } from "./connector-logo";
import { placeKey, type Place } from "./connector-place";
import type { ConnectorRow } from "./connector-rows";
import { checkedWords, isStale, LEVEL_WORDS, repairLabel, repairWords } from "./connector-status";
import { Icon } from "./Icon";
import type { ConnectorActions } from "./use-connectors";

// One card of Needs a look (#3002), the same parts in the same places whatever
// its level and kind: its logo, its name and where it is from, and its level;
// its status in a word with the reason; where it is declared and when it was
// checked; the credential names it declares, when it declares any; and every
// action in one row at its foot, Remove last. The name opens its place.
export function ConnectorLookCard({ row, now, actions, onOpen, onRemove }: {
  row: ConnectorRow; now: number; actions: ConnectorActions; onOpen: (place: Place) => void; onRemove: (row: ConnectorRow) => void;
}) {
  const { refresh } = row;
  const isStaleNow = !row.isChecking && isStale(row.level, row.checkedAt, now);
  const origin = row.origin || LEVEL_WORDS[row.level];
  const repairs = row.level === "project" && !row.isChecking ? row.actions : [];
  const signIns = repairs.filter((action) => !action.startsWith("store:"));
  const stores = repairs.filter((action) => action.startsWith("store:"));
  return <li className="look-card" data-place={placeKey(row.place)}>
    <div className="look-card-head">
      {row.logoName && <ConnectorLogo name={row.logoName} />}
      <div className="look-card-title">
        <button type="button" className="connector-open" aria-label={row.name + ", " + origin} onClick={() => onOpen(row.place)}>{row.name}</button>
        <span className="look-card-origin">{origin}</span>
      </div>
      <span className={"connector-level is-" + row.level}>{LEVEL_WORDS[row.level]}</span>
    </div>
    <p className="look-card-state"><b>{row.isChecking ? "Rechecking..." : row.word}</b>{!row.isChecking && row.detail && <span>{row.detail}</span>}</p>
    <p className="look-card-facts">
      {[row.about, row.target, row.isChecking ? "rechecking in the background" : checkedWords(row.checkedAt, now)].filter(Boolean).join(" · ")}
      {isStaleNow && <span className="connector-stale">Stale</span>}
    </p>
    {row.credentials.length > 0 && <ul className="look-card-vars" aria-label={"Credential names of " + row.name}>
      {row.credentials.map((credential) => <li key={credential.name} className={credential.isResolved ? undefined : "is-missing"}><code>{credential.name}</code>{credential.isResolved ? "set" : "not set"}</li>)}
    </ul>}
    <div className="look-card-actions">
      {signIns.map((action) => <button key={action} type="button" className="primary" disabled={actions.isBusy}
        aria-label={repairLabel(action, row.name)} onClick={() => actions.fix(row.project, row.connection, action)}>{repairWords(action)}</button>)}
      {stores.length > 0 && <details className="look-card-store">
        <summary>Store a key</summary>
        <ul>{stores.map((action) => <li key={action}><button type="button" disabled={actions.isBusy} aria-label={repairLabel(action, row.name)}
          onClick={() => actions.fix(row.project, row.connection, action)}>{action.slice("store:".length)}</button></li>)}</ul>
      </details>}
      <span className="look-card-end">
        {refresh && <button type="button" className="connector-refresh" disabled={row.isChecking} aria-label={"Refresh " + row.name} onClick={() => actions.refresh(refresh)}><Icon name="refresh" /></button>}
        <button type="button" className="look-card-remove" disabled={actions.isBusy} aria-label={"Remove " + row.name + ", " + origin} onClick={() => onRemove(row)}><Icon name="trash" />Remove</button>
      </span>
    </div>
  </li>;
}
