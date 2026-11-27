import { useEffect, useId, useRef } from "react";
import { createPortal } from "react-dom";
import { Icon } from "./Icon";

// Asks before Remove changes anything, naming what it will change. Cancel, the
// default, changes nothing. While the removal runs both choices wait; a
// refusal is said here, in full, and the dialog stays for him to read it. An
// item Remove cannot take away says where it lives, with only Close. Focus
// returns to what opened it, or to the element fallbackFocus names once a
// removal took that away.
export function RemoveConnectorDialog({ name, origin, words, canRemove, error, isRemoving, fallbackFocus, onRemove, onCancel }: {
  name: string; origin: string; words: string; canRemove: boolean; error: string; isRemoving: boolean; fallbackFocus: string; onRemove: () => void; onCancel: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId(), description = useId();
  useEffect(() => {
    const element = dialog.current;
    const source = document.activeElement;
    element?.showModal();
    element?.querySelector<HTMLButtonElement>(".remove-connector-cancel")?.focus();
    return () => {
      element?.close();
      if (source instanceof HTMLElement && source.isConnected) source.focus();
      else document.getElementById(fallbackFocus)?.focus();
    };
  }, [fallbackFocus]);
  return createPortal(<dialog ref={dialog} className="forget-login remove-connector" aria-labelledby={title} aria-describedby={description}
    onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); if (!isRemoving) onCancel(); }}>
    <h2 id={title}>Remove {name}?</h2>
    <p className="remove-connector-origin">{origin}</p>
    <p id={description}>{words}</p>
    {error && <p className="remove-connector-error" role="alert">Nothing changed: {error}</p>}
    <div className="forget-login-choices">
      <button type="button" className="remove-connector-cancel" disabled={isRemoving} onClick={onCancel}>{error || !canRemove ? "Close" : "Cancel"}</button>
      {!error && canRemove && <button type="button" className="forget-login-confirm" disabled={isRemoving} onClick={onRemove}><Icon name="trash" />{isRemoving ? "Removing..." : "Remove"}</button>}
    </div>
  </dialog>, document.body);
}
