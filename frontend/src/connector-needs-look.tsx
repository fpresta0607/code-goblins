import { useState } from "react";
import { message } from "./api";
import { ConnectorLookCard } from "./connector-look-card";
import type { Place } from "./connector-place";
import { removePlan } from "./connector-remove";
import type { ConnectorRow } from "./connector-rows";
import { LEVEL_WORDS } from "./connector-status";
import { RemoveConnectorDialog } from "./remove-connector-dialog";
import type { ConnectorActions } from "./use-connectors";

// Needs a look (#3002): every item of any level whose status is trouble, as
// cards laid out alike, each with Remove. Remove asks first; a removal that
// lands shows the snapshot it left, so the item leaves the list, and a refusal
// stays in the dialog with nothing changed.
export function ConnectorNeedsLook({ why, rows, now, actions, onOpen }: { why: string; rows: ConnectorRow[]; now: number; actions: ConnectorActions; onOpen: (place: Place) => void }) {
  const [removing, setRemoving] = useState<ConnectorRow>();
  const [isRemoving, setIsRemoving] = useState(false);
  const [error, setError] = useState("");
  const close = () => { setRemoving(undefined); setError(""); };
  const remove = async (row: ConnectorRow) => {
    setIsRemoving(true);
    try {
      await actions.remove(row);
      close();
    } catch (refusal: unknown) {
      setError(message(refusal));
    } finally { setIsRemoving(false); }
  };
  const plan = removing && removePlan(removing);
  return <section className="connector-group" aria-label="Needs a look">
    <h3>Needs a look<small>{rows.length}</small></h3>
    <p className="connector-why">{why}</p>
    <ul className="look-cards">
      {rows.map((row) => <ConnectorLookCard key={row.key} row={row} now={now} actions={actions} onOpen={onOpen} onRemove={(chosen) => { setError(""); setRemoving(chosen); }} />)}
    </ul>
    {removing && plan && <RemoveConnectorDialog name={removing.name} origin={removing.origin || LEVEL_WORDS[removing.level]} words={plan.words} canRemove={plan.canRemove} error={error} isRemoving={isRemoving}
      onCancel={close} onRemove={() => void remove(removing)} />}
  </section>;
}
