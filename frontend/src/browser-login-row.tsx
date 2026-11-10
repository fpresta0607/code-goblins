import { loginDetail, loginName, type BrowserLogin } from "./browser-logins";
import { ConnectorLogo } from "./connector-logo";
import { Icon } from "./Icon";

// One browser login on the Connectors page, as a row like a connector's: its
// provider's logo, its name, signed in or out, what it serves and when it
// was used, and Sign out and Forget, which are disabled while a browser of
// it runs, as a muted line then says.
export function BrowserLoginRow({ login, now, isBusy, onSignOut, onForget }: {
  login: BrowserLogin; now: number; isBusy: boolean; onSignOut: () => void; onForget: () => void;
}) {
  const name = loginName(login);
  const isWaiting = isBusy || login.inUse;
  return <li className="browser-login">
    <ConnectorLogo name={login.provider} />
    <span className="browser-login-copy">
      <span className="browser-login-name"><strong>{name}</strong><span className={"browser-login-state " + (login.signedIn ? "is-signed-in" : "is-signed-out")}><Icon name={login.signedIn ? "check" : "clock"} />{login.signedIn ? "Signed in" : "Signed out"}</span></span>
      <span className="browser-login-about">{loginDetail(login, now)}</span>
      {login.inUse && <span className="browser-login-about">In use by a fetch, a goblin or a sign-in window</span>}
    </span>
    <span className="browser-login-actions">
      {login.signedIn && <button type="button" disabled={isWaiting} aria-label={"Sign out of " + name} onClick={onSignOut}>Sign out</button>}
      <button type="button" disabled={isWaiting} aria-label={"Forget " + name} onClick={onForget}>Forget</button>
    </span>
  </li>;
}
