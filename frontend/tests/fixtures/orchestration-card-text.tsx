import { createRoot } from "react-dom/client";
import { Orchestration } from "../../src/Orchestration";
import { Lineage } from "../../src/Lineage";
import { Board } from "../../src/Board";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The canvas cards with the most text a card holds. Woody is the Overlord's
// screenshot: a paused goblin whose status reads "Allowance: resumes Oct 14,
// 4:05 AM", with no browser line. Wilhelmina is the worst case: a goblin whose
// session reported no parent, so her card also names her relation, with a name
// that takes two lines, her browser active, and a long status. Cornelius has
// his browser active beside a status of two lines, and Bartholomew has a
// project name no line can break, with baby goblins whose names and states
// run long. Jules's baby goblin is the Overlord's second screenshot, "Jules IX
// - Process Wrangler", now working with 4 MB, Mabel's four are his third, which
// he asked about since their cards named no program, and Olive is the board
// card he showed beside them. ?view picks what shows: the canvas (the default), the
// lineage list the canvas becomes at phone width, or the board's cards.
const at = "2026-10-12T12:00:00Z";
const ago = (minutes: number) => new Date(Date.parse(at) - minutes * 60_000).toISOString();
const paused = (reason: string, until: string) => ({
  phase: "paused",
  lifecycle: { phase: "paused", action: "pause", at, kept: [], stopped: [], problems: [], handoff_saved: false, validation_restarts: false, pause: { reason, until, at } },
});
const goblin = (fields: Record<string, unknown>) => ({ project: "code-goblins", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", verified: false, since: at, at, ...fields });
const child = (fields: Record<string, unknown>) => ({ parent: "", detail: "", group: "", task: "", last_line: "", memory: 0, finished: "", started: ago(60), last_activity: ago(5), source_updated_at: at, fetched_at: at, ...fields });
const tree = (task: string, children: Record<string, unknown>[]) => ({ task_id: task, generation: "g-" + task.slice(3), harness: "claude", memory: 0, own_memory: 0, children, source_updated_at: at, fetched_at: at });
// Jules's ninth child is the one still running: the eight before it finished.
const finishedBefore = Array.from({ length: 8 }, (_, i) => child({ id: "subagent:done-" + i, kind: "subagent", label: "Read the logs", state: "done", started: ago(300 - i), finished: ago(200), last_activity: ago(200) }));
const PULL_REQUEST = "https://github.com/fpresta0607/precisiondocs/pull/1556";
const view = new URLSearchParams(location.search).get("view") || "canvas";
const snapshot = parseSnapshot({ healthy: true, instance: "fixture", at, cfo_runs: true, cfo_harness: "claude",
  subscriptions: [{ provider: "claude", status: "available", percent_remaining: 62, read_at: at, resets_at: "2026-10-16T22:27:42Z", source: "oauth", floor_percent: 5 }],
  tasks: [
    goblin({ id: "cg-tour", title: "Show a visitor around the board", goblin_name: "Woody", goblin_title: "Tour Guide", generation: "g-tour", ...paused("allowance", "2026-10-14T11:05:00Z") }),
    goblin({ id: "cg-plumbing", title: "Fix the reliability of the nightly sync", goblin_name: "Wilhelmina", goblin_title: "Senior Reliability Plumber", generation: "g-plumbing", session: "s-plumbing", ...paused("dependency", "task:cg-archive") }),
    goblin({ id: "cg-dispatch", title: "Dispatch the nightly builds", goblin_name: "Cornelius", goblin_title: "Senior Dispatcher", generation: "g-dispatch", ...paused("allowance", "2026-10-14T11:05:00Z") }),
    goblin({ id: "cg-archive", title: "Archive the old infrastructure notes", goblin_name: "Bartholomew", goblin_title: "Distinguished Infrastructure Archivist", project: "code-goblins-infrastructure-observability-monorepo", generation: "g-archive", phase: "working", since: ago(135), pr: "https://github.com/fpresta0607/code-goblins-infrastructure-observability-monorepo/pull/12345",
      tree: tree("cg-archive", [
        child({ id: "subagent:observability", kind: "subagent", label: "Investigate the infrastructure observability", state: "working", started: ago(42), last_activity: ago(1) }),
        child({ id: "process:7001:1", kind: "process", group: "dev-server", label: "Dev server :5173", state: "working", detached: true, started: ago(779), last_activity: ago(1), memory: 1.4 * 2 ** 30 }),
        child({ id: "helper:cg-archive-h1", kind: "helper", label: "Reconcile the nightly infrastructure exports with the observability ledger", task: "Reconcile the exports", state: "working", started: ago(20), last_activity: ago(2), memory: 512 * 2 ** 20 }),
      ]) }),
    goblin({ id: "cg-processes", title: "Keep the fleet's processes in order", goblin_name: "Jules", goblin_title: "Process Keeper", generation: "g-processes", phase: "working", since: ago(48),
      tree: tree("cg-processes", [...finishedBefore, child({ id: "process:4242:9", kind: "process", group: "other", label: "node scripts/watch-queue.mjs", task: "node scripts/watch-queue.mjs", state: "working", started: ago(30), last_activity: ago(0), memory: 4 * 2 ** 20 })]) }),
    goblin({ id: "pd-email", title: "Send the tenant emails", goblin_name: "Mabel", goblin_title: "Email Elf", project: "PrecisionDocs-AI", generation: "g-email", phase: "working", since: ago(35),
      tree: tree("pd-email", [
        ...finishedBefore.slice(0, 4),
        child({ id: "process:2375:4", kind: "process", group: "dev-server", label: "Dev server :2375", task: "Docker Desktop", state: "waiting", detached: true, started: ago(290), last_activity: ago(5), memory: 257 * 2 ** 20 }),
        child({ id: "process:3000:7", kind: "process", group: "dev-server", label: "Dev server :3000", task: "npm run dev", state: "working", detached: true, started: ago(772), last_activity: ago(0), memory: 970 * 2 ** 20 }),
        child({ id: "process:5150:6", kind: "process", group: "other", label: "bash scripts/sync-tenants.sh", task: "bash scripts/sync-tenants.sh --all", state: "waiting", detached: true, started: ago(280), last_activity: ago(6), memory: 20 * 2 ** 20 }),
        child({ id: "process:4949:8", kind: "process", group: "other", label: "stripe listen --forward-to localhost:3000/api/stripe/webhook", task: "stripe listen --forward-to localhost:3000/api/stripe/webhook", state: "waiting", started: ago(0.5), last_activity: ago(0.5), memory: 8 * 2 ** 20 }),
      ]) }),
    goblin({ id: "pd-billing", title: "Bill the pilot tenants", goblin_name: "Olive", goblin_title: "Billing Baron", project: "PrecisionDocs-AI", generation: "g-billing", phase: "working", since: ago(7), pr: PULL_REQUEST }),
  ], sessions: [
    { id: "s-plumbing", native_id: "n-plumbing", harness: "claude", role: "goblin", task_id: "cg-plumbing", generation: "g-plumbing", phase: "working" },
  ] });
const presentations = ["cg-plumbing", "cg-dispatch"].map((task) => ({ id: "p-" + task, kind: "browser", task_id: task, generation: "g-" + task.slice(3), source: "", target: "", state: "active", url: "", at: "", until: "" }));
const now = Date.parse(at);
// How often the CFO's card asked for its terminal, which the test reads.
const opened = window as Window & { cfoOpened?: number };

function Fixture() {
  if (view === "lineage") return <main className="canvas-region" style={{ width: "100%" }}><Lineage onOpenCfo={() => {}} snapshot={snapshot} project="" selected={null} effects={[]} presentations={presentations} now={now} onSelect={() => {}} /></main>;
  if (view === "board") return <Board snapshot={snapshot} layout="stacked" now={now} presentations={presentations} onSelect={() => {}} onSelectTrain={() => {}} onCount={() => {}} onOpenCfo={() => {}} onOpenCommand={() => {}} onStartCfo={() => {}} cardStart={() => ({ blocked: "", onStart: () => Promise.resolve("") })} />;
  return <main style={{ height: "100vh", display: "flex", flexDirection: "column" }}>
    <Orchestration snapshot={snapshot} selected="" connected effects={[]} presentations={presentations} now={now} onSelect={() => {}} onChild={() => {}} onOpenCfo={() => { opened.cfoOpened = (opened.cfoOpened || 0) + 1; }} />
  </main>;
}
createRoot(document.getElementById("root")!).render(<Fixture />);
