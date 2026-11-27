import { placeKey, type Place } from "./connector-place";
import { countWords, needsLook, type ConnectorRow } from "./connector-rows";
import { pcScope, treeProjects, yoursCount } from "./connector-scopes";
import type { ConnectorSnapshot } from "./connector-snapshot";
import { ConnectorTag } from "./connector-tag";

// Where connectors live, the widest scope first (#3010): Needs a look, then
// This PC, which every goblin gets with no grant, then each project, one git
// repository, with the goblins running in it under it, and last the
// Overlord's own Claude and Codex, which goblins start without. Each scope,
// project and goblin opens its page; the one open is marked.
export function ConnectorTree({ snapshot, rows, nameOf, place, onOpen }: {
  snapshot: ConnectorSnapshot; rows: ConnectorRow[]; nameOf: (task: string) => string; place: Place; onOpen: (place: Place) => void;
}) {
  const pc = pcScope(snapshot);
  const projects = treeProjects(snapshot, nameOf);
  const attention = needsLook(rows).length;
  const here = placeKey(place.kind === "home" ? { kind: "look" } : place);
  const node = (target: Place, label: string) => ({
    type: "button" as const, "data-place": placeKey(target), "aria-label": label, "aria-current": placeKey(target) === here ? "location" as const : undefined, onClick: () => onOpen(target),
  });
  const trouble = (isTroubled: boolean) => isTroubled ? ", needs a look" : "";
  return <nav className="connector-tree" aria-label="Where connectors live">
    <button className="connector-node is-look" {...node({ kind: "look" }, "Needs a look, " + attention)}>Needs a look<b>{attention}</b></button>
    <div className="connector-scope">
      <div className="connector-scope-head"><ConnectorTag tone="global" text="Global" /><button className="connector-node" {...node({ kind: "pc" }, "This PC")}>This PC</button></div>
      <p className="connector-why">Every goblin on this PC gets these, with no grant.</p>
      <ul className="connector-tree-list">
        {pc.groups.map((group) => <li key={group.group} className="connector-leaf"><span>{group.title}</span><small>{group.count}</small></li>)}
      </ul>
    </div>
    <div className="connector-scope">
      <div className="connector-scope-head">
        <ConnectorTag tone="local" text="Local" />
        <button className="connector-node" {...node({ kind: "projects" }, "Projects, " + snapshot.projects.length + ", one git repository each")}>Projects<small>{snapshot.projects.length}, one git repository each</small></button>
      </div>
      <p className="connector-why">Each project's goblins get what it declares, when granted.</p>
      <ul className="connector-tree-list">
        {projects.shown.map((project) => <li key={project.name}>
          <button className="connector-node" {...node({ kind: "project", project: project.name }, project.name + ", " + project.about + trouble(project.isTroubled))}>
            <span>{project.name}{project.isTroubled && <i className="connector-dot" />}</span><small>{project.about}</small>
          </button>
          {project.goblins.length > 0 && <ul className="connector-tree-goblins">
            {project.goblins.map((goblin) => <li key={goblin.task}>
              <button className="connector-node" {...node({ kind: "goblin", task: goblin.task }, goblin.name + ", carries " + goblin.carries + trouble(goblin.isTroubled))}>
                <span><ConnectorTag tone="goblin" text="Goblin" />{goblin.name}{goblin.isTroubled && <i className="connector-dot" />}</span><small>carries {goblin.carries}</small>
              </button>
            </li>)}
          </ul>}
        </li>)}
        {projects.more > 0 && <li><button type="button" className="connector-node is-more" onClick={() => onOpen({ kind: "projects" })}>{countWords(projects.more, "more project")}, those with a goblin first</button></li>}
      </ul>
    </div>
    <div className="connector-scope is-yours">
      <div className="connector-scope-head">
        <ConnectorTag tone="yours" text="Yours only" />
        <button className="connector-node" {...node({ kind: "yours" }, "Your Claude and Codex, " + yoursCount(snapshot))}>Your Claude and Codex<small>{yoursCount(snapshot)}</small></button>
      </div>
      <p className="connector-why">Connectors and servers of your own sessions. Goblins start without them.</p>
    </div>
  </nav>;
}
