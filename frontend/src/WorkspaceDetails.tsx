import { useResource } from "./api";
import { array, object, string, strings, type Session, type Task } from "./types";
import { harnessName } from "./workflow";
import { ownsTaskSession, sessionModel } from "./lineageTree";
import { connectorMark, harnessMark, modelMark } from "./connectors";
import { ConnectorMark } from "./ConnectorMark";
import { Icon, type IconName } from "./Icon";
import { Disclosure } from "./Disclosure";

function parse(value: unknown) {
  const v = object(value);
  const entries = (value: unknown) => array(value).map((value) => { const e = object(value); return { name: string(e.name), status: string(e.status), source: string(e.source) }; });
  return { project: string(v.project), repository: string(v.repository), root: string(v.root), branch: string(v.branch), harness: string(v.harness), model: string(v.model), mcp: entries(v.mcp), environment: entries(v.environment), notes: strings(v.notes) };
}

// The supervisor labels a model "Reported: x" or "Configured: x".
function splitModel(model: string): { name: string; basis: string } {
  const [basis, name] = model.split(/: (.*)/s);
  return name === undefined ? { name: model, basis: "" } : { name, basis };
}

function Status({ status }: { status: string }) {
  const shown: Record<string, [IconName, string]> = { Configured: ["key", "declared"], Empty: ["warning", "empty"] };
  const [icon, tone] = shown[status] || ["clock", "declared"];
  return <span className={"connection-status " + tone}><Icon name={icon} />{status.toLowerCase()}</span>;
}

export function WorkspaceDetails({ task, node }: { task?: Task; node?: Session }) {
  const child = !!node && !ownsTaskSession(node, task);
  const queued = !!task && !task.generation;
  const path = "/api/workspace" + (task ? "?task=" + encodeURIComponent(task.id) + "&generation=" + encodeURIComponent(task.generation) : "");
  const unlinked = !!node && !task;
  const resource = useResource(!queued && !unlinked ? path : null, parse);
  const details = resource.data;
  const harness = details ? harnessName(child ? node.harness : details.harness) : "";
  const model = details ? splitModel(child ? sessionModel(node, task) : details.model) : { name: "", basis: "" };
  const provider = modelMark(model.name);
  return <section className="workspace-details" aria-label="Workspace">
    <h3>Workspace</h3>
    {unlinked ? <p className="muted">No working folder was reported for this session.</p> : queued ? <><p className="workspace-project">{task.project || "Project not specified"}</p><p className="muted">Not started yet.</p></> : resource.error ? <p role="alert">{resource.error}</p> : !details ? <p role="status">Reading workspace...</p> : <>
      <dl>{[["Repository", details.repository], ["Branch", details.branch], [child ? "Owning task folder" : task ? "Working folder" : "CFO project root", details.root]].filter(([, value]) => value).map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>
      <Disclosure kind="connections" title={<>Connectors{!child && <span className="column-count">{details.mcp.length + details.environment.length}</span>}</>}>
        <ul className="connection-list" aria-label="Harness and model">
          {harness && <li><ConnectorMark mark={harnessMark(child ? node.harness : details.harness)} label={harness} /><span className="connection-name">{harness}<span className="chip">Harness</span></span></li>}
          {model.name && <li><ConnectorMark mark={provider.mark} label={provider.provider} /><span className="connection-name">{model.name}<span className="chip">{model.basis ? model.basis + " model" : "Model"}</span></span></li>}
        </ul>
        {child ? <p>No separate connection configuration was reported for this child.</p> : <>
          {details.mcp.length + details.environment.length > 0 && <ul className="connection-list" aria-label="Connectors">
            {details.mcp.map((entry) => <li key={"mcp:" + entry.name}><ConnectorMark mark={connectorMark(entry.name, "mcp")} label={entry.name} /><span className="connection-name">{entry.name}<span className="chip">MCP server</span></span><Status status={entry.status} /></li>)}
            {details.environment.map((entry) => <li key={"env:" + entry.name + entry.source}><ConnectorMark mark={connectorMark(entry.name, "credential")} label={entry.name} /><span className="connection-name mono">{entry.name}<span className="chip">Credential</span></span><Status status={entry.status} /></li>)}
          </ul>}
          {details.notes.map((note) => <p key={note}>{note}</p>)}
          <p>Configured is not connected. No secret values are shown.</p>
        </>}
      </Disclosure>
    </>}
  </section>;
}
