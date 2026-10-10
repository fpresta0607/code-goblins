import { Icon } from "./Icon";
import { plainText } from "./task-words";
import "./afk.css";

// What the supervisor said when it refused the Overlord's AFK switch, as an
// error he can read: what did not happen, then the one short sentence the
// supervisor has for him, which says what to do instead. Why it refused goes
// to the CFO, never here. It stays until he closes it or presses the switch
// again. on says which way he pressed it.
export function AfkRefusal({ on, problem, onClose }: { on: boolean; problem: string; onClose?: () => void }) {
  if (!problem) return null;
  return <div className="afk-refusal" role="alert">
    <Icon name="warning" />
    <div className="afk-refusal-text">
      <strong>{on ? "AFK did not turn on" : "AFK did not turn off"}</strong>
      <p>{plainText(problem)}</p>
    </div>
    {onClose && <button className="icon-button" aria-label="Close the message" data-tip="Close" onClick={onClose}><Icon name="close" /></button>}
  </div>;
}
