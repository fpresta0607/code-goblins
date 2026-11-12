import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { CeoPin } from "../../src/CeoPin";
import { PanelHeader } from "../../src/PanelHeader";
import { RestartCeoButton } from "../../src/restart-ceo-button";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";
import { RESTING_AFK_TOGGLE } from "./resting-afk-toggle";

// The CEO's bar over a home whose CEO runs in its native terminal, as the
// board shows it, and the CEO's panel: its header and the Restart its Harness
// section holds, or, with #starting,
// one whose CEO has not registered yet. With #comes-back-starting the CEO
// shows as starting once a restart is answered, as a restarted CEO does
// until it registers. What the bar's actions would open is written under it;
// Restart makes its own request, which each test answers.
const comesBackStarting = location.hash === "#comes-back-starting";
const currentHarness = new URLSearchParams(location.search).get("harness") || "claude";
const board = (isStarting: boolean) => parseSnapshot({ healthy: true, instance: "fixture", ceo_runs: true, ceo_starting: isStarting, ceo_terminal: "ceo", ceo_terminal_since: "2026-10-05T09:00:00Z", ceo_harness: isStarting ? "" : currentHarness, tasks: [
  { id: "cg-board-kill", title: "cg-board-kill", project: "code-goblins", phase: "working", generation: "cg-board-kill-1", verified: false },
] });

function Page() {
  const [said, setSaid] = useState("");
  const [isStarting, setStarting] = useState(location.hash === "#starting");
  const snapshot = board(isStarting);
  useEffect(() => {
    if (!comesBackStarting) return;
    const original = window.fetch;
    window.fetch = async (...args) => {
      const response = await original(...args);
      setStarting(true);
      return response;
    };
    return () => { window.fetch = original; };
  }, []);
  return <main style={{ display: "grid", alignContent: "start", gap: 28, minHeight: "100vh", boxSizing: "border-box", padding: 24 }}>
    <section className="task-board" aria-label="Task board">
      <CeoPin afkToggle={RESTING_AFK_TOGGLE} snapshot={snapshot} now={Date.now()} onOpen={() => setSaid("opened the CEO's terminal")} onCommand={() => setSaid("opened the Command Center")} onStart={() => setSaid("opened the first-run page")} />
      <section className="board-column" aria-label="Working"><h2>Working</h2></section>
    </section>
    <section className="goblin-panel" aria-label="CEO panel"><PanelHeader snapshot={snapshot} onAnswer={() => {}} onOpenTask={() => {}} onOpenLog={() => {}} /><RestartCeoButton snapshot={snapshot} /></section>
    <output aria-label="Opened">{said}</output>
  </main>;
}

createRoot(document.getElementById("root")!).render(<Page />);
