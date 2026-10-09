import { createRoot } from "react-dom/client";
import { Orchestration } from "../../src/Orchestration";
import { watchTips } from "../../src/tips";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// A goblin with seven running baby goblins and a goblin card under them,
// beside two goblins with three and four baby goblins of their own, on the
// canvas, or with ?view=narrow, without the card under them, on a canvas as
// narrow as the one beside an open panel, where the goblins take two rows.
const now = Date.now();
const ago = (minutes: number) => new Date(now - minutes * 60_000).toISOString();
const child = (id: string, label: string, minutes: number, fields: Record<string, unknown> = {}) => ({ id, kind: "subagent", group: "", parent: "", label, detail: "Explore", task: label, state: "working", memory: 0, finished: "", last_line: "", started: ago(minutes), last_activity: ago(1), source_updated_at: ago(1), fetched_at: ago(0), ...fields });
const tree = (task: string, children: Record<string, unknown>[]) => ({ task_id: task, generation: "g1", harness: "claude", memory: 2 ** 30, own_memory: 2 ** 29, children, source_updated_at: ago(0), fetched_at: ago(0.1) });
const task = (id: string, title: string, fields: Record<string, unknown> = {}) => ({ id, title, project: "code-goblins", phase: "working", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", verified: false, generation: "g1", session: "s-" + id, backend: "native", since: ago(90), ...fields });
const session = (id: string, role: string, parent: string, taskID = "") => ({ id, native_id: id, harness: "claude", role, task_id: taskID, generation: taskID ? "g1" : "", parent, relation: parent ? "Dispatched by the CFO" : "", phase: "working", updated_at: ago(0) });
const isNarrow = new URLSearchParams(location.search).get("view") === "narrow";
const snapshot = parseSnapshot({
  healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [],
  sessions: [
    session("s-cfo", "cfo", ""), session("s-fleet", "goblin", "s-cfo", "fleet"), session("s-billing", "goblin", "s-cfo", "billing"),
    session("s-canvas", "goblin", "s-cfo", "canvas"), ...isNarrow ? [] : [session("s-notes", "goblin", "s-fleet", "notes")],
  ],
  tasks: [
    task("fleet", "Build the fleet tree", { goblin_name: "Grub", goblin_title: "Tree Surgeon", tree: tree("fleet", [
      child("subagent:a", "Map harness plumbing", 50), child("subagent:b", "Research MCP OAuth", 45),
      child("shell:c", "Run the affected Go tests", 40, { kind: "shell", group: "test" }),
      child("process:d", "Dev server :5173", 38, { kind: "process", group: "dev-server", state: "waiting", memory: 412 * 2 ** 20 }),
      child("subagent:e", "Trace the export", 36), child("subagent:f", "Check the phone list", 30), child("subagent:g", "Measure the branches", 20),
    ]) }),
    task("billing", "Stream the billing CSV export", { goblin_name: "Kip", goblin_title: "Echo Chaser", tree: tree("billing", [
      child("subagent:h", "Trace the writer", 30), child("subagent:i", "Verify the replies", 20), child("subagent:j", "Find the flaky assertion", 10),
    ]) }),
    task("canvas", "Polish the canvas", { goblin_name: "Moss", goblin_title: "Pixel Wrangler", tree: tree("canvas", [
      child("subagent:k", "Measure the gaps", 30), child("subagent:l", "Check the zoom", 25), child("subagent:m", "Render the fan", 20), child("subagent:n", "Review the drops", 10),
    ]) }),
    ...isNarrow ? [] : [task("notes", "Draft the release notes")],
  ],
});
watchTips();
createRoot(document.getElementById("root")!).render(<main style={{ height: "100vh", width: isNarrow ? 836 : "100%", display: "flex", flexDirection: "column" }}>
  <Orchestration snapshot={snapshot} selected="" connected effects={[]} presentations={[]} now={now} onSelect={() => {}} onChild={() => {}} />
</main>);
