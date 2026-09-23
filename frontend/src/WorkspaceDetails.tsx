import { useState } from "react";
import { message, request, useResource } from "./api";
import { array, object, string, strings, type Session, type Task } from "./types";
import { harnessName } from "./workflow";
import { ownsTaskSession, sessionModel } from "./lineageTree";

function parse(value: unknown) {
  const v = object(value);
  const entries = (value: unknown) => array(value).map((value) => { const e = object(value); return { name: string(e.name), status: string(e.status), source: string(e.source) }; });
  return { project: string(v.project), repository: string(v.repository), root: string(v.root), branch: string(v.branch), harness: string(v.harness), model: string(v.model), mcp: entries(v.mcp), environment: entries(v.environment), notes: strings(v.notes) };
}
export function WorkspaceDetails({ task, node, instance }: { task?: Task; node?: Session; instance: string }) {
  const [opening, setOpening] = useState(false);
  const [outcome, setOutcome] = useState("");
  const child = !!node && !ownsTaskSession(node, task);
  const queued = !!task && !task.generation;
  const path = "/api/workspace" + (task ? "?task=" + encodeURIComponent(task.id) + "&generation=" + encodeURIComponent(task.generation) : "");
  const unlinked = !!node && !task;
  const resource = useResource(!queued && !unlinked ? path : null, parse);
  const details = resource.data;
  const open = async (target: "vscode" | "folder") => {
    if (!task || opening) return;
    setOpening(true); setOutcome("");
    try {
      const result = object(await request("/api/workspace/open", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ task: task.id, generation: task.generation, target }) }));
      setOutcome(string(result.message));
    } catch (error: unknown) { setOutcome(message(error) + " Nothing is retried automatically."); }
    finally { setOpening(false); }
  };
  return <section className="workspace-details" aria-label="Workspace">
    <h3>Workspace</h3>
    {unlinked ? <p className="muted">No working folder was reported for this session.</p> : queued ? <><p className="workspace-project">{task.project || "Project not specified"}</p><p className="muted">Not started yet.</p></> : resource.error ? <p role="alert">{resource.error}</p> : !details ? <p role="status">Reading workspace...</p> : <>
      <dl>{[["Repository", details.repository], ["Branch", details.branch], [child ? "Owning task folder" : task ? "Working folder" : "CFO project root", details.root]].filter(([, value]) => value).map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>
      {task && !child && <div className="workspace-actions"><button disabled={opening || !instance} onClick={() => void open("vscode")}>Open in VS Code</button><button disabled={opening || !instance} onClick={() => void open("folder")}>Open folder</button></div>}
      {outcome && <p className="workspace-outcome" role="status">{outcome}</p>}
      <details className="connections-details"><summary>Connections<span>{child ? "" : details.mcp.length + " configured"}</span></summary><div>
        <dl>{[["Harness", harnessName(child ? node.harness : details.harness)], ["Model", child ? sessionModel(node, task) : details.model]].filter(([, value]) => value).map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>
        {child ? <p>No separate connection configuration was reported for this child.</p> : <>
          <p>Configured names only. Live connection and environment state is unavailable.</p>
          {details.mcp.length > 0 && <ul>{details.mcp.map((entry) => <li key={entry.name}><strong>{entry.name}</strong><span>{entry.status}</span></li>)}</ul>}
          {details.environment.length > 0 && <><h4>Environment</h4><ul>{details.environment.map((entry) => <li key={entry.name + entry.source}><strong>{entry.name}</strong><span>{entry.status}</span></li>)}</ul></>}
        </>}
      </div></details>
    </>}
  </section>;
}
