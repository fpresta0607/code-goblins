import { createRoot } from "react-dom/client";
import { ConnectionsPanel } from "../../src/connections-panel";
import { parseSnapshot } from "../../src/types";
import { watchTips } from "../../src/tips";
import "../../src/styles.css";

const root = document.getElementById("root");
if (!root) throw new Error("Fixture root is missing");
watchTips();
const snapshot = parseSnapshot({ instance: "layout-proof", healthy: true, tasks: [{ id: "connection-layout", generation: "first", phase: "working", verified: false }] });
createRoot(root).render(<main className="panel-content" style={{ maxWidth: 1100, margin: "auto" }}><ConnectionsPanel task={snapshot.tasks[0]} /></main>);
