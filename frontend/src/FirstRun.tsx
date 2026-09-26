import { useState } from "react";
import { message, request, useResource } from "./api";
import { Avatar } from "./Avatar";
import { Icon, type IconName } from "./Icon";
import { startState } from "./firstRunStart";
import { parseSetup } from "./types";

const AGENT_ICONS: Record<string, IconName> = { claude: "sparkle", codex: "openai", pi: "pi" };

// FirstRun is the board's first-run page at /start: the folder that holds
// the Overlord's projects, the project the CFO starts in, the agent, and
// Start, which starts the CFO and hands over to its terminal.
export function FirstRun({ instance, onStarted }: { instance: string; onStarted: () => void }) {
  // typed is what the Overlord typed in the field, which shows the recorded
  // folder until he types; asked is the folder last looked at, and null
  // opens on the recorded one.
  const [typed, setTyped] = useState<string | null>(null);
  const [asked, setAsked] = useState<string | null>(null);
  const [picked, setPicked] = useState("");
  const [starting, setStarting] = useState(false);
  const [failure, setFailure] = useState("");
  const setup = useResource("/api/setup?root=" + encodeURIComponent(asked ?? ""), parseSetup);
  const folder = typed ?? setup.data?.projects_root ?? "";
  if (setup.error) return <section className="first-run"><p className="warning-text" role="alert">{setup.error}</p><button onClick={setup.reload}>Try again</button></section>;
  if (!setup.data) return <section className="first-run"><p className="loading" role="status">Reading this machine…</p></section>;
  const data = setup.data;
  if (data.cfo_runs) return <section className="first-run" aria-labelledby="first-run-title">
    <header className="first-run-head"><Avatar persona="cfo" /><div><h2 id="first-run-title">The CFO is running</h2><p className="muted">Open its terminal to talk to it.</p></div></header>
    <div className="first-run-actions"><span /><button className="primary" onClick={onStarted}><Icon name="terminal" />Open the CFO's terminal</button></div>
  </section>;
  const { project, blocked } = startState(data, picked);
  const start = async () => {
    setStarting(true);
    setFailure("");
    try {
      await request("/api/setup/start", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ root: data.projects_root, project, agent: "claude" }) });
      onStarted();
    } catch (error) {
      setFailure(message(error));
      setup.reload();
    } finally {
      setStarting(false);
    }
  };
  return <section className="first-run" aria-labelledby="first-run-title">
    <header className="first-run-head"><Avatar persona="cfo" /><div><h2 id="first-run-title">Start Code Goblins</h2><p className="muted">Pick where your projects live and start the CFO. It opens in the board's terminal.</p></div></header>
    <form className="first-run-step" onSubmit={(event) => { event.preventDefault(); setAsked(folder.trim()); }}>
      <label htmlFor="projects-folder">Projects folder</label>
      <div className="first-run-folder">
        <input id="projects-folder" value={folder} onChange={(event) => setTyped(event.target.value)} placeholder="C:\dev" spellCheck={false} autoComplete="off" />
        <button type="submit"><Icon name="folder" />Look</button>
      </div>
      <p className="muted">The folder that holds your git checkouts.</p>
    </form>
    <div className="first-run-step">
      <label htmlFor="cfo-project">Project the CFO starts in</label>
      {data.problem ? <p className="warning-text" role="alert">{data.problem}</p>
        : <select id="cfo-project" value={project} disabled={!data.checkouts.length} onChange={(event) => setPicked(event.target.value)}>
          {!project && <option value="">{data.checkouts.length ? "Pick a project" : "Look at a folder first"}</option>}
          {data.checkouts.map((name) => <option key={name} value={name}>{name}</option>)}
        </select>}
    </div>
    <fieldset className="first-run-step first-run-agents">
      <legend>Agent</legend>
      {data.agents.map((agent) => <label key={agent.id} className={"agent-choice" + (agent.reason ? " unavailable" : "")}>
        <input type="radio" name="agent" value={agent.id} defaultChecked={agent.id === "claude" && !agent.reason} disabled={!!agent.reason} />
        <Icon name={AGENT_ICONS[agent.id] || "sparkle"} />
        <span className="agent-copy">
          <strong>{agent.name}</strong>
          <span className="agent-state">
            <span className={agent.installed ? "yes" : "no"}><Icon name={agent.installed ? "check" : "close"} />{agent.installed ? "Installed" : "Not installed"}</span>
            <span className={agent.signed_in ? "yes" : "no"}><Icon name={agent.signed_in ? "check" : "close"} />{agent.signed_in ? "Signed in" : "Not signed in"}</span>
          </span>
          {agent.reason && <small>{agent.reason}</small>}
        </span>
      </label>)}
    </fieldset>
    {failure && <p className="warning-text" role="alert">{failure}</p>}
    <div className="first-run-actions">
      <span className="muted">{blocked}</span>
      <button className="primary" disabled={!!blocked || starting} onClick={start}><Icon name="play" />{starting ? "Starting the CFO…" : "Start the CFO"}</button>
    </div>
  </section>;
}
