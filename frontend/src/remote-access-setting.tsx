import { useCallback, useEffect, useRef, useState } from "react";
import { message, request } from "./api";
import { boolean, object, string } from "./types";
import { CopyValue } from "./CopyValue";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import { SettingSwitch } from "./setting-switch";
import "./remote-access.css";

// A few words for what needs him, by the supervisor's names: what this PC's
// Tailscale lacks, what is wrong while remote access is on, and what is
// wrong with Scrawl reviews.
const TAILSCALE_NOTES: Record<string, string> = {
  missing: "Not installed", stopped: "Not running", signed_out: "Signed out", disconnected: "Disconnected", unsuitable: "Tagged or no MagicDNS", unavailable: "Not answering", no_https: "HTTPS not approved",
  settings: "Settings unreadable", board_moved: "Board moved", account_changed: "Account changed", funnel: "Funnel on", route_missing: "Route missing", start_at_login: "Start at login off",
};
const SCRAWL_NOTES: Record<string, string> = { bypassed: "Direct route", exposed: "Not private", foreign: "Other server", outdated: "Update Scrawl", unrouted: "Needs setup" };

type Status = ReturnType<typeof parse>;

function parse(value: unknown) {
  const v = object(value);
  const text = (field: unknown) => field ? string(field) : "";
  return { enabled: boolean(v.enabled), ready: boolean(v.ready), url: text(v.url), issue: text(v.issue), attention: text(v.attention), tailscale: text(v.tailscale), tailscaleIssue: text(v.tailscale_issue), fix: text(v.fix), scrawl: v.scrawl === undefined ? false : boolean(v.scrawl), scrawlState: text(v.scrawl_state), scrawlIssue: text(v.scrawl_issue), scrawlFix: text(v.scrawl_fix) };
}

// Remote access is one switch, Tailscale, offered once the supervisor reads
// this PC's Tailscale as signed in. On, it shows the private link and,
// indented under it as a setting of its own, Scrawl reviews' switch. Each
// switch says how it stands with a mark and what needs him in a few words,
// and on this PC the command that mends it sits under it to copy.
export function RemoteAccessSetting({ instance }: { instance: string }) {
  const [status, setStatus] = useState<Status>();
  const [failed, setFailed] = useState(false);
  const [pending, setPending] = useState<"" | "tailscale" | "scrawl">("");
  const [feedback, showFeedback] = useClickFeedback();
  const local = location.hostname === "127.0.0.1" || location.hostname === "[::1]";
  // Each read and press takes the next turn, and an answer is shown only
  // while its turn is the latest, so a read begun before a press never
  // undoes what the press did.
  const turn = useRef(0);
  const pressing = useRef(false);
  const read = useCallback(() => {
    const mine = ++turn.current;
    void request("/api/remote").then((value) => {
      if (mine !== turn.current) return;
      setStatus(parse(value));
      setFailed(false);
    }).catch(() => { if (mine === turn.current) setFailed(true); });
  }, []);
  // Tailscale is read again whenever he comes back to the board, as from
  // installing it or signing in, since nothing else tells the board.
  useEffect(() => {
    read();
    const again = () => { if (!document.hidden && !pressing.current) read(); };
    window.addEventListener("focus", again);
    document.addEventListener("visibilitychange", again);
    return () => { window.removeEventListener("focus", again); document.removeEventListener("visibilitychange", again); };
  }, [read]);
  const change = async (input: { on: boolean } | { scrawl: boolean }) => {
    const mine = ++turn.current;
    pressing.current = true;
    setPending("on" in input ? "tailscale" : "scrawl");
    try {
      const next = parse(await request("/api/remote", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify(input) }));
      if (mine === turn.current) setStatus(next);
    } catch (error: unknown) { showFeedback(message(error)); read(); }
    finally { pressing.current = false; setPending(""); }
  };
  if (!status) return <section className="remote-access-setting" aria-label="Remote access">
    {failed ? <span className="plain-status">Unavailable</span> : <p role="status">Checking remote access…</p>}
  </section>;
  const canTurnOn = local && !status.attention && (status.tailscale === "ready" || status.tailscale === "no_https");
  const scrawlWrong = status.scrawlState !== "" && status.scrawlState !== "on";
  return <section className="remote-access-setting" aria-label="Remote access">
    <SettingSwitch label="Tailscale" on={status.enabled} pending={pending === "tailscale"} disabled={pending !== "" || !instance || !status.enabled && !canTurnOn}
      tip={status.enabled ? "Turn remote access off" : canTurnOn ? "Turn remote access on" : status.tailscaleIssue || status.issue}
      mark={status.enabled ? status.ready && !status.attention ? "ok" : "problem" : status.attention ? "problem" : undefined} markTip={status.issue}
      note={TAILSCALE_NOTES[status.attention || (status.enabled ? "" : status.tailscale)]}
      onPress={() => void change({ on: !status.enabled })} />
    {local && !status.enabled && status.fix && <CopyValue value={status.fix} />}
    {status.enabled && status.url && <CopyValue value={status.url} />}
    {status.enabled && <div className="remote-access-option">
      <SettingSwitch label="Scrawl reviews" on={status.scrawl} pending={pending === "scrawl"} disabled={pending !== "" || !instance || !status.ready || !status.scrawl && !local}
        tip={status.scrawl ? "Turn Scrawl reviews off" : "Turn Scrawl reviews on"}
        mark={scrawlWrong ? "problem" : status.scrawl && status.scrawlState === "on" ? "ok" : undefined} markTip={status.scrawlIssue}
        note={SCRAWL_NOTES[status.scrawlState]}
        onPress={() => void change({ scrawl: !status.scrawl })} />
      {local && status.scrawlFix && <CopyValue value={status.scrawlFix} />}
    </div>}
    <ClickFeedback text={feedback} />
  </section>;
}
