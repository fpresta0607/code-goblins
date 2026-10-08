// A fleet whose branches run every way a branch can: one goblin with five
// running baby goblins in two rows, one of them idle and one silent, and a
// goblin of its own under them, one with a single baby goblin, and one with
// two sub-agents and a helper goblin. Its snapshot is what the supervisor
// serves, before the board parses it.
export function treeFleet(now: number) {
  const ago = (minutes: number) => new Date(now - minutes * 60_000).toISOString();
  const child = (fields: Record<string, unknown>) => ({ parent: "", detail: "", task: "", last_line: "", memory: 0, finished: "", started: ago(10), last_activity: ago(1), source_updated_at: ago(1), fetched_at: ago(0), ...fields });
  const tree = (task: string, children: Record<string, unknown>[]) => ({ task_id: task, generation: "g1", harness: "claude", memory: 2 ** 30, own_memory: 2 ** 29, children, source_updated_at: ago(0), fetched_at: ago(0.1) });
  const task = (id: string, title: string, fields: Record<string, unknown> = {}) => ({ id, title, project: "code-goblins", phase: "working", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", verified: false, generation: "g1", session: "s-" + id, backend: "native", since: ago(90), ...fields });
  const session = (id: string, role: string, parent: string, taskID = "") => ({ id, native_id: id, harness: "claude", role, task_id: taskID, generation: taskID ? "g1" : "", parent, relation: parent ? "Dispatched by the CFO" : "", phase: "working", updated_at: ago(0) });
  return {
    healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [],
    sessions: [
      session("s-cfo", "cfo", ""),
      session("s-fleet", "goblin", "s-cfo", "fleet"),
      session("s-billing", "goblin", "s-cfo", "billing"),
      session("s-canvas", "goblin", "s-cfo", "canvas"),
      session("s-notes", "goblin", "s-fleet", "notes"),
      session("s-helper-one", "goblin", "s-canvas", "helper-one"),
    ],
    tasks: [
      task("fleet", "Build the fleet tree", { goblin_name: "Grub", goblin_title: "Tree Surgeon", tree: tree("fleet", [
        child({ id: "subagent:toolu_A", kind: "subagent", label: "Map harness plumbing", detail: "Explore", task: "Find where the harness starts sub-agents and what each one records", state: "working", started: ago(50) }),
        child({ id: "subagent:toolu_B", kind: "subagent", label: "Research MCP OAuth", detail: "general-purpose", task: "Read how MCP servers sign in", state: "working", started: ago(45) }),
        child({ id: "shell:b1", kind: "shell", group: "test", label: "Run the affected Go tests", detail: "background bash", task: "go test ./internal/monitor/...", state: "working", started: ago(40) }),
        child({ id: "process:5120:1", kind: "process", group: "dev-server", label: "Dev server :5173", detail: "node, 2 processes", task: "npm run dev", state: "waiting", started: ago(38), last_activity: ago(38), memory: 412 * 2 ** 20 }),
        child({ id: "process:6100:2", kind: "process", group: "other", label: 'C:\\WINDOWS\\System32\\cmd.exe /d /s /c "python -I survey.py"', detail: "python, 1 process", task: "python -I survey.py", state: "silent", started: ago(36), last_activity: ago(34), memory: 1228 * 2 ** 20 }),
        child({ id: "subagent:toolu_C", kind: "subagent", label: "Find the flaky assertion", detail: "Explore", state: "done", started: ago(30), finished: ago(3) }),
      ]) }),
      task("billing", "Stream the billing CSV export", { goblin_name: "Kip", goblin_title: "Echo Chaser", tree: tree("billing", [
        child({ id: "subagent:toolu_D", kind: "subagent", label: "Trace the export", detail: "Explore", task: "Trace the CSV export from the handler to the writer", state: "working" }),
      ]) }),
      task("canvas", "Polish the canvas", { goblin_name: "Moss", goblin_title: "Pixel Wrangler", tree: tree("canvas", [
        child({ id: "subagent:toolu_E", kind: "subagent", label: "Measure the branches", detail: "Explore", task: "Measure where each branch line ends", state: "working", started: ago(20) }),
        child({ id: "subagent:toolu_F", kind: "subagent", label: "Check the phone list", detail: "general-purpose", task: "Check the lineage list at phone width", state: "working", started: ago(15) }),
        child({ id: "helper:helper-one", kind: "helper", label: "Pip - Rail Fitter", detail: "helper goblin", task: "Fit the phone rails", state: "working" }),
      ]) }),
      task("notes", "Draft the release notes"),
      task("helper-one", "Fit the phone rails", { parent: "canvas", goblin_name: "Pip", goblin_title: "Rail Fitter" }),
    ],
  };
}
