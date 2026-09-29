import { useState } from "react";
import { message, request, useResource } from "./api";
import { Avatar } from "./Avatar";
import { Icon, type IconName } from "./Icon";
import { startState } from "./firstRunStart";
import { parseSetup } from "./types";

const AGENT_ICONS: Record<string, IconName> = { claude: "sparkle", codex: "openai", pi: "pi" };

export function FirstRun({ instance, onStarted }: { instance: string; onStarted: () => void }) {
  const [picked, setPicked] = useState("");
  const [isStarting, setIsStarting] = useState(false);
  const [failure, setFailure] = useState("");
  const setup = useResource("/api/setup", parseSetup);
  if (setup.error) return <section className="first-run"><p className="warning-text" role="alert">{setup.error}</p><button onClick={setup.reload}>Try again</button></section>;
  if (!setup.data) return <section className="first-run"><p className="loading" role="status">Checking your agents...</p></section>;
  const data = setup.data;
  if (data.cfo_runs) return <section className="first-run" aria-labelledby="first-run-title">
    <header className="first-run-head"><Avatar persona="cfo" /><div><h2 id="first-run-title">Your CFO is running</h2><p className="muted">Open its terminal to continue.</p></div></header>
    <button autoFocus className="primary" onClick={onStarted}><Icon name="terminal" />Open CFO terminal (default)</button>
  </section>;
  const { agent, blocked } = startState(data, picked);
  const start = async () => {
    setIsStarting(true);
    setFailure("");
    try {
      await request("/api/setup/start", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ agent }) });
      onStarted();
    } catch (error) {
      setFailure(message(error));
      setup.reload();
    } finally {
      setIsStarting(false);
    }
  };
  return <section className="first-run" aria-labelledby="first-run-title">
    <header className="first-run-head"><Avatar persona="cfo" /><div><h2 id="first-run-title">Start your CFO</h2><p className="muted">One home for all your projects and files.</p></div></header>
    <p>Home <code>{data.home}</code></p>
    <form onSubmit={(event) => { event.preventDefault(); if (!blocked && !isStarting) void start(); }}>
      <fieldset className="first-run-step first-run-agents" disabled={isStarting}>
        <legend>Choose your CFO</legend>
        {data.agents.map((choice) => <label key={choice.id} className={"agent-choice" + (!choice.installed || !choice.signed_in ? " unavailable" : "")}>
          <input type="radio" name="agent" value={choice.id} checked={agent === choice.id} onChange={() => setPicked(choice.id)} />
          <Icon name={AGENT_ICONS[choice.id] || "sparkle"} />
          <span className="agent-copy"><strong>{choice.name}{choice.id === data.default_agent ? " (Your default)" : ""}</strong>
            <span className="agent-state">{choice.installed && choice.signed_in ? "Ready" : choice.reason || "Setup needed"}</span>
          </span>
        </label>)}
      </fieldset>
      <p className="muted">Run <code>goblins setup</code> in a terminal to install an agent or sign in.</p>
      {failure && <p className="warning-text" role="alert">{failure}</p>}
      <div className="first-run-actions"><span className="muted">{blocked}</span><button autoFocus type="submit" className="primary" disabled={!!blocked || isStarting}><Icon name="play" />{isStarting ? "Starting CFO..." : "Start CFO (default)"}</button></div>
      <p className="muted">Press Enter to continue.</p>
    </form>
  </section>;
}
