import { createRoot } from "react-dom/client";
import { CommandCenter } from "../../src/CommandCenter";
import { parseSnapshot, type BoardActivity } from "../../src/types";
import "../../src/styles.css";

// The CFO needs a command run, cg-board-kill runs its own test walkthrough,
// as it did three times on 2026-09-28, and the checkout goblin runs one it
// asks the Overlord to watch.
const now = Date.now();
const at = new Date(now - 2 * 60_000).toISOString();
const until = new Date(now + 5 * 60_000).toISOString();
const snapshot = parseSnapshot({
  healthy: true, instance: "fixture", revision: 1,
  tasks: [
    { id: "cg-board-kill", title: "Kill switch for the board", phase: "working", generation: "1", verified: false },
    { id: "checkout-race", title: "Checkout race fix", phase: "working", generation: "1", verified: false },
  ],
  runs: [{ id: "fly-login-deploy", identity: "c".repeat(64), title: "Sign in to Fly for the deploy", shell: "powershell", command: "fly auth login", cwd: "C:\\dev", state: "ready", created_at: new Date(now - 12 * 60_000).toISOString() }],
});
const walkthrough = (id: string, task: string, url: string, watch = ""): BoardActivity => ({ id, kind: "browser", task_id: task, generation: "1", source: "", target: "", state: "active", url, at, until, watch });
const presentations = [
  walkthrough("kill-switch-test", "cg-board-kill", "http://127.0.0.1:5173/kill"),
  walkthrough("checkout-walkthrough", "checkout-race", "http://127.0.0.1:5174/checkout", "Watch the checkout walkthrough I am running for you"),
];

createRoot(document.getElementById("root")!).render(<main><CommandCenter snapshot={snapshot} connected presentations={presentations} focus={null} onUnsent={() => {}} /></main>);
