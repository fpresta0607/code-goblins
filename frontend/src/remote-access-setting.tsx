import { useState } from "react";
import { message, request, useResource } from "./api";
import { boolean, object, string } from "./types";
import { CopyValue } from "./CopyValue";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import { Disclosure } from "./Disclosure";
import "./remote-access.css";

function parse(value: unknown) {
  const v = object(value);
  return { enabled: boolean(v.enabled), ready: boolean(v.ready), url: v.url ? string(v.url) : "", login: v.login ? string(v.login) : "", issue: v.issue ? string(v.issue) : "", scrawlReady: v.scrawl_ready === undefined ? false : boolean(v.scrawl_ready), scrawlIssue: v.scrawl_issue ? string(v.scrawl_issue) : "" };
}

export function RemoteAccessSetting({ instance }: { instance: string }) {
  const resource = useResource("/api/remote", parse);
  const [changed, setChanged] = useState<ReturnType<typeof parse>>();
  const [busy, setBusy] = useState(false);
  const [feedback, showFeedback] = useClickFeedback();
  const status = changed ?? resource.data;
  const local = location.hostname === "127.0.0.1" || location.hostname === "[::1]";
  const refresh = () => { setChanged(undefined); resource.reload(); };
  const change = async (input: { on: boolean } | { scrawl: true }) => {
    setBusy(true);
    try {
      setChanged(parse(await request("/api/remote", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify(input) })));
    } catch (error: unknown) { showFeedback(message(error)); setChanged(undefined); resource.reload(); }
    finally { setBusy(false); }
  };
  return <section className="remote-access-setting" aria-label="Remote access">
    <div className="remote-access-heading"><strong>Tailscale</strong>
      {status && (status.enabled || local) && <button className="secondary" disabled={busy || !instance} onClick={() => void change({ on: !status.enabled })}>{busy ? "Updating…" : status.enabled ? "Turn off" : "Enable"}</button>}
    </div>
    {status ? <>
      <p>{status.enabled ? status.ready ? "Private access is ready." : "Remote access needs attention." : "Open this board and the CFO terminal from your phone over Tailscale."}</p>
      {status.enabled && status.url && <><CopyValue value={status.url} /><a href={status.url} target="_blank" rel="noreferrer">Open private board</a></>}
      {status.enabled && <p className="muted">Connect Tailscale as {status.login}, then open the link in Safari. Share → Add to Home Screen keeps it handy. Keep this PC awake and online.</p>}
      {!status.enabled && local && <p className="muted">Enable also turns on Start at login so the board returns after Windows sign-in.</p>}
      {status.issue && <p role="status">{status.issue}</p>}
      {status.enabled && <>
        <div className="remote-access-heading"><strong>Scrawl reviews</strong>{local && <button className="secondary" disabled={busy || !status.ready} onClick={() => void change({ scrawl: true })}>Set up Scrawl</button>}</div>
        <p>{status.scrawlReady ? "Private reviews are ready. Open a review from this board." : status.scrawlIssue || "Set up Scrawl on this PC to open reviews from your phone."}</p>
        {local && !status.scrawlReady && <p className="muted">Setup restarts Scrawl privately. Saved sessions and queued feedback stay. Open review pages reconnect.</p>}
        {local && <p className="muted">After an upgrade, restart or resume older CFO and goblin terminal sessions through Code Goblins before relying on private Scrawl launches.</p>}
      </>}
    </> : !resource.error && <p role="status">Checking remote access…</p>}
    <ClickFeedback text={feedback} />
    {resource.error && <span className="plain-status">Unavailable</span>}
    <div className="remote-access-help"><button className="secondary" disabled={busy} onClick={refresh}>Refresh</button><a href="https://tailscale.com/download/windows" target="_blank" rel="noreferrer">Get Tailscale for PC</a><a href="https://tailscale.com/download/ios" target="_blank" rel="noreferrer">For iPhone</a><a href="https://tailscale.com/docs/how-to/set-up-https-certificates" target="_blank" rel="noreferrer">HTTPS setup</a></div>
    <p className="muted">Connect the Tailscale app on each device with the same account. Your device’s VPN stays in Tailscale.</p>
    {local && <Disclosure kind="remote-https" title="Approve HTTPS on this PC"><p>In an Administrator PowerShell, run the command below and follow Tailscale’s approval link. Then press Enable again.</p><CopyValue value={"tailscale serve --bg --https=4311 " + location.origin} /></Disclosure>}
  </section>;
}
