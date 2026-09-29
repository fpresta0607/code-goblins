import { createRoot } from "react-dom/client";
import { WorkspaceDetails } from "../../src/WorkspaceDetails";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

const snapshot = parseSnapshot({ instance: "connections-proof", healthy: true, tasks: [
  { id: "connection-proof", generation: "first", project: "code-goblins", title: "Build review panel", phase: "working", verified: false },
] });
const root = document.getElementById("root");
if (!root) throw new Error("Fixture root is missing");
createRoot(root).render(<main className="panel-content" style={{ maxWidth: 720, margin: "auto" }}><WorkspaceDetails task={snapshot.tasks[0]} onRepair={(key) => { document.title = key; }} /></main>);
