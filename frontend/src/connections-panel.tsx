import { useCallback, useEffect, useRef, useState } from "react";
import { message, reportToCfo, request } from "./api";
import { BrowserLoginRow } from "./browser-login-row";
import { parseBrowserLogins, type BrowserLogin } from "./browser-logins";
import { ConnectorCard } from "./connector-card";
import { parseConnections, type ConnectionEntry } from "./connections";
import { Icon } from "./Icon";
import { object, string, type Run, type Task } from "./types";
import "./connections.css";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import { ForgetLoginDialog } from "./forget-login-dialog";
import { goblinName } from "./task-words";

export function ConnectionsPanel({ task, runs = [], onRepair }: { task: Task; runs?: Run[]; onRepair?: (key: string) => void }) {
  const [data, setData] = useState<ReturnType<typeof parseConnections>>();
  const [notice, setNotice] = useState("");
  const [feedback, showFeedback] = useClickFeedback();
  const [runId, setRunId] = useState("");
  const [loginURL, setLoginURL] = useState("");
  const [isBusy, setIsBusy] = useState(false);
  const [reads, setReads] = useState(0);
  const [forgetting, setForgetting] = useState<BrowserLogin>();
  // When the panel last read its connections, which a login's times are
  // told from.
  const [readAt, setReadAt] = useState(0);
  // What his + or check asked for each card, shown at once and kept until a
  // finished check shows it.
  const [asked, setAsked] = useState<ReadonlyMap<string, boolean>>(new Map());
  const isSignInPending = useRef(false);
  // Whose connections, as the CFO is told when they cannot be read.
  const whose = goblinName(task) + " (" + task.id + ")";
  const whoseValue = useRef(whose);
  useEffect(() => { whoseValue.current = whose; }, [whose]);
  const repairFinishedAt = runs.filter((run) => run.connection_task === task.id && run.connection_generation === task.generation).map((run) => run.finished_at).sort().at(-1) || "";
  const path = "/api/connections?task=" + encodeURIComponent(task.id) + "&generation=" + encodeURIComponent(task.generation);
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const read = async () => {
      let isChecking = true;
      try {
        const next = parseConnections(await request(path, controller.signal));
        isChecking = next.checking;
        if (!controller.signal.aborted) {
          setData(next);
          setReadAt(Date.now());
          if (!next.checking) setAsked((prior) => new Map([...prior].filter(([id, isCarried]) => next.entries.find((entry) => entry.id === id)?.grant !== (isCarried ? "revoke" : "grant"))));
        }
      } catch (error: unknown) {
        if (!controller.signal.aborted) reportToCfo("reading the connections of " + whoseValue.current, message(error));
      } finally {
        if (!controller.signal.aborted && isChecking) timer = setTimeout(() => void read(), 2000);
      }
    };
    void read();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [path, reads, repairFinishedAt]);
  const post = useCallback(async (endpoint: string, extra: Record<string, string> = {}) => {
    return request(endpoint, undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": data?.instance || "" }, body: JSON.stringify({ task: task.id, generation: task.generation, ...extra }) });
  }, [data?.instance, task.id, task.generation]);
  const refresh = useCallback(async () => {
    setIsBusy(true);
    try { setData(parseConnections(await post("/api/connections/check"))); setReadAt(Date.now()); setReads((count) => count + 1); }
    catch (error: unknown) { showFeedback(message(error)); }
    finally { setIsBusy(false); }
  }, [post, showFeedback]);
  useEffect(() => {
    const recheck = () => {
      if (!isSignInPending.current) return;
      isSignInPending.current = false;
      void refresh();
    };
    window.addEventListener("focus", recheck);
    return () => window.removeEventListener("focus", recheck);
  }, [refresh]);
  const fix = async (entry: ConnectionEntry, action: string) => {
    setIsBusy(true); setNotice(""); setRunId("");
    try {
      const result = object(await post("/api/connections/fix", { connection: entry.id, action }));
      setNotice(string(result.message)); setRunId(string(result.run_id));
      if (string(result.run_id)) onRepair?.("run:" + string(result.run_id));
      const url = string(result.url);
      if (url && new URL(url).protocol === "https:") {
        setLoginURL(url);
        isSignInPending.current = true;
        window.open(url, "_blank", "noopener,noreferrer");
      }
    } catch (error: unknown) { showFeedback(message(error)); }
    finally { setIsBusy(false); }
  };
  const endLogin = async (login: BrowserLogin, action: "sign-out" | "forget") => {
    setIsBusy(true);
    try {
      const logins = parseBrowserLogins(object(await post("/api/browser-logins/" + action, { login: login.id })).browser_logins);
      setData((current) => current && { ...current, browserLogins: logins });
    } catch (error: unknown) { showFeedback(message(error)); }
    finally { setIsBusy(false); }
  };
  const logins = data?.browserLogins || [];
  // His + gives the task the connector and his check takes it back, as cfo
  // auth grant and cfo auth revoke do; the card changes at once.
  const toggle = async (entry: ConnectionEntry) => {
    const isGiving = !(asked.get(entry.id) ?? entry.grant === "revoke");
    setAsked((prior) => new Map(prior).set(entry.id, isGiving));
    showFeedback("");
    try {
      setData(parseConnections(await post("/api/connections/grant", { connection: entry.id, action: isGiving ? "grant" : "revoke" })));
    } catch (error: unknown) {
      setAsked((prior) => { const next = new Map(prior); next.delete(entry.id); return next; });
      showFeedback(message(error));
    } finally { setReads((count) => count + 1); }
  };
  return <section className="connections-panel" aria-label="Connections">
    <div className="connections-toolbar"><p>{data?.checking || !data ? "Checking connections..." : data.error ? "Check failed" : "Connection health"}</p><button type="button" className="icon-button" aria-label="Recheck connections" data-tip="Recheck connections" disabled={isBusy || !data || data.checking} onClick={() => void refresh()}><Icon name="refresh" /></button></div>
    <ClickFeedback text={feedback} />
    {notice && <div className="connection-notice"><p role="status">{notice}</p>{runId && onRepair && <button type="button" onClick={() => onRepair("run:" + runId)}>Open repair card</button>}{loginURL && <a href={loginURL} target="_blank" rel="noreferrer" onClick={() => { isSignInPending.current = true; }}>Open sign-in</a>}</div>}
    {[["mcp", "MCP servers"], ["service", "Repository services"], ["credential", "Goblin credentials"]].map(([kind, label]) => {
      // A variable the task does not carry is its service's, whose card offers it.
      const entries = data?.entries.filter((entry) => entry.kind === kind && !(kind === "credential" && entry.status === "withheld")) || [];
      return entries.length > 0 && <div className="connections-group" key={kind}><h4>{label}</h4><ul aria-label={label}>{entries.map((entry) => <ConnectorCard key={entry.id} entry={entry} isCarried={asked.get(entry.id) ?? entry.grant === "revoke"} isBusy={isBusy || !!data?.checking} isChanging={asked.has(entry.id) && !!data?.checking} onFix={(entry, action) => void fix(entry, action)} onToggle={(entry) => void toggle(entry)} />)}</ul></div>;
    })}
    {logins.length > 0 && <div className="connections-group"><h4>Browser logins</h4><ul aria-label="Browser logins">
      {logins.map((login) => <BrowserLoginRow key={login.id} login={login} now={readAt} isBusy={isBusy} onSignOut={() => void endLogin(login, "sign-out")} onForget={() => setForgetting(login)} />)}
    </ul></div>}
    {forgetting && <ForgetLoginDialog login={forgetting} onCancel={() => setForgetting(undefined)} onForget={() => { setForgetting(undefined); void endLogin(forgetting, "forget"); }} />}
    {data && !data.checking && data.entries.length === 0 && logins.length === 0 && !data.error && <p>No connections.</p>}
  </section>;
}
