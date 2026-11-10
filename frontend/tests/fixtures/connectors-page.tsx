import { createRoot } from "react-dom/client";
import { knowInstance } from "../../src/api";
import { ConnectorsPage } from "../../src/connectors-page";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The Connectors page alone in the board's canvas, for the specs of what it
// reads and when: one goblin, or as many as ?goblins= names, each running.
// A repair's Command Center card is the page's title, finishRepair ends a
// repair of the first goblin as the supervisor's snapshot would, and
// restartGoblins puts every goblin on a new session, as a restart does.
const count = Number(new URLSearchParams(location.search).get("goblins")) || 1;
const tasksOn = (generation: string) => Array.from({ length: count }, (_, at) => ({
  id: at ? "connection-proof-" + (at + 1) : "connection-proof", generation, project: "code-goblins", title: at ? "Goblin " + (at + 1) : "Build review panel",
  phase: "working", verified: false, backend: "native", harness: "codex", model: "gpt-6-astra", effort: "xhigh",
}));
const root = document.getElementById("root");
if (!root) throw new Error("Fixture root is missing");
const board = createRoot(root);
const render = (runs: unknown[], generation = "first") => {
  const snapshot = parseSnapshot({ instance: "connections-proof", healthy: true, tasks: tasksOn(generation), runs });
  board.render(<main className="canvas-region" aria-label="Connectors" style={{ height: "100vh" }}><ConnectorsPage snapshot={snapshot} onRepair={(key) => { document.title = key; }} /></main>);
};
Object.assign(window, {
  finishRepair: (id: string) => render([{ id, state: "succeeded", finished_at: new Date().toISOString(), connection_task: "connection-proof", connection_generation: "first" }]),
  restartGoblins: () => render([], "second"),
});
knowInstance("connections-proof");
render([]);
