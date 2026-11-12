import { useResource } from "./api";
import { object, string, strings, type Session, type Snapshot, type Task } from "./types";
import { harnessName } from "./workflow";
import { ownsTaskSession, sessionModel } from "./lineageTree";
import { harnessMark, modelMark } from "./connectors";
import { ConnectorMark } from "./ConnectorMark";
import { Disclosure } from "./Disclosure";
import { Icon } from "./Icon";
import { EngineSelector } from "./engine-selector";
import { CeoEngineSelector } from "./ceo-engine-selector";
import { DevDriveSetting } from "./dev-drive-setting";
import { isCeoRestartOffered, RestartCeoButton } from "./restart-ceo-button";
import { RemoteAccessSetting } from "./remote-access-setting";
import { AppBadgeSetting } from "./app-badge-setting";
import { ChatStyleSetting } from "./chat-style-setting";
import { messagesQuery } from "./session-messages";

function parse(value: unknown) {
  const v = object(value);
  return { project: string(v.project), repository: string(v.repository), root: string(v.root), branch: string(v.branch), harness: string(v.harness), notes: strings(v.notes) };
}

// Where the work lives and what it runs on: the Workspace and Harness
// sections of a panel, each closed until it is opened, like the sections
// below them. Harness holds every harness control, the CEO's Restart among
// them. What a goblin reaches is no section here: its panel links to the
// Connectors page, which lists every goblin's connectors once (#2303).
export function WorkspaceDetails({ task, node, snapshot, connected, onConnectors }: { task?: Task; node?: Session; snapshot: Snapshot; connected: boolean; onConnectors: () => void }) {
  const child = !!node && !ownsTaskSession(node, task);
  const queued = !!task && !task.generation;
  const isArchived = !!task?.archived;
  const path = "/api/workspace" + (task ? "?task=" + encodeURIComponent(task.id) + "&generation=" + encodeURIComponent(task.generation) : "");
  const unlinked = !!node && !task;
  const resource = useResource(!queued && !unlinked && !isArchived ? path : null, parse);
  const details = resource.data;
  const harness = details ? harnessName(child ? node.harness : details.harness) : "";
  // A task's own session and the CEO's show their one record in the selector
  // below; a child session names the model it reported of itself.
  const model = details && child ? sessionModel(node, task) : "";
  const provider = modelMark(model);
  const isCeo = !task && !node;
  const hasConnectors = !child && !!task && !queued && !isArchived;
  // The Chat choice matters only where the board reads the session as messages.
  const hasChat = !child && (task ? !queued && !isArchived && !!messagesQuery(snapshot, task) : !!messagesQuery(snapshot));
  return <>
    <Disclosure kind="workspace-details" title="Workspace">
      {unlinked ? <p className="muted">No working folder.</p> : isArchived ? <p className="workspace-project">{task?.project || "Project not recorded"}</p> : queued ? <p className="workspace-project">{task.project || "Project not specified"}</p> : resource.error ? <button className="icon-button raised" aria-label="Retry" onClick={resource.reload}><Icon name="refresh" /></button> : !details ? <p className="loading" role="status">Reading workspace…</p> : <>
        <dl>{[["Repository", details.repository], ["Branch", details.branch], [child ? "Owning task folder" : task ? "Working folder" : "CEO project root", details.root]].filter(([, value]) => value).map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>
        {details.notes.map((note) => <p key={note}>{note}</p>)}
      </>}
      {hasChat && <ChatStyleSetting />}
      {isCeo && snapshot.dev_drive && <DevDriveSetting drive={snapshot.dev_drive} instance={snapshot.instance} />}
    </Disclosure>
    {isCeo && !snapshot.example && <Disclosure kind="remote-access" title="Remote access"><RemoteAccessSetting instance={snapshot.instance} runs={snapshot.runs || []} connected={connected} /><AppBadgeSetting instance={snapshot.instance} /></Disclosure>}
    <Disclosure kind="harness" title="Harness">
      {task && !child ? <EngineSelector key={task.id + task.generation} task={task} instance={snapshot.instance} /> : <>
        {!(isCeo && isCeoRestartOffered(snapshot)) && <ul className="connection-list" aria-label="Harness and model">
          {harness && <li><ConnectorMark mark={harnessMark(child ? node.harness : details?.harness || "")} label={harness} /><span className="connection-name">{harness}</span></li>}
          {model && <li><ConnectorMark mark={provider.mark} label={provider.provider} /><span className="connection-name">{model}<span className="chip">Model</span></span></li>}
        </ul>}
        {isCeo && <RestartCeoButton snapshot={snapshot} />}
        {isCeo && <CeoEngineSelector snapshot={snapshot} />}
      </>}
    </Disclosure>
    {hasConnectors && <button type="button" className="connectors-link" onClick={onConnectors}><Icon name="plug" />Connectors<Icon name="next" /></button>}
  </>;
}
