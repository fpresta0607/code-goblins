import { Avatar } from "./Avatar";
import { usageWords, type Connector, type ConnectorUse } from "./connector-list";
import { connectionAction, connectionTrouble } from "./connections";
import { Icon } from "./Icon";
import { goblinName } from "./task-words";
import type { Task } from "./types";
import { personaFor } from "./workflow";

// What a repair's button says: the sign-in it opens, or the variable it
// stores from the clipboard, named whole since it reads what he copied.
function repairWords(action: string, name: string): string {
  if (action.startsWith("store:")) return connectionAction(action, name);
  return action === "cli" ? "CLI sign-in" : "Sign in";
}

// One goblin on a connector's page: who it is, what its check said of the
// connector, the repairs a failing one offers, and Connect or Disconnect
// where the board can give it the connector or take it back. A probe that
// succeeded says what it observed and when, since a separate probe proves
// nothing about the running session, and a restart the goblin needs says so.
export function ConnectorGoblin({ connector, use, task, isBusy, isChanging, onFix, onGive }: {
  connector: Connector; use: ConnectorUse; task: Task; isBusy: boolean; isChanging: boolean;
  onFix: (action: string) => void; onGive: (isGiving: boolean) => void;
}) {
  const name = goblinName(task);
  const { entry } = use;
  const trouble = use.isCarried ? connectionTrouble(entry) : "";
  // Carried, checked and without trouble, which the list marks with a check.
  const isWorking = use.isCarried && !trouble && entry.status !== "withheld" && entry.status !== "checking";
  return <li className="connector-goblin">
    <Avatar persona={personaFor(task)} small />
    <span className="connector-goblin-copy">
      <strong>{name}</strong>
      <span className={"connector-use" + (trouble ? " is-trouble" : isWorking ? " is-working" : "")}>{(trouble || isWorking) && <Icon name={trouble ? "warning" : "check"} />}<span>{usageWords(use)}{use.isCarried && entry.status === "probe_succeeded" && entry.checkedAt && <> Observed <time dateTime={entry.checkedAt}>{entry.checkedAt}</time>.</>}</span></span>
      {use.isCarried && entry.restartReason && <span className="connector-use">{entry.isRestartRequired && "Restart required. "}{entry.restartReason}</span>}
    </span>
    <span className="connector-goblin-actions">
      {trouble && entry.actions.map((action) => <button key={action} type="button" disabled={isBusy} aria-label={connectionAction(action, connector.name) + " for " + name} onClick={() => onFix(action)}>
        <Icon name={action.startsWith("store:") ? "key" : action === "cli" ? "terminal" : "external"} />{repairWords(action, connector.name)}
      </button>)}
      {entry.grant && <button type="button" className={use.isCarried ? undefined : "primary"} disabled={isChanging} aria-label={(use.isCarried ? "Disconnect " + connector.name + " from " : "Connect " + connector.name + " to ") + name} onClick={() => onGive(!use.isCarried)}>{use.isCarried ? "Disconnect" : "Connect"}</button>}
    </span>
  </li>;
}
