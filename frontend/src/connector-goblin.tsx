import { Avatar } from "./Avatar";
import { usageState, type Connector, type ConnectorUse } from "./connector-list";
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

// One goblin on a connector's page: who it is, its state in a word with the
// reason its check gave, the repairs a failing one offers, and Connect or
// Disconnect where the board can give it the connector or take it back. A
// verified one says Verified alone, since the page's check details say what
// its check did and did not prove (the Overlord, 2026-11-11), one that must
// sign in says why beside its Sign in, the board's green button as Connect
// is, and a change that waits for the goblin's next terminal says why in a
// quiet line.
export function ConnectorGoblin({ connector, use, task, isBusy, isChanging, onFix, onGive }: {
  connector: Connector; use: ConnectorUse; task: Task; isBusy: boolean; isChanging: boolean;
  onFix: (action: string) => void; onGive: (isGiving: boolean) => void;
}) {
  const name = goblinName(task);
  const { entry } = use;
  const isFailing = use.isCarried && !!connectionTrouble(entry);
  const state = usageState(use);
  return <li className="connector-goblin">
    <Avatar persona={personaFor(task)} small />
    <span className="connector-goblin-copy">
      <strong>{name}</strong>
      <span className={"connector-use is-" + state.tone}>{state.tone === "verified" && <Icon name="check" />}<span>
        {state.word && <span className="connector-word">{state.word}</span>}{state.word && state.reason && ". "}{state.reason}
      </span></span>
      {entry.restartReason && <span className="connector-waiting">{entry.restartReason}</span>}
    </span>
    <span className="connector-goblin-actions">
      {isFailing && entry.actions.map((action) => <button key={action} type="button" className={action.startsWith("store:") ? undefined : "primary"} disabled={isBusy} aria-label={connectionAction(action, connector.name) + " for " + name} onClick={() => onFix(action)}>
        <Icon name={action.startsWith("store:") ? "key" : action === "cli" ? "terminal" : "external"} />{repairWords(action, connector.name)}
      </button>)}
      {entry.grant && <button type="button" className={use.isCarried ? undefined : "primary"} disabled={isChanging} aria-label={(use.isCarried ? "Disconnect " + connector.name + " from " : "Connect " + connector.name + " to ") + name} onClick={() => onGive(!use.isCarried)}>{use.isCarried ? "Disconnect" : "Connect"}</button>}
    </span>
  </li>;
}
