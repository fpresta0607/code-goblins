import { createRoot } from "react-dom/client";
import { Orchestration } from "../../src/Orchestration";
import { Lineage } from "../../src/Lineage";
import { Board } from "../../src/Board";
import { WhatsWorking } from "../../src/WhatsWorking";
import { watchTips } from "../../src/tips";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The busy fleet of the approved mockups: one goblin with two sub-agents and
// a dev server, one in its gate, one idle. ?view picks what shows: the canvas
// (the default), the lineage list the canvas becomes at phone width, the
// goblin panel's What's working, or the board's cards, where a goblin with a
// silent child says so.
const now = Date.now();
const ago = (minutes: number) => new Date(now - minutes * 60_000).toISOString();
const megabytes = (count: number) => count * 2 ** 20;
const child = (fields: Record<string, unknown>) => ({ parent: "", detail: "", last_line: "", memory: 0, finished: "", started: ago(10), last_activity: ago(1), source_updated_at: ago(1), fetched_at: ago(0), ...fields });
const tree = (task: string, children: Record<string, unknown>[], memory: number, own: number) => ({ task_id: task, generation: "g1", harness: "claude", memory, own_memory: own, children, source_updated_at: ago(0), fetched_at: ago(0.1) });

const builder = [
  child({ id: "subagent:toolu_A", kind: "subagent", label: "Map harness plumbing", detail: "Explore", state: "working", started: ago(4), last_activity: ago(0.5) }),
  child({ id: "subagent:toolu_B", kind: "subagent", label: "Research MCP OAuth", detail: "general-purpose", state: "done", started: ago(40), finished: ago(12), last_activity: ago(12) }),
  child({ id: "process:5120:1", kind: "process", group: "dev-server", label: "Dev server :5173", detail: "node, 2 processes", state: "waiting", started: ago(38), last_activity: ago(38), memory: megabytes(412) }),
];
// The panel's goblin has more under it: a test run, a background shell gone
// silent, and a monitor that finished.
const busier = [
  ...builder,
  child({ id: "process:6100:2", kind: "process", group: "test", label: "Test run", detail: "go, monitor.test, 3 processes", state: "working", started: ago(3), memory: megabytes(1228) }),
  child({ id: "shell:b1", kind: "shell", group: "test", label: "Run the affected Go tests", detail: "background bash", state: "silent", started: ago(30), last_activity: ago(14), last_line: "ok   internal/monitor 41.2s" }),
  child({ id: "monitor:m1", kind: "monitor", label: "CI checks on PR 398", detail: "monitor", state: "done", started: ago(30), finished: ago(9), last_activity: ago(9) }),
];
const view = new URLSearchParams(location.search).get("view") || "canvas";
const task = (fields: Record<string, unknown>) => ({ harness: "claude", model: "claude-opus-5-5", effort: "xhigh", verified: false, generation: "g1", since: ago(90), ...fields });
const snapshot = parseSnapshot({
  healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude",
  tasks: [
    task({ id: "cg-fleet-tree", title: "Build the fleet tree", project: "code-goblins", phase: "working", tree: tree("cg-fleet-tree", view === "canvas" || view === "lineage" ? builder : busier, megabytes(2662), megabytes(1024)) }),
    task({ id: "acme-billing", title: "Stream the billing CSV export", project: "acme-api", phase: "review", gate_step: "test",
      tree: tree("acme-billing", [child({ id: "gate:01M3", kind: "gate", label: "Gate: test", detail: "test step running", state: "working", started: ago(6), last_line: "go test ./...", memory: megabytes(1126) })], megabytes(2000), megabytes(700)) }),
    task({ id: "acme-checkout", title: "Fix the flaky checkout test", project: "acme-web", phase: "idle", runtime: { state: "idle", reason: "at its prompt", at: ago(2) }, tree: tree("acme-checkout", [], megabytes(600), megabytes(600)) }),
  ],
});

function Fixture() {
  if (view === "lineage") return <main className="canvas-region" style={{ width: "100%" }}><Lineage snapshot={snapshot} project="" selected={null} effects={[]} presentations={[]} now={now} onSelect={() => {}} /></main>;
  if (view === "panel") return <aside className="context-pane"><div className="panel-content"><WhatsWorking tree={snapshot.tasks[0].tree!} now={now} /></div></aside>;
  if (view === "cards") return <Board snapshot={snapshot} layout="stacked" now={now} presentations={[]} onSelect={() => {}} onTerminal={() => {}} onOpenCfo={() => {}} onOpenCommand={() => {}} onStartCfo={() => {}} cardStart={() => ({ blocked: "", problem: "", onStart: () => {} })} />;
  return <main style={{ height: "100vh", display: "flex", flexDirection: "column" }}>
    <Orchestration snapshot={snapshot} selected="" connected effects={[]} presentations={[]} now={now} onSelect={() => {}} />
  </main>;
}
watchTips();
createRoot(document.getElementById("root")!).render(<Fixture />);
