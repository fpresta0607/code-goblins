import { Icon } from "./Icon";
import type { RefreshTarget } from "./connector-rows";
import type { ConnectorActions } from "./use-connectors";

// The line at the head of a level's view: what it is and when it was checked,
// and a refresh of that level, which stays off while the level is checked.
export function ConnectorLevelHead({ text, refresh, refreshLabel, isChecking, actions }: {
  text: string; refresh: RefreshTarget; refreshLabel: string; isChecking: boolean; actions: ConnectorActions;
}) {
  return <div className="connector-level-head">
    <p>{text}</p>
    <button type="button" className="connector-refresh-level" disabled={isChecking} onClick={() => actions.refresh(refresh)}><Icon name="refresh" />{refreshLabel}</button>
  </div>;
}
