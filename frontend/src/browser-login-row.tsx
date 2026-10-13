import { loginDetail, loginName, type BrowserLogin } from "./browser-logins";
import { Icon } from "./Icon";

// One browser login in a goblin's Connections: its provider's letter, its
// name, signed in or out, what it serves and when it was used, and Sign
// out and Forget, which wait while a browser of it runs.
export function BrowserLoginRow({ login, now, isBusy, onSignOut, onForget }: {
  login: BrowserLogin; now: number; isBusy: boolean; onSignOut: () => void; onForget: () => void;
}) {
  const name = loginName(login);
  const isWaiting = isBusy || login.inUse;
  const waitTip = login.inUse ? "In use by a fetch, a goblin or a sign-in window. Try again once it closes." : "";
  return <li className="connection-row">
    <span className="browser-login-mark" aria-hidden="true">{login.provider.slice(0, 1).toUpperCase()}</span>
    <div className="connection-copy">
      <strong data-tip={name}>{name}</strong>
      <span className={"connection-verdict " + (login.signedIn ? "is-connected" : "is-signed-out")}><Icon name={login.signedIn ? "check" : "clock"} />{login.signedIn ? "Signed in" : "Signed out"}</span>
      <p>{loginDetail(login, now)}</p>
    </div>
    <div className="connection-actions">
      {login.signedIn && <button type="button" className="icon-button" disabled={isWaiting} aria-label={"Sign out of " + name} data-tip={waitTip || "Sign out"} onClick={onSignOut}><Icon name="lock" /></button>}
      <button type="button" className="icon-button" disabled={isWaiting} aria-label={"Forget " + name} data-tip={waitTip || "Forget"} onClick={onForget}><Icon name="trash" /></button>
    </div>
  </li>;
}
