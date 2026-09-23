import { useState } from "react";
import { useResource } from "./api";
import { array, object, string, strings, type Session, type Task } from "./types";
import { ownsTaskSession, sessionModel } from "./lineageTree";

function parse(value: unknown) {
  const v = object(value);
  const entries = (value: unknown) => array(value).map((value) => { const e = object(value); return { name: string(e.name), status: string(e.status), source: string(e.source) }; });
  return { project: string(v.project), repository: string(v.repository), root: string(v.root), branch: string(v.branch), harness: string(v.harness), model: string(v.model), mcp: entries(v.mcp), environment: entries(v.environment), notes: strings(v.notes) };
}
export function WorkspaceDetails({ task, node }: { task?: Task; node?: Session }) {
  const [open, setOpen] = useState(false);
  const child = !!node && !ownsTaskSession(node, task);
  const queued = !!task && !task.generation;
  const path = "/api/workspace" + (task ? "?task=" + encodeURIComponent(task.id) + "&generation=" + encodeURIComponent(task.generation) : "");
  const resource = useResource(open && !queued ? path : null, parse);
  const details = resource.data;
  return <details className="workspace-details" open={open} onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary>Workspace details</summary>
    {open && <div className="workspace-popover">
      {queued ? <><p>{task.project || "Project not specified"}</p><p>Not started yet.</p></> : resource.error ? <p role="alert">{resource.error}</p> : !details ? <p role="status">Reading workspace details...</p> : <>
        <dl>{[["Project", details.project], ["Repository", details.repository], ["Branch", details.branch], ["Root", details.root], ["Harness", child ? node.harness : details.harness], ["Model", child ? sessionModel(node, task) : details.model]].filter(([, value]) => value).map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>
        {child ? <p>This child has no separately reported terminal or connection configuration.</p> : <>
          <p>Configuration is shown below. Live connection and environment state is not reported.</p>
          <h3>MCP connections</h3>{details.mcp.length ? <ul>{details.mcp.map((entry) => <li key={entry.name}><strong>{entry.name}</strong><span>{entry.status}</span></li>)}</ul> : <p>No connections reported.</p>}
          <h3>Environment names</h3>{details.environment.length ? <ul>{details.environment.map((entry) => <li key={entry.name + entry.source}><strong>{entry.name}</strong><span>{entry.status}</span><small>{entry.source}</small></li>)}</ul> : <p>No scoped variable declarations reported.</p>}
          {details.notes.map((note) => <p key={note}>{note}</p>)}
        </>}
      </>}
    </div>}
  </details>;
}
