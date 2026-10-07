import { createRoot } from "react-dom/client";
import { Orchestration } from "../../src/Orchestration";
import { Lineage } from "../../src/Lineage";
import { Board } from "../../src/Board";
import { watchTips } from "../../src/tips";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// A goblin with a helper at work under it, and another goblin's helper whose
// parent is paused, so no tree holds it. ?view picks the canvas (the
// default), the lineage list it becomes at phone width, or the board's cards.
const now = Date.now();
const ago = (minutes: number) => new Date(now - minutes * 60_000).toISOString();
const megabytes = (count: number) => count * 2 ** 20;
const child = (fields: Record<string, unknown>) => ({ parent: "", group: "", detail: "", last_line: "", memory: 0, finished: "", started: ago(10), last_activity: ago(1), source_updated_at: ago(1), fetched_at: ago(0), ...fields });
const tree = (task: string, children: Record<string, unknown>[]) => ({ task_id: task, generation: "g1", harness: "claude", memory: megabytes(900), own_memory: megabytes(600), children, source_updated_at: ago(0), fetched_at: ago(0.1) });
const task = (fields: Record<string, unknown>) => ({ harness: "claude", model: "claude-opus-5-5", effort: "xhigh", verified: false, generation: "g1", since: ago(90), project: "code-goblins", ...fields });
const view = new URLSearchParams(location.search).get("view") || "canvas";
const snapshot = parseSnapshot({
  healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude",
  tasks: [
    task({ id: "cg-helpers", title: "Let goblins start helpers", phase: "working",
      tree: tree("cg-helpers", [child({ id: "helper:cg-helpers-h1", kind: "helper", label: "Accounts migration", detail: "Helper goblin cg-helpers-h1", state: "working", started: ago(4), last_activity: ago(0.5), memory: megabytes(512) })]) }),
    task({ id: "cg-helpers-h1", parent: "cg-helpers", title: "Accounts migration", phase: "working", since: ago(4), tree: tree("cg-helpers-h1", []) }),
    task({ id: "acme-sync", title: "Sync the ledger", phase: "paused", lifecycle: { phase: "paused", action: "pause", at: ago(3), kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false, pause: { reason: "overlord", until: "", at: ago(3) } } }),
    task({ id: "acme-sync-h1", parent: "acme-sync", title: "Ledger fixtures", phase: "working", since: ago(8) }),
  ],
});

function Fixture() {
  if (view === "lineage") return <main className="canvas-region" style={{ width: "100%" }}><Lineage snapshot={snapshot} project="" selected={null} effects={[]} presentations={[]} now={now} onSelect={() => {}} /></main>;
  if (view === "cards") return <Board snapshot={snapshot} layout="stacked" now={now} presentations={[]} onSelect={() => {}} onTerminal={() => {}} onOpenCfo={() => {}} onOpenCommand={() => {}} onStartCfo={() => {}} cardStart={() => ({ blocked: "", problem: "", onStart: () => {} })} />;
  return <main style={{ height: "100vh", display: "flex", flexDirection: "column" }}>
    <Orchestration snapshot={snapshot} selected="" connected effects={[]} presentations={[]} now={now} onSelect={() => {}} />
  </main>;
}
watchTips();
createRoot(document.getElementById("root")!).render(<Fixture />);
