import { useResource } from "./api";
import { object, string, strings, type Run, type Session, type Snapshot, type Task } from "./types";
import { harnessName } from "./workflow";
import { ownsTaskSession, sessionModel } from "./lineageTree";
import { harnessMark, modelMark } from "./connectors";
import { ConnectorMark } from "./ConnectorMark";
import { Disclosure } from "./Disclosure";
import { Icon } from "./Icon";
import { ConnectionsPanel } from "./connections-panel";
import { EngineSelector } from "./engine-selector";
import { DevDriveSetting } from "./dev-drive-setting";
import { isCfoRestartOffered, RestartCfoButton } from "./restart-cfo-button";
import { RemoteAccessSetting } from "./remote-access-setting";
import { AppBadgeSetting } from "./app-badge-setting";

function parse(value: unknown) {
  const v = object(value);
  return { project: string(v.project), repository: string(v.repository), root: string(v.root), branch: string(v.branch), harness: string(v.harness), model: string(v.model), notes: strings(v.notes) };
}

// The supervisor labels a model "Reported: x" or "Configured: x".
function splitModel(model: string): { name: string; basis: string } {
  const [basis, name] = model.split(/: (.*)/s);
  return name === undefined ? { name: model, basis: "" } : { name, basis };
}

// Where the work lives, what it runs on and what it reaches: the Workspace,
// Harness and Connections sections of a panel, each closed until it is
// opened, like the sections below them. Harness holds every harness control,
// the CFO's Restart among them, and Connections names no harness.
export function WorkspaceDetails({ task, node, runs, snapshot, connected, onRepair }: { task?: Task; node?: Session; runs?: Run[]; snapshot: Snapshot; connected: boolean; onRepair?: (key: string) => void }) {
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
  const isCfo = !task && !node;
  const hasConnections = child || !!task && !queued && !isArchived;
  return <>
    <Disclosure kind="workspace-details" title="Workspace">
      {unlinked ? <p className="muted">No working folder.</p> : isArchived ? <p className="workspace-project">{task?.project || "Project not recorded"}</p> : queued ? <p className="workspace-project">{task.project || "Project not specified"}</p> : resource.error ? <button className="icon-button raised" aria-label="Retry" data-tip="Retry" onClick={resource.reload}><Icon name="refresh" /></button> : !details ? <p className="loading" role="status">Reading workspace…</p> : <>
        <dl>{[["Repository", details.repository], ["Branch", details.branch], [child ? "Owning task folder" : task ? "Working folder" : "CFO project root", details.root]].filter(([, value]) => value).map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>
        {details.notes.map((note) => <p key={note}>{note}</p>)}
      </>}
      {isCfo && snapshot.dev_drive && <DevDriveSetting drive={snapshot.dev_drive} instance={snapshot.instance} />}
    </Disclosure>
    {isCfo && !snapshot.example && <Disclosure kind="remote-access" title="Remote access"><RemoteAccessSetting instance={snapshot.instance} runs={snapshot.runs || []} connected={connected} /><AppBadgeSetting instance={snapshot.instance} /></Disclosure>}
    <Disclosure kind="harness" title="Harness">
      {task && !child ? <EngineSelector key={task.id + task.generation} task={task} instance={snapshot.instance} /> : <>
        {!(isCfo && isCfoRestartOffered(snapshot)) && <ul className="connection-list" aria-label="Harness and model">
          {harness && <li><ConnectorMark mark={harnessMark(child ? node.harness : details?.harness || "")} label={harness} /><span className="connection-name">{harness}</span></li>}
          {model.name && <li><ConnectorMark mark={provider.mark} label={provider.provider} /><span className="connection-name">{model.name}<span className="chip">{model.basis ? model.basis + " model" : "Model"}</span></span></li>}
        </ul>}
        {isCfo && <RestartCfoButton snapshot={snapshot} />}
      </>}
    </Disclosure>
    {hasConnections && <Disclosure kind="connections" title="Connections">
      {child ? <p>No separate connections were reported for this child.</p> : task && <ConnectionsPanel key={task.id + task.generation} task={task} runs={runs} onRepair={onRepair} />}
    </Disclosure>}
  </>;
}
