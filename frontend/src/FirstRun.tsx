import { useState, type KeyboardEvent } from "react";
import { message, request, useResource } from "./api";
import { Avatar } from "./Avatar";
import { Icon, type IconName } from "./Icon";
import { RECOMMENDED_AGENT, startState } from "./firstRunStart";
import { parseSetup } from "./types";

const AGENT_ICONS: Record<string, IconName> = { claude: "claude", codex: "openai", pi: "pi" };

// FirstRun is the page the board's root shows while no CFO runs. It shows as
// done what the terminal quick start already knows, the home the CFO starts
// in and the agent it remembered, offers the agents as one row of icon tabs,
// and Start starts the CFO in the home and hands over to its terminal. It
// asks for no project: the CFO works across every project from its home. The
// projects folder, where goblins find a project by its name, is his to enter
// or leave. A quiet link shows the board without a CFO, so goblins at work
// stay in view.
export function FirstRun({ instance, onStarted, onBoard }: { instance: string; onStarted: () => void; onBoard: () => void }) {
  // typed is what the Overlord typed in the field, which shows the recorded
  // folder until he types; asked is the folder last looked at, and null,
  // which an empty Look goes back to, opens on the recorded one.
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
  const { agent, blocked } = startState(data, picked, asked !== null);
  const shown = data.agents.find((each) => each.id === agent);
  const remembered = data.agents.find((each) => each.id === data.agent);
  const start = async () => {
    setStarting(true);
    setFailure("");
    try {
      // A folder is sent only once he has looked at one: the CFO starts
      // without it, and the recorded folder stays as it is.
      await request("/api/setup/start", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ root: asked === null ? "" : data.projects_root, agent }) });
      onStarted();
    } catch (error) {
      setFailure(message(error));
      setup.reload();
    } finally {
      setStarting(false);
    }
  };
  const move = (event: KeyboardEvent<HTMLDivElement>) => {
    const step = event.key === "ArrowRight" ? 1 : event.key === "ArrowLeft" ? -1 : 0;
    if (!step || !data.agents.length) return;
    event.preventDefault();
    const next = data.agents[(data.agents.findIndex((each) => each.id === agent) + step + data.agents.length) % data.agents.length];
    setPicked(next.id);
    document.getElementById("agent-tab-" + next.id)?.focus();
  };
  return <section className="first-run" aria-labelledby="first-run-title">
    <header className="first-run-head"><Avatar persona="cfo" /><div><h2 id="first-run-title">Start Code Goblins</h2><p className="muted">Start the CFO. It opens in the board's terminal and works across every project from its home.</p></div></header>
    <ul className="first-run-done" aria-label="Already set up">
      <li><Icon name="check" /><strong>Home</strong><span className="first-run-path">{data.home}</span></li>
      {remembered && <li><Icon name="check" /><strong>Agent</strong><span>{remembered.name}, remembered for this home</span></li>}
    </ul>
    <div className="first-run-step">
      <span className="first-run-label" id="agent-label">Agent</span>
      <div className="agent-tabs" role="tablist" aria-labelledby="agent-label" onKeyDown={move}>
        {data.agents.map((each) => <button key={each.id} type="button" role="tab" id={"agent-tab-" + each.id} className="agent-tab" aria-selected={each.id === agent} aria-controls="agent-panel" tabIndex={each.id === agent ? 0 : -1} onClick={() => setPicked(each.id)}>
          <Icon name={AGENT_ICONS[each.id] || "sparkle"} />
          <span>{each.name}</span>
          {each.id === RECOMMENDED_AGENT && <span className="agent-recommended">Recommended</span>}
        </button>)}
      </div>
      {shown && <div className="agent-panel" role="tabpanel" id="agent-panel" aria-labelledby={"agent-tab-" + shown.id}>
        <span className="agent-state">
          <span className={shown.installed ? "yes" : "no"}><Icon name={shown.installed ? "check" : "close"} />{shown.installed ? "Installed" : "Not installed"}</span>
          <span className={shown.signed_in ? "yes" : "no"}><Icon name={shown.signed_in ? "check" : "close"} />{shown.signed_in ? "Signed in" : "Not signed in"}</span>
        </span>
        {shown.reason && <small>{shown.reason}</small>}
      </div>}
    </div>
    <form className="first-run-step" onSubmit={(event) => { event.preventDefault(); setAsked(folder.trim() || null); }}>
      <label htmlFor="projects-folder">Projects folder <span className="muted">(optional)</span></label>
      <div className="first-run-folder">
        <input id="projects-folder" value={folder} onChange={(event) => setTyped(event.target.value)} placeholder="C:\dev" spellCheck={false} autoComplete="off" />
        <button type="submit"><Icon name="folder" />Look</button>
      </div>
      {data.problem ? <p className="warning-text" role="alert">{data.problem}</p>
        : <p className="muted">{data.checkouts.length ? `Goblins find ${data.checkouts.length === 1 ? "1 project" : data.checkouts.length + " projects"} here by name: ${data.checkouts.join(", ")}.` : "The folder that holds your git checkouts, where goblins find a project by its name."}</p>}
    </form>
    {failure && <p className="warning-text" role="alert">{failure}</p>}
    <div className="first-run-actions">
      <span className="muted">{blocked}</span>
      <button className="primary" disabled={!!blocked || starting} onClick={start}><Icon name="play" />{starting ? "Starting the CFO…" : "Start the CFO"}</button>
    </div>
    <button type="button" className="quiet-link" onClick={onBoard}>Open the board without a CFO</button>
  </section>;
}
