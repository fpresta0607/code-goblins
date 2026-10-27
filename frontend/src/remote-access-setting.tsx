import { useCallback, useEffect, useRef, useState } from "react";
import { message, reportToCfo, request } from "./api";
import { array, boolean, object, string, type Run } from "./types";
import { CopyValue } from "./CopyValue";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import { SettingSwitch } from "./setting-switch";
import { isLive, RemoteFix } from "./remote-fix";
import { runMark } from "./feedback";
import "./remote-access.css";

type Row = "tailscale" | "scrawl";

// A few words for what needs him, by the supervisor's names: what this PC's
// Tailscale lacks, what is wrong while remote access is on, and what is
// wrong with Scrawl reviews.
const TAILSCALE_NOTES: Record<string, string> = {
  missing: "Not installed", stopped: "Not running", signed_out: "Signed out", disconnected: "Disconnected", unsuitable: "Tagged or no MagicDNS", unavailable: "Not answering", no_https: "HTTPS not approved",
  settings: "Settings unreadable", board_moved: "Board moved", account_changed: "Account changed", funnel: "Funnel on", route_missing: "Route missing", start_at_login: "Start at login off",
};
// How often the setting reads remote access again while the board shows.
const READ_EVERY_MS = 5000;
const SCRAWL_NOTES: Record<string, string> = { bypassed: "Direct route", exposed: "Not private", foreign: "Other server", outdated: "Update Scrawl", unrouted: "Needs setup" };

type Status = ReturnType<typeof parse>;
type Machine = ReturnType<typeof parseMachines>[number];

function parse(value: unknown) {
  const v = object(value);
  const text = (field: unknown) => field ? string(field) : "";
  return { enabled: boolean(v.enabled), ready: boolean(v.ready), url: text(v.url), issue: text(v.issue), attention: text(v.attention), tailscale: text(v.tailscale), tailscaleIssue: text(v.tailscale_issue), fix: text(v.fix), scrawl: v.scrawl === undefined ? false : boolean(v.scrawl), scrawlState: text(v.scrawl_state), scrawlIssue: text(v.scrawl_issue), scrawlFix: text(v.scrawl_fix) };
}

// The machines on his tailnet whose boards answer privately, the one whose
// board this is among them.
function parseMachines(value: unknown) {
  return array(object(value).machines).map((each) => {
    const machine = object(each);
    return { name: string(machine.name), url: string(machine.url), isThis: boolean(machine.is_this) };
  });
}

// Remote access is one switch, Tailscale, offered once the supervisor reads
// this PC's Tailscale as signed in. On, it shows the private link and,
// indented under it as a setting of its own, Scrawl reviews' switch. Each
// switch says how it stands with a mark and what needs him in a few words,
// and on this PC the command that mends it sits under it, to copy or to run.
// On the private board, where another machine of his on the tailnet runs a
// board that answers, the switch has a picker of the machine the board and
// Scrawl connect to, this one by default; picking another opens that
// machine's private board. The board on the PC has none: another machine's
// gate refuses a page that comes from it.
export function RemoteAccessSetting({ instance, runs, connected }: { instance: string; runs: Run[]; connected: boolean }) {
  const [status, setStatus] = useState<Status>();
  const [failed, setFailed] = useState(false);
  const [pending, setPending] = useState<"" | Row | "fix">("");
  const [feedback, showFeedback] = useClickFeedback();
  const [machines, setMachines] = useState<Machine[]>([]);
  const [picked, setPicked] = useState("");
  const local = location.hostname === "127.0.0.1" || location.hostname === "[::1]";
  // Each read and press takes the next turn, and an answer is shown only
  // while its turn is the latest, so a read begun before a press never
  // undoes what the press did.
  const turn = useRef(0);
  const pressing = useRef(false);
  const reading = useRef<number | undefined>(undefined);
  const read = useCallback(() => {
    const mine = ++turn.current;
    reading.current = mine;
    void request("/api/remote").then((value) => {
      if (mine !== turn.current) return;
      setStatus(parse(value));
      setFailed(false);
    }).catch(() => { if (mine === turn.current) setFailed(true); })
      .finally(() => { if (reading.current === mine) reading.current = undefined; });
  }, []);
  const readMachines = useCallback(() => {
    if (local) return;
    void request("/api/remote/machines").then((value) => setMachines(parseMachines(value))).catch((error: unknown) => reportToCfo("the machines Remote access lists", message(error)));
  }, [local]);
  // Tailscale and the machines on it are read again whenever he comes back to
  // the board, as from installing it or signing in, and remote access every
  // few seconds while the board shows, since nothing tells the board when
  // Tailscale stops or starts, cfo remote turns it on or off, or a restart
  // moves it.
  useEffect(() => {
    read();
    readMachines();
    const again = () => {
      if (document.hidden || pressing.current) return;
      read();
      readMachines();
    };
    const every = setInterval(() => { if (!document.hidden && !pressing.current && reading.current !== turn.current) read(); }, READ_EVERY_MS);
    window.addEventListener("focus", again);
    document.addEventListener("visibilitychange", again);
    return () => { clearInterval(every); window.removeEventListener("focus", again); document.removeEventListener("visibilitychange", again); };
  }, [read, readMachines]);
  // A fix names the command the setting showed, which runs only while it is
  // still the row's, and the answer names its run.
  const [started, setStarted] = useState("");
  const change = async (input: { on: boolean } | { scrawl: boolean } | { fix: Row; command: string }) => {
    const mine = ++turn.current;
    pressing.current = true;
    setPending("on" in input ? "tailscale" : "scrawl" in input ? "scrawl" : "fix");
    try {
      const answer = await request("/api/remote", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify(input) });
      if (mine === turn.current) setStatus(parse(answer));
      if ("fix" in input) setStarted(string(object(answer).run_id));
    } catch (error: unknown) { showFeedback(message(error)); read(); }
    finally { pressing.current = false; setPending(""); }
  };
  // Each row's newest fix. One that ends after this setting started it or saw
  // it run, even one that failed before the board saw it running, reads the
  // setting again, since its row may be mended now, unless a press is on its
  // way, and a failure says why beside it for a moment.
  const fixes = { tailscale: runs.filter((run) => run.remote_fix === "tailscale").at(-1), scrawl: runs.filter((run) => run.remote_fix === "scrawl").at(-1) };
  const seenRunning = useRef(new Set<string>());
  useEffect(() => {
    if (started) seenRunning.current.add(started);
    for (const fix of [fixes.tailscale, fixes.scrawl]) {
      if (!fix) continue;
      if (isLive(fix)) seenRunning.current.add(fix.id);
      else if (seenRunning.current.delete(fix.id)) {
        if (!pressing.current) read();
        const mark = runMark(fix);
        if (mark.trouble) showFeedback(mark.label);
      }
    }
  }, [fixes.tailscale, fixes.scrawl, started, read, showFeedback]);
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
    {machines.length > 1 && <select className="remote-machines" aria-label="Machine" value={picked || machines.find((machine) => machine.isThis)?.url} disabled={!!picked} onChange={(event) => { setPicked(event.target.value); location.assign(event.target.value + "/"); }}>
      {machines.map((machine) => <option key={machine.url} value={machine.url}>{machine.name}</option>)}
    </select>}
    {local && (status.fix || isLive(fixes.tailscale)) && <RemoteFix command={status.fix} run={fixes.tailscale} instance={instance} connected={connected} pending={pending === "fix"} disabled={pending !== ""} onRun={() => void change({ fix: "tailscale", command: status.fix })} />}
    {status.enabled && status.url && <CopyValue value={status.url} />}
    {status.enabled && <div className="remote-access-option">
      <SettingSwitch label="Scrawl reviews" on={status.scrawl} pending={pending === "scrawl"} disabled={pending !== "" || !instance || !status.ready || !status.scrawl && !local}
        tip={status.scrawl ? "Turn Scrawl reviews off" : "Turn Scrawl reviews on"}
        mark={scrawlWrong ? "problem" : status.scrawl && status.scrawlState === "on" ? "ok" : undefined} markTip={status.scrawlIssue}
        note={SCRAWL_NOTES[status.scrawlState]}
        onPress={() => void change({ scrawl: !status.scrawl })} />
      {local && (status.scrawlFix || isLive(fixes.scrawl)) && <RemoteFix command={status.scrawlFix} run={fixes.scrawl} instance={instance} connected={connected} pending={pending === "fix"} disabled={pending !== ""} onRun={() => void change({ fix: "scrawl", command: status.scrawlFix })} />}
    </div>}
    <ClickFeedback text={feedback} />
  </section>;
}
