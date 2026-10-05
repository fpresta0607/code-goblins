import { useResource } from "./api";
import { object, string, strings, type Run, type Session, type Task } from "./types";
import { harnessName } from "./workflow";
import { ownsTaskSession, sessionModel } from "./lineageTree";
import { harnessMark, modelMark } from "./connectors";
import { ConnectorMark } from "./ConnectorMark";
import { Disclosure } from "./Disclosure";
import { ConnectionsPanel } from "./connections-panel";
import { EngineSelector } from "./engine-selector";

function parse(value: unknown) {
  const v = object(value);
  return { project: string(v.project), repository: string(v.repository), root: string(v.root), branch: string(v.branch), harness: string(v.harness), model: string(v.model), notes: strings(v.notes) };
}

// The supervisor labels a model "Reported: x" or "Configured: x".
function splitModel(model: string): { name: string; basis: string } {
  const [basis, name] = model.split(/: (.*)/s);
  return name === undefined ? { name: model, basis: "" } : { name, basis };
}

export function WorkspaceDetails({ task, node, runs, instance, onRepair }: { task?: Task; node?: Session; runs?: Run[]; instance: string; onRepair?: (key: string) => void }) {
  const child = !!node && !ownsTaskSession(node, task);
  const queued = !!task && !task.generation;
  const isArchived = !!task?.archived;
  const path = "/api/workspace" + (task ? "?task=" + encodeURIComponent(task.id) + "&generation=" + encodeURIComponent(task.generation) : "");
  const unlinked = !!node && !task;
  const resource = useResource(!queued && !unlinked && !isArchived ? path : null, parse);
  const details = resource.data;
  const harness = details ? harnessName(child ? node.harness : details.harness) : "";
  const model = details ? splitModel(child ? sessionModel(node, task) : details.model) : { name: "", basis: "" };
  const provider = modelMark(model.name);
  return <section className="workspace-details" aria-label="Workspace">
    <h3>Workspace</h3>
    {unlinked ? <p className="muted">No working folder was reported for this session.</p> : isArchived ? <p className="workspace-project">{task?.project || "Project not recorded"}</p> : queued ? <><p className="workspace-project">{task.project || "Project not specified"}</p><p className="muted">Not started yet.</p></> : resource.error ? <p role="alert">{resource.error}</p> : !details ? <p role="status">Reading workspace...</p> : <>
      <dl>{[["Repository", details.repository], ["Branch", details.branch], [child ? "Owning task folder" : task ? "Working folder" : "CFO project root", details.root]].filter(([, value]) => value).map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>
      {details.notes.map((note) => <p key={note}>{note}</p>)}
    </>}
      <Disclosure kind="connections" title="Connections">
        {task && !child ? <EngineSelector key={task.id + task.generation} task={task} instance={instance} /> : <ul className="connection-list" aria-label="Harness and model">
          {harness && <li><ConnectorMark mark={harnessMark(child ? node.harness : details?.harness || "")} label={harness} /><span className="connection-name">{harness}<span className="chip">Harness</span></span></li>}
          {model.name && <li><ConnectorMark mark={provider.mark} label={provider.provider} /><span className="connection-name">{model.name}<span className="chip">{model.basis ? model.basis + " model" : "Model"}</span></span></li>}
        </ul>}
        {child ? <p>No separate connections were reported for this child.</p> : task ? !queued && !isArchived && <ConnectionsPanel key={task.id + task.generation} task={task} runs={runs} onRepair={onRepair} /> : <p>Choose a goblin to check its connections.</p>}
      </Disclosure>
  </section>;
}
