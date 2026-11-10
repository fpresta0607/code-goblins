import { ConnectorGoblin } from "./connector-goblin";
import { askKey, toolGroups, type Connector, type ConnectorUse } from "./connector-list";
import { ConnectorLogo } from "./connector-logo";
import { connectorAbout } from "./connectors";
import { Disclosure } from "./Disclosure";
import { goblinName } from "./task-words";
import type { Task } from "./types";
import type { Reading } from "./use-connectors";

// One connector's page, laid out as Claude's own (the Overlord, 2026-11-10):
// its logo, name and what it does, the goblins that can use it, each with
// Connect or Disconnect and the repairs it offers, and its tools in two
// groups, those that only read and those that can write, each named in
// words (#2303). A harness reports no permission for a tool, so none is
// shown. What each goblin's check was run against, its process and its
// workspace, waits closed at the end. The way back to the list is the
// page's, above it.
export function ConnectorDetail({ connector, goblins, readings, asked, isBusy, onFix, onGive }: {
  connector: Connector; goblins: ReadonlyMap<string, Task>; readings: ReadonlyMap<string, Reading>; asked: ReadonlyMap<string, boolean>; isBusy: boolean;
  onFix: (task: Task, use: ConnectorUse, action: string) => void; onGive: (task: Task, use: ConnectorUse, isGiving: boolean) => void;
}) {
  // A credential is one of its service's variables, not a service of its own.
  const about = connector.kind === "credential" ? "" : connectorAbout(connector.reported);
  const tools = toolGroups(connector);
  const uses = connector.uses.flatMap((use) => {
    const task = goblins.get(use.goblin);
    return task ? [{ use, task, connections: readings.get(use.goblin)?.connections }] : [];
  });
  return <article className="connector-detail" aria-label={connector.name}>
    <header className="connector-head">
      <ConnectorLogo name={connector.reported} />
      <div><h2>{connector.name}</h2><p className="connector-type">{connector.type}</p></div>
    </header>
    {about && <p className="connector-about">{about}</p>}
    <h3>Goblins</h3>
    <ul className="connector-goblins" aria-label={"Goblins that can use " + connector.name}>
      {uses.map(({ use, task, connections }) => <ConnectorGoblin key={use.goblin} connector={connector} use={use} task={task} isBusy={isBusy || !!connections?.checking}
        isChanging={asked.has(askKey(use.goblin, use.entry.id)) && !!connections?.checking} onFix={(action) => onFix(task, use, action)} onGive={(isGiving) => onGive(task, use, isGiving)} />)}
    </ul>
    {tools.readOnly.length + tools.write.length > 0 && <>
      <h3>Tools</h3>
      <div className="connector-tools">
        {([["Read-only tools", tools.readOnly], ["Write tools", tools.write]] as const).map(([label, names]) => names.length > 0 && <Disclosure key={label} kind="connector-tool-group" title={<>{label}<span className="connector-count">{names.length}</span></>}>
          <ul aria-label={label}>{names.map((name, at) => <li key={at}>{name}</li>)}</ul>
        </Disclosure>)}
      </div>
    </>}
    <Disclosure kind="connector-checks" title="Check details">
      {uses.map(({ use, task, connections }) => <p key={use.goblin}><strong>{goblinName(task)}</strong> {connections?.generation ? <>Generation {connections.generation}. </> : null}
        {connections && connections.runtime.processId > 0 ? <>Verified process {connections.runtime.processId}, started {connections.runtime.startedAt}, workspace {connections.runtime.workspace}.</> : "Process and workspace identity unknown."}</p>)}
    </Disclosure>
  </article>;
}
