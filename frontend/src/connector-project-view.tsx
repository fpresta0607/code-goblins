import { ago, type BrowserLogin } from "./browser-logins";
import { BrowserLoginRow } from "./browser-login-row";
import { ConnectorGroup } from "./connector-group";
import { ConnectorLevelHead } from "./connector-level-head";
import { goblinRows, goblinsOf, groupByGroup } from "./connector-rows";
import { hasChecked } from "./connector-status";
import type { ConnectorViewProps } from "./connector-view-props";

// One project, checked once however many goblins it has: its services with
// the names of their credentials, whether each resolves and what each points
// at, its MCP servers with theirs, its browser logins and the goblins that
// run in it. Signing out of or forgetting a login acts through one of those
// goblins.
export function ConnectorProjectView({ snapshot, rows, now, actions, nameOf, onOpen, project: name, onForget }: ConnectorViewProps & {
  project: string; onForget: (login: BrowserLogin, project: string) => void;
}) {
  const project = snapshot.projects.find((each) => each.name === name);
  if (!project) return <p className="connector-empty">This project is not in the saved snapshot.</p>;
  const goblins = goblinsOf(snapshot, name);
  const goblinList = goblinRows({ ...snapshot, goblins }, nameOf);
  const canActOnLogins = goblins.length > 0;
  return <>
    <ConnectorLevelHead text={name + (hasChecked(project.checkedAt) ? " checked " + ago(project.checkedAt, now) : " has not been checked yet")} refresh={{ level: "project", scope: name }}
      refreshLabel={"Refresh " + name} isChecking={project.isChecking} actions={actions} />
    {project.error && <p className="connector-note is-warning"><strong>Check failed</strong> {project.error}</p>}
    {groupByGroup(rows.filter((row) => row.level === "project" && row.project === name), "project").map((group) => <ConnectorGroup key={group.group} title={group.title} why={group.why} rows={group.rows} now={now} actions={actions} />)}
    {project.browserLogins.length > 0 && <section className="connector-group" aria-label="Browser logins">
      <h3>Browser logins<small>{project.browserLogins.length}</small></h3>
      {!canActOnLogins && <p className="connector-why">Sign out and Forget need a goblin running in this project.</p>}
      <ul className="connector-logins" aria-label="Browser logins">
        {project.browserLogins.map((login) => <BrowserLoginRow key={login.id} login={login} now={now} isBusy={actions.isBusy || !canActOnLogins}
          onSignOut={() => actions.endLogin(name, login.id, "sign-out")} onForget={() => onForget(login, name)} />)}
      </ul>
    </section>}
    {goblinList.length > 0
      ? <ConnectorGroup title="Goblins" why="Open one for what its own session loaded." rows={goblinList} now={now} actions={actions} onOpen={onOpen} />
      : <p className="connector-empty">No goblin is running in this project.</p>}
  </>;
}
