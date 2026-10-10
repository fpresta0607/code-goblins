import { createRoot } from "react-dom/client";
import { Orchestration } from "../../src/Orchestration";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The canvas cards with the most text a card holds. Woody is the Overlord's
// screenshot: a paused goblin whose status reads "Allowance: resumes Oct 14,
// 4:05 AM", with no browser line. Wilhelmina is the worst case: a goblin whose
// session reported no parent, so her card also names her relation, with a name
// that takes two lines, her browser active, and a long status. Cornelius has
// his browser active beside a status of two lines, and Bartholomew has a
// project name no line can break.
const at = "2026-10-12T12:00:00Z";
const paused = (reason: string, until: string) => ({
  phase: "paused",
  lifecycle: { phase: "paused", action: "pause", at, kept: [], stopped: [], problems: [], handoff_saved: false, validation_restarts: false, pause: { reason, until, at } },
});
const goblin = (fields: Record<string, unknown>) => ({ project: "code-goblins", harness: "claude", verified: false, since: at, at, ...fields });
const snapshot = parseSnapshot({ healthy: true, instance: "fixture", at, tasks: [
  goblin({ id: "cg-tour", title: "Show a visitor around the board", goblin_name: "Woody", goblin_title: "Tour Guide", generation: "g-tour", ...paused("allowance", "2026-10-14T11:05:00Z") }),
  goblin({ id: "cg-plumbing", title: "Fix the reliability of the nightly sync", goblin_name: "Wilhelmina", goblin_title: "Senior Reliability Plumber", generation: "g-plumbing", session: "s-plumbing", ...paused("dependency", "task:cg-archive") }),
  goblin({ id: "cg-dispatch", title: "Dispatch the nightly builds", goblin_name: "Cornelius", goblin_title: "Senior Dispatcher", generation: "g-dispatch", ...paused("allowance", "2026-10-14T11:05:00Z") }),
  goblin({ id: "cg-archive", title: "Archive the old infrastructure notes", goblin_name: "Bartholomew", goblin_title: "Distinguished Infrastructure Archivist", project: "code-goblins-infrastructure-observability-monorepo", generation: "g-archive", phase: "working" }),
], sessions: [
  { id: "s-plumbing", native_id: "n-plumbing", harness: "claude", role: "goblin", task_id: "cg-plumbing", generation: "g-plumbing", phase: "working" },
] });
const presentations = ["cg-plumbing", "cg-dispatch"].map((task) => ({ id: "p-" + task, kind: "browser", task_id: task, generation: "g-" + task.slice(3), source: "", target: "", state: "active", url: "", at: "", until: "" }));

createRoot(document.getElementById("root")!).render(<main style={{ height: "100vh", display: "flex", flexDirection: "column" }}>
  <Orchestration snapshot={snapshot} selected="" connected effects={[]} presentations={presentations} now={Date.parse(at)} onSelect={() => {}} onChild={() => {}} />
</main>);
