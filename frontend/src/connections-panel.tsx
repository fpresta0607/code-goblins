import { useCallback, useEffect, useRef, useState } from "react";
import { message, request } from "./api";
import { ConnectionRow } from "./connection-row";
import { parseConnections, type ConnectionEntry } from "./connections";
import { Icon } from "./Icon";
import { object, string, type Run, type Task } from "./types";
import "./connections.css";

export function ConnectionsPanel({ task, runs = [], onRepair }: { task: Task; runs?: Run[]; onRepair?: (key: string) => void }) {
  const [data, setData] = useState<ReturnType<typeof parseConnections>>();
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [runId, setRunId] = useState("");
  const [loginURL, setLoginURL] = useState("");
  const [isBusy, setIsBusy] = useState(false);
  const [reads, setReads] = useState(0);
  const isSignInPending = useRef(false);
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
        if (!controller.signal.aborted) { setData(next); setError(""); }
      } catch (error: unknown) {
        if (!controller.signal.aborted) setError(message(error));
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
    setError("");
    try { setData(parseConnections(await post("/api/connections/check"))); setReads((count) => count + 1); }
    catch (error: unknown) { setError(message(error)); }
    finally { setIsBusy(false); }
  }, [post]);
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
    setIsBusy(true); setError(""); setNotice(""); setRunId("");
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
    } catch (error: unknown) { setError(message(error)); }
    finally { setIsBusy(false); }
  };
  return <section className="connections-panel" aria-label="Connections">
    <div className="connections-toolbar"><p>{data?.checking || !data ? "Checking connections..." : "Connection health"}</p><button type="button" className="icon-button" aria-label="Recheck connections" data-tip="Recheck connections" data-tip-align="end" disabled={isBusy || !data || data.checking} onClick={() => void refresh()}><Icon name="refresh" /></button></div>
    {(error || data?.error) && <p className="connections-error" role="alert">{error || data?.error}</p>}
    {notice && <div className="connection-notice"><p role="status">{notice}</p>{runId && onRepair && <button type="button" onClick={() => onRepair("run:" + runId)}>Open repair card</button>}{loginURL && <a href={loginURL} target="_blank" rel="noreferrer" onClick={() => { isSignInPending.current = true; }}>Open sign-in</a>}</div>}
    {[["mcp", "MCP servers"], ["service", "Repository services"], ["credential", "Goblin credentials"]].map(([kind, label]) => {
      const entries = data?.entries.filter((entry) => entry.kind === kind) || [];
      return entries.length > 0 && <div className="connections-group" key={kind}><h4>{label}</h4><ul aria-label={label}>{entries.map((entry) => <ConnectionRow key={entry.id} entry={entry} isBusy={isBusy || !!data?.checking} onFix={(entry, action) => void fix(entry, action)} />)}</ul></div>;
    })}
    {data && !data.checking && data.entries.length === 0 && !data.error && <p>No connections were reported for this goblin.</p>}
  </section>;
}
