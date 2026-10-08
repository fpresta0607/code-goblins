import { createRoot } from "react-dom/client";
import { WorkspaceDetails } from "../../src/WorkspaceDetails";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

const tasks = [{ id: "connection-proof", generation: "first", project: "code-goblins", title: "Build review panel", phase: "working", verified: false, backend: "native", harness: "codex", model: "gpt-6-astra", effort: "xhigh" }];
const root = document.getElementById("root");
if (!root) throw new Error("Fixture root is missing");
const board = createRoot(root);
const render = (runs: unknown[]) => {
  const snapshot = parseSnapshot({ instance: "connections-proof", healthy: true, tasks, runs });
  board.render(<main className="panel-content" style={{ maxWidth: 720, margin: "auto" }}><WorkspaceDetails task={snapshot.tasks[0]} runs={snapshot.runs} instance={snapshot.instance} onRepair={(key) => { document.title = key; }} /></main>);
};
Object.assign(window, { finishRepair: (id: string) => render([{ id, state: "succeeded", finished_at: new Date().toISOString(), connection_task: "connection-proof", connection_generation: "first" }]) });
render([]);
