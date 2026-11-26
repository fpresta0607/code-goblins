import { createRoot } from "react-dom/client";
import { knowInstance } from "../../src/api";
import { ConnectorsPage } from "../../src/connectors-page";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The Connectors page alone in the board's canvas, for the specs of what it
// reads and when. ?goblins= is a JSON list of [task, name, title] the board
// runs, which are Zeke and Ivy when it is not given. A repair's Command Center
// card is the page's title, finishRepair ends a repair as the supervisor's
// snapshot would, and restartGoblins puts every goblin on a new session, as a
// restart does.
const named = new URLSearchParams(location.search).get("goblins");
const goblins: [string, string, string][] = named ? JSON.parse(named) : [["cg-touch", "Zeke", "Touch Wrangler"], ["cg-other", "Ivy", "Window Painter"]];
const tasksOn = (generation: string) => goblins.map(([id, name, title]) => ({
  id, generation: generation === "first" ? id + "-1" : id + "-2", project: "acme", title: "A made-up task of " + name, goblin_name: name, goblin_title: title,
  phase: "working", verified: false, backend: "native", harness: "claude", model: "claude-opus-5-5", effort: "xhigh",
}));
const root = document.getElementById("root");
if (!root) throw new Error("Fixture root is missing");
const board = createRoot(root);
const render = (runs: unknown[], generation = "first") => {
  const snapshot = parseSnapshot({ instance: "connectors-proof", healthy: true, tasks: tasksOn(generation), runs });
  board.render(<main className="canvas-region" aria-label="Connectors" style={{ height: "100vh" }}><ConnectorsPage snapshot={snapshot} onRepair={(key) => { document.title = key; }} /></main>);
};
Object.assign(window, {
  finishRepair: (id: string) => render([{ id, state: "succeeded", finished_at: new Date().toISOString() }]),
  restartGoblins: () => render([], "second"),
});
knowInstance("connectors-proof");
render([]);
