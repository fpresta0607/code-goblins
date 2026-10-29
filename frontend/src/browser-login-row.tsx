import { BrandGlyph } from "./brand-glyph";
import { loginDetail, loginName, type BrowserLogin } from "./browser-logins";
import { connectorLogo } from "./connectors";
import { Icon } from "./Icon";

// One browser login in a goblin's Connections, as a card like its
// connectors': its provider's logo, its name, signed in or out, what it
// serves and when it was used, and Sign out and Forget, which are disabled
// while a browser of it runs, as a muted line then says.
export function BrowserLoginRow({ login, now, isBusy, onSignOut, onForget }: {
  login: BrowserLogin; now: number; isBusy: boolean; onSignOut: () => void; onForget: () => void;
}) {
  const name = loginName(login);
  const logo = connectorLogo(login.provider);
  const isWaiting = isBusy || login.inUse;
  return <li className="connector-card">
    <span className="connector-logo" aria-hidden="true">{"brand" in logo ? <BrandGlyph brand={logo.brand} /> : <span className="connector-initial">{logo.initial}</span>}</span>
    <span className="connector-copy">
      <span className="connector-name browser-login-name"><strong>{name}</strong><span className={"browser-login-state " + (login.signedIn ? "is-signed-in" : "is-signed-out")}><Icon name={login.signedIn ? "check" : "clock"} />{login.signedIn ? "Signed in" : "Signed out"}</span></span>
      <span className="connector-about">{loginDetail(login, now)}</span>
      {login.inUse && <span className="connector-about">In use by a fetch, a goblin or a sign-in window</span>}
    </span>
    {login.signedIn && <button type="button" className="icon-button" disabled={isWaiting} aria-label={"Sign out of " + name} onClick={onSignOut}><Icon name="lock" /></button>}
    <button type="button" className="icon-button" disabled={isWaiting} aria-label={"Forget " + name} onClick={onForget}><Icon name="trash" /></button>
  </li>;
}
