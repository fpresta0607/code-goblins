import { ago, type BrowserLogin } from "./browser-logins";
import { BrowserLoginRow } from "./browser-login-row";
import { ConnectorLevelHead } from "./connector-level-head";
import { countWords, goblinRows, goblinsOf } from "./connector-rows";
import { pcScope, projectScope } from "./connector-scopes";
import { hasChecked } from "./connector-status";
import { ConnectorTitle } from "./connector-title";
import type { ConnectorViewProps } from "./connector-view-props";
import { ConnectorWhere } from "./connector-where";

// One project, one git repository, checked once however many goblins it has
// (#3010): the MCP servers its repository declares in .mcp.json, shared with
// everyone who clones it, apart from the services Code Goblins keeps private
// to this PC in its auth.json, each with the names of its credentials, whether
// each resolves, what it points at and which goblins carry it; what this PC
// gives every goblin; its browser logins; and the goblins that run in it.
// Signing out of or forgetting a login acts through one of those goblins.
export function ConnectorProjectView({ snapshot, rows, now, actions, nameOf, onOpen, project: name, onForget }: ConnectorViewProps & {
  project: string; onForget: (login: BrowserLogin, project: string) => void;
}) {
  const project = snapshot.projects.find((each) => each.name === name);
  if (!project) return <p className="connector-empty">This project is not in the saved snapshot.</p>;
  const goblins = goblinsOf(snapshot, name);
  const goblinList = goblinRows({ ...snapshot, goblins }, nameOf);
  const canActOnLogins = goblins.length > 0;
  const { servers, services, folded } = projectScope(snapshot, rows, name, nameOf);
  const pc = pcScope(snapshot);
  const where = { now, actions };
  return <>
    <ConnectorTitle name={name} tone="local" tag="Local" about={project.path + " · one git repository"} />
    <ConnectorLevelHead text={name + (hasChecked(project.checkedAt) ? " checked " + ago(project.checkedAt, now) : " has not been checked yet")} refresh={{ level: "project", scope: name }}
      refreshLabel={"Refresh " + name} isChecking={project.isChecking} actions={actions} />
    {project.error && <p className="connector-note is-warning"><strong>Check failed</strong> {project.error}</p>}
    <ConnectorWhere {...where} tone="repo" title="In the repository" file=".mcp.json" count={countWords(servers.length, "server")}
      why="Shared with everyone who clones it. Changed through a pull request." rows={servers} empty="Its repository declares no MCP server." />
    <ConnectorWhere {...where} tone="private" title="Private to this PC" file="auth.json" count={countWords(services.length + folded.length, "service")}
      why="Kept by Code Goblins for this project, its values in the vault. A goblin gets one when granted." rows={services} empty="Code Goblins keeps no service for it."
      fold={{ title: countWords(folded.length, "more service") + ", none needs a look", rows: folded }} />
    <ConnectorWhere {...where} tone="global" title="From this PC" count={String(pc.total)} why={pc.words + ", the same for every project."} />
    {project.browserLogins.length > 0 && <section className="connector-group" aria-label="Browser logins">
      <h4>Browser logins<small>{project.browserLogins.length}</small></h4>
      {!canActOnLogins && <p className="connector-why">Sign out and Forget need a goblin running in this project.</p>}
      <ul className="connector-logins" aria-label="Browser logins">
        {project.browserLogins.map((login) => <BrowserLoginRow key={login.id} login={login} now={now} isBusy={actions.isBusy || !canActOnLogins}
          onSignOut={() => actions.endLogin(name, login.id, "sign-out")} onForget={() => onForget(login, name)} />)}
      </ul>
    </section>}
    <ConnectorWhere {...where} tone="goblin" title="Goblins" count={String(goblinList.length)} why="Open one for what it can reach and what its own session loaded."
      rows={goblinList} empty="No goblin is running in this project." onOpen={onOpen} />
  </>;
}
