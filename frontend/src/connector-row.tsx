import { ConnectorLogo } from "./connector-logo";
import { toolWords } from "./connector-names";
import { placeKey, type Place } from "./connector-place";
import type { ConnectorRow as Row } from "./connector-rows";
import { checkedWords, isStale, repairLabel, repairWords, SCOPE_WORDS } from "./connector-status";
import { ConnectorTag } from "./connector-tag";
import { Disclosure } from "./Disclosure";
import { Icon } from "./Icon";
import type { ConnectorActions } from "./use-connectors";

// One row of the Connectors page, laid out the same whichever level it comes
// from: its logo and name, the scope it lives in, its status in a word with the
// reason and when it was checked, and what he can do to it. Only verified is
// green and only trouble is amber. A row that opens a place is a button on its
// name, which the whole row answers to, so the keyboard reaches it with Tab.
// While its level is checked again it says Rechecking, and past its level's
// life it says Stale. A goblin's row has no refresh: a goblin is read from its
// own record.
export function ConnectorRow({ row, now, showsOrigin = false, goblinName = "", actions, onOpen }: {
  row: Row; now: number; showsOrigin?: boolean; goblinName?: string; actions: ConnectorActions; onOpen?: (place: Place) => void;
}) {
  const { refresh } = row;
  const sub = [showsOrigin && row.origin, row.target, row.about].filter(Boolean).join(" · ");
  const isStaleNow = !row.isChecking && isStale(row.level, row.checkedAt, now);
  const tone = row.isChecking ? "quiet" : row.tone;
  const label = showsOrigin && row.origin ? row.name + ", " + row.origin : row.name;
  const isRepairable = row.level === "project" && !row.isChecking;
  return <li className={"connector-row" + (onOpen ? " is-open" : "")} data-place={onOpen ? placeKey(row.place) : undefined}>
    <div className="connector-name">
      {row.logoName && <ConnectorLogo name={row.logoName} />}
      <div>
        {onOpen ? <button type="button" className="connector-open" aria-label={label} onClick={() => onOpen(row.place)}>{row.name}</button> : <strong>{row.name}</strong>}
        {sub && <span className="connector-sub">{sub}</span>}
      </div>
    </div>
    <ConnectorTag tone={row.scope} text={SCOPE_WORDS[row.scope]} />
    <div className={"connector-state is-" + tone}>
      <b>{tone === "verified" && <Icon name="check" />}{row.isChecking ? "Rechecking..." : row.word}</b>
      {!row.isChecking && row.detail && <small>{row.detail}</small>}
      <small>{row.isChecking ? "in the background" : checkedWords(row.checkedAt, now)}</small>
      {isStaleNow && <span className="connector-stale">Stale</span>}
    </div>
    <div className="connector-actions">
      {isRepairable && row.actions.map((action) => <button key={action} type="button" className={action.startsWith("store:") ? undefined : "primary"} disabled={actions.isBusy}
        aria-label={repairLabel(action, row.name)} onClick={() => actions.fix(row.project, row.connection, action)}>{repairWords(action)}</button>)}
      {row.grant && <button type="button" className={row.grant === "grant" ? "primary" : undefined} disabled={actions.isBusy}
        aria-label={(row.grant === "grant" ? "Connect " + row.name + " to " : "Disconnect " + row.name + " from ") + goblinName} onClick={() => actions.grant(row.task, row.connection, row.grant === "grant")}>
        {row.grant === "grant" ? "Connect" : "Disconnect"}</button>}
      {refresh && <button type="button" className="connector-refresh" disabled={row.isChecking} aria-label={"Refresh " + row.name} onClick={() => actions.refresh(refresh)}><Icon name="refresh" /></button>}
    </div>
    {row.credentials.length > 0 && <ul className="connector-vars" aria-label={"Credential names of " + row.name}>
      {row.credentials.map((credential) => <li key={credential.name} className={credential.isResolved ? undefined : "is-missing"}><code>{credential.name}</code><span>{credential.isResolved ? "set" : "not set"}</span></li>)}
    </ul>}
    {row.tools.length > 0 && <div className="connector-tools">
      <Disclosure kind="connector-tool-group" title={<>Tools<span className="connector-count">{row.tools.length}</span></>}>
        <ul aria-label={"Tools of " + row.name}>{row.tools.map((tool) => <li key={tool}>{toolWords(tool, row.name)}</li>)}</ul>
      </Disclosure>
    </div>}
  </li>;
}
