import { BrandGlyph } from "./brand-glyph";
import { loginDetail, loginName, type BrowserLogin } from "./browser-logins";
import { connectorLogo } from "./connectors";
import { Icon } from "./Icon";

// One browser login in a goblin's Connections, as a card like its
// connectors': its provider's logo, its name, signed in or out, what it
// serves and when it was used, and Sign out and Forget, which are disabled
// while a browser of it runs.
export function BrowserLoginRow({ login, now, isBusy, onSignOut, onForget }: {
  login: BrowserLogin; now: number; isBusy: boolean; onSignOut: () => void; onForget: () => void;
}) {
  const name = loginName(login);
  const logo = connectorLogo(login.provider);
  const isWaiting = isBusy || login.inUse;
  const waitTip = login.inUse ? "In use by a fetch, a goblin or a sign-in window. Try again once it closes." : "";
  return <li className="connector-card">
    <span className="connector-logo" aria-hidden="true">{"brand" in logo ? <BrandGlyph brand={logo.brand} /> : <span className="connector-initial">{logo.initial}</span>}</span>
    <span className="connector-copy">
      <span className="connector-name browser-login-name"><strong data-tip={name}>{name}</strong><span className={"browser-login-state " + (login.signedIn ? "is-signed-in" : "is-signed-out")}><Icon name={login.signedIn ? "check" : "clock"} />{login.signedIn ? "Signed in" : "Signed out"}</span></span>
      <span className="connector-about">{loginDetail(login, now)}</span>
    </span>
    {login.signedIn && <button type="button" className="icon-button" disabled={isWaiting} aria-label={"Sign out of " + name} data-tip={waitTip || "Sign out"} onClick={onSignOut}><Icon name="lock" /></button>}
    <button type="button" className="icon-button" disabled={isWaiting} aria-label={"Forget " + name} data-tip={waitTip || "Forget"} onClick={onForget}><Icon name="trash" /></button>
  </li>;
}
