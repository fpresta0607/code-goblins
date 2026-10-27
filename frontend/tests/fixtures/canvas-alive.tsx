import { useState } from "react";
import { createRoot } from "react-dom/client";
import { Orchestration } from "../../src/Orchestration";
import { parseSnapshot } from "../../src/types";
import { watchTips } from "../../src/tips";
import "../../src/styles.css";

// The canvas coming alive on stand-in goblins: Pip and Mabel work, with baby
// goblins under them, and Woody waits. Each step is one real change a snapshot
// can carry: a goblin starts a baby goblin, a baby goblin reports or
// finishes, or a goblin reports to the CFO. window.canvasStep plays a step,
// and ?demo plays them one after another, as a busy fleet would.
type Child = Record<string, unknown> & { id: string; state: string };
type Step = "birth" | "report" | "finish" | "goblin";
const minutes = (count: number) => new Date(Date.now() - count * 60_000).toISOString();
const megabytes = (count: number) => count * 2 ** 20;
const child = (id: string, fields: Record<string, unknown>): Child => ({ id, parent: "", detail: "", group: "", task: "", last_line: "", memory: 0, finished: "", started: minutes(20), last_activity: minutes(1), source_updated_at: minutes(0), fetched_at: minutes(0), state: "working", ...fields });
// What a newly born baby goblin runs, in turn.
const NEWBORNS = [
  { kind: "subagent", label: "Trace the flaky checkout test", state: "working" },
  { kind: "subagent", label: "Review the development server", task: "Check the app server", state: "working", memory: megabytes(412) },
  { kind: "subagent", label: "Review the affected Go tests", task: "Check the tests", state: "working" },
  { kind: "subagent", label: "Review the webhook listener", task: "Check webhook delivery", state: "waiting", memory: megabytes(8) },
];
const families: Record<string, Child[]> = {
  "cg-tests": [
    child("subagent:pip-1", { kind: "subagent", label: "Map the harness plumbing", started: minutes(14) }),
    child("subagent:pip-2", { kind: "subagent", label: "Review the test run", task: "Check the test results", started: minutes(6), memory: megabytes(1228) }),
  ],
  "pd-email": [
    child("subagent:mabel-1", { kind: "subagent", label: "Review the development server", task: "Check the server", started: minutes(290), last_activity: minutes(0), memory: megabytes(257) }),
    child("subagent:mabel-2", { kind: "subagent", label: "Review the webhook listener", task: "Check webhook delivery", started: minutes(30), memory: megabytes(8) }),
  ],
};
const activity: Record<string, number> = { "cg-tests": 0, "pd-email": 0 };
let born = 0, reports = 0;
const task = (id: string, fields: Record<string, unknown>) => ({ id, project: "code-goblins", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", verified: false, since: minutes(48), at: minutes(48), generation: "g-" + id, ...fields });
const snapshot = () => parseSnapshot({ healthy: true, instance: "canvas-alive", cfo_runs: true, cfo_harness: "claude", tasks: [
  task("cg-tests", { title: "Make the flaky tests steady", goblin_name: "Pip", goblin_title: "Test Tamer", phase: "working", activity: "working: report " + activity["cg-tests"],
    tree: { task_id: "cg-tests", generation: "g-cg-tests", harness: "claude", memory: 0, own_memory: 0, children: families["cg-tests"], source_updated_at: minutes(0), fetched_at: minutes(0) } }),
  task("pd-email", { title: "Send the tenant emails", goblin_name: "Mabel", goblin_title: "Email Elf", project: "PrecisionDocs-AI", phase: "working", activity: "working: report " + activity["pd-email"],
    tree: { task_id: "pd-email", generation: "g-pd-email", harness: "claude", memory: 0, own_memory: 0, children: families["pd-email"], source_updated_at: minutes(0), fetched_at: minutes(0) } }),
  task("cg-tour", { title: "Show a visitor around the board", goblin_name: "Woody", goblin_title: "Tour Guide", phase: "waiting", waiting_on: "ci", activity: "waiting" }),
] });

// step changes one goblin's family as a snapshot would show it.
function step(name: Step, goblin: string) {
  const family = families[goblin], live = family.filter((one) => one.state !== "done");
  if (name === "birth") family.push(child("baby:" + goblin + ":" + ++born, { ...NEWBORNS[born % NEWBORNS.length], started: minutes(0), last_activity: minutes(0) }));
  if (name === "report" && live.length) live[reports++ % live.length].last_activity = new Date().toISOString();
  if (name === "finish" && live.length) Object.assign(live[0], { state: "done", finished: new Date().toISOString() });
  if (name === "goblin") activity[goblin]++;
}

// What ?demo plays, a step every 1.4 s: each goblin's babies come and go, so
// none ever has more than four or fewer than one.
const SCRIPT: [Step, string][] = [
  ["report", "cg-tests"], ["birth", "pd-email"], ["goblin", "cg-tests"], ["report", "pd-email"], ["birth", "cg-tests"],
  ["finish", "pd-email"], ["goblin", "pd-email"], ["report", "cg-tests"], ["finish", "cg-tests"], ["report", "pd-email"],
];

function Fixture() {
  const [shown, setShown] = useState(snapshot);
  (window as Window & { canvasStep?: (name: Step, goblin: string) => void }).canvasStep = (name, goblin) => { step(name, goblin); setShown(snapshot()); };
  return <main style={{ height: "100vh", display: "flex", flexDirection: "column" }}>
    <Orchestration snapshot={shown} selected="" connected effects={[]} presentations={[]} now={Date.now()} freshNow={Date.now()} onSelect={() => {}} onChild={() => {}} onOpenCfo={() => {}} />
  </main>;
}
watchTips();
createRoot(document.getElementById("root")!).render(<Fixture />);
if (new URLSearchParams(location.search).has("demo")) {
  let at = 0;
  setInterval(() => {
    const [name, goblin] = SCRIPT[at++ % SCRIPT.length];
    (window as Window & { canvasStep?: (name: Step, goblin: string) => void }).canvasStep?.(name, goblin);
  }, 1400);
}
