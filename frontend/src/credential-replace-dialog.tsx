import { useEffect, useId, useRef } from "react";
import { Icon } from "./Icon";
import { listNames } from "./credentials";

// Asks before a save, or a terminal, replaces values the project's scope
// already holds. Cancel, the default, stores nothing and leaves every pasted
// value in its field.
export function CredentialReplaceDialog({ names, project, action, onReplace, onCancel }: {
  names: string[]; project: string; action: "save" | "run"; onReplace: () => void; onCancel: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId(), description = useId();
  useEffect(() => {
    const element = dialog.current;
    const source = document.activeElement;
    element?.showModal();
    element?.querySelector<HTMLButtonElement>(".credential-replace-cancel")?.focus();
    return () => { element?.close(); if (source instanceof HTMLElement && source.isConnected) source.focus(); };
  }, []);
  const several = names.length > 1;
  return <dialog ref={dialog} className="credential-replace" aria-labelledby={title} aria-describedby={description}
    onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onCancel(); }}>
    <h2 id={title}>Replace a stored credential?</h2>
    <p id={description}>
      {listNames(names)} already {several ? "have values" : "has a value"} for {project}. {action === "save" ? "Saving" : "Typing it in the terminal"} replaces {several ? "them" : "it"}, and running {project} goblins get the new {several ? "ones" : "one"}.
    </p>
    <div className="credential-replace-choices">
      <button type="button" className="credential-replace-cancel" onClick={onCancel}><Icon name="close" />Cancel</button>
      <button type="button" className="credential-replace-confirm" onClick={onReplace}><Icon name="refresh" />{action === "save" ? "Replace and save" : "Replace and run"}</button>
    </div>
  </dialog>;
}
