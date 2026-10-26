import { useEffect, useId, useRef } from "react";
import { createPortal } from "react-dom";
import { forgetDetail, type BrowserLogin } from "./browser-logins";
import { Icon } from "./Icon";

// Asks before a login is forgotten, which deletes its browser profile.
// Cancel, the default, leaves the login as it is.
export function ForgetLoginDialog({ login, onForget, onCancel }: { login: BrowserLogin; onForget: () => void; onCancel: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId(), description = useId();
  useEffect(() => {
    const element = dialog.current;
    const source = document.activeElement;
    element?.showModal();
    element?.querySelector<HTMLButtonElement>(".forget-login-cancel")?.focus();
    return () => {
      element?.close();
      if (source instanceof HTMLElement && source.isConnected) {
        const summary = source.closest("details:not([open])")?.querySelector<HTMLElement>(":scope > summary");
        (summary || source).focus();
      }
    };
  }, []);
  return createPortal(<dialog ref={dialog} className="forget-login" aria-labelledby={title} aria-describedby={description}
    onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onCancel(); }}>
    <h2 id={title}>Forget this login?</h2>
    <p id={description}>{forgetDetail(login)}</p>
    <div className="forget-login-choices">
      <button type="button" className="forget-login-cancel" onClick={onCancel}>Cancel</button>
      <button type="button" className="forget-login-confirm" onClick={onForget}><Icon name="trash" />Forget</button>
    </div>
  </dialog>, document.body);
}
