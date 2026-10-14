import type { ReactNode } from "react";
import { Avatar } from "./Avatar";
import { ClickFeedback } from "./click-feedback";
import { Icon } from "./Icon";
import { signInHeading, signInOutcome } from "./browser-signins";
import { onThisMachine } from "./credentials";
import { age } from "./presentation";
import { goblinName } from "./task-words";
import type { BrowserSignIn, Snapshot } from "./types";
import { personaFor } from "./workflow";
import "./credential-card.css";
import "./browser-signin-card.css";

export function BrowserSignInCard({ signin, snapshot, connected, isOpening, error, onOpen, pager }: {
  signin: BrowserSignIn; snapshot: Snapshot; connected: boolean; isOpening: boolean; error: string; onOpen: () => void; pager?: ReactNode;
}) {
  const isLocal = onThisMachine(window.location.hostname);
  const task = snapshot.tasks.find((candidate) => candidate.id === signin.task);
  const asker = signin.task ? task ? goblinName(task) : signin.task : "The CFO";
  if (signin.state === "expired") return <div className="done-card signin-closed" role="status">
    <Icon name="clock" /><h3>Closed</h3><p>{signInOutcome(signin)}</p>
    {pager && <div className="card-actions">{pager}</div>}
  </div>;
  const isWaiting = isOpening || signin.state === "opening" || signin.state === "open";
  return <article className="question-card credential-card" data-browser-signin={signin.id} aria-labelledby={"signin-" + signin.id}>
    <p className="asker"><Avatar persona={signin.task ? personaFor(task) : "cfo"} small /><span><strong>{asker}</strong> asks · waiting {age(signin.created_at).replace(/ ago$/, "")}</span></p>
    <header className="credential-heading">
      <h3 id={"signin-" + signin.id} tabIndex={-1}>{signInHeading(signin)}</h3>
      <p className="credential-why">Fetching keys needs the {signin.account} signed in. The fleet keeps the session afterwards, never your password.</p>
      <ul className="signin-facts" aria-label="Which login">
        <li className="chip">{signin.provider}</li><li className="chip">{signin.account}</li><li className="chip">Kept for {signin.project}</li>
      </ul>
      <ol className="credential-steps" aria-label="How to sign in">
        <li>Press Open the sign-in window</li>
        <li>Sign in to {signin.provider} there with your password, a passkey or a code from your phone</li>
        <li>If {signin.provider} asks which account, pick {signin.account}</li>
        <li>The window closes by itself once you are signed in, and the fetch carries on</li>
      </ol>
    </header>
    {isWaiting && <p className="signin-waiting" role="status"><span className="credential-ring storing" aria-hidden="true" />{signin.state === "open"
      ? "The " + signin.provider + " window is open on your screen. Sign in there and this card moves on by itself."
      : "Opening the " + signin.provider + " sign-in window on your PC."}</p>}
    {signin.state === "ready" && !isOpening && signin.reason && <p className="signin-note"><Icon name="warning" />{signin.reason}</p>}
    {isLocal
      ? <button type="button" className="primary signin-open" disabled={!connected || isWaiting || signin.state !== "ready"} onClick={onOpen}><Icon name="external" />Open the sign-in window</button>
      : <p className="credential-remote"><Icon name="lock" />Open the sign-in window from the board on your PC.</p>}
    {(pager || error) && <div className="card-actions"><ClickFeedback text={error} />{pager}</div>}
  </article>;
}
