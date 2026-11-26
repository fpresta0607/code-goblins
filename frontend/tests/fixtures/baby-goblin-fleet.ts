// A fleet of one goblin at work with what a goblin runs under it: two
// sub-agents at work, one that finished, a background shell and a test run,
// and its Modern Chat, whose sub-agent calls start those sub-agents and one
// more its tree no longer holds. A round, when given, adds a sub-agent at
// work for each of its descriptions, all started together after the others.
// Every name, task and line is made up. Its snapshot is what the supervisor
// serves, before the board parses it.
export function babyGoblinFleet(now: number, round: readonly string[] = []) {
  const ago = (minutes: number) => new Date(now - minutes * 60_000).toISOString();
  const child = (fields: Record<string, unknown>) => ({ parent: "", detail: "", task: "", last_line: "", memory: 0, finished: "", started: ago(10), last_activity: ago(0.2), source_updated_at: ago(0.2), fetched_at: ago(0), ...fields });
  const session = (id: string, role: string, parent: string, task = "") => ({ id, native_id: id, harness: "claude", role, task_id: task, generation: task ? "g1" : "", parent, relation: parent ? "Dispatched by the CEO" : "", phase: "working", updated_at: ago(0) });
  return {
    healthy: true, example: true, instance: "fixture", ceo_runs: true, ceo_harness: "claude", revision: 1, attention: [], afk: { state: "off" }, actions: [],
    sessions: [session("s-ceo", "ceo", ""), session("s-grub", "goblin", "s-ceo", "grub"), session("s-kip", "goblin", "s-ceo", "kip")],
    tasks: [
      { id: "grub", title: "Draw the board's family tree", project: "example-app", phase: "working", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", verified: false,
        generation: "g1", session: "s-grub", backend: "native", since: ago(90), goblin_name: "Grub", goblin_title: "Tree Surgeon",
        tree: { task_id: "grub", generation: "g1", harness: "claude", memory: 2 ** 30, own_memory: 2 ** 29, source_updated_at: ago(0), fetched_at: ago(0.1), children: [
          child({ id: "subagent:toolu_A", kind: "subagent", label: "Map harness plumbing", detail: "Explore", model: "claude-sonnet-5-5", task: "Find where the harness starts sub-agents and what each one records", state: "working", started: ago(12) }),
          child({ id: "subagent:toolu_B", kind: "subagent", label: "Check the lineage list at phone width", detail: "general-purpose", model: "claude-sonnet-5-5", task: "Check how the lineage list draws at phone width", state: "working", started: ago(8) }),
          child({ id: "subagent:toolu_C", kind: "subagent", label: "Find the flaky assertion", detail: "Explore", task: "Find the flaky assertion", state: "done", started: ago(30), finished: ago(20) }),
          child({ id: "shell:b1", kind: "shell", label: "Start the board", task: "npm run dev", state: "working", started: ago(40), memory: 300 * 2 ** 20 }),
          child({ id: "process:7100:1", kind: "process", group: "test", label: "npx playwright test", detail: "node, 3 processes", task: "npx playwright test", state: "working", started: ago(3), memory: 900 * 2 ** 20 }),
          ...round.map((label, index) => child({ id: "subagent:toolu_R" + index, kind: "subagent", label, detail: "general-purpose", model: "claude-sonnet-5-5", task: label, state: "working", started: ago(2) })),
        ] } },
      { id: "kip", title: "Stream the CSV export", project: "example-app", phase: "working", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", verified: false,
        generation: "g1", session: "s-kip", backend: "native", since: ago(40), goblin_name: "Kip", goblin_title: "Echo Chaser" },
    ],
  };
}

// The sub-agent's own transcript, as the supervisor reads it.
export const TRANSCRIPT = [
  { kind: "prompt", text: "Find where the harness starts sub-agents and what each one records" },
  { kind: "call", tool: "Search", subject: "subagents/agent-", state: "done", result: ["Found 3 files"] },
  { kind: "reply", text: "Reading internal/tree/claude.go" },
];

const at = "2026-11-26T03:00:00Z";
// The goblin's conversation: a sub-agent started in the background, whose
// call came back at once, one still running in the foreground, and one whose
// sub-agent its goblin's tree no longer holds, then a round's sub-agents,
// when given, each started in the background.
export function talkOf(round: readonly string[] = []) {
  return {
    conversation: "fixture-conversation", more: false,
    messages: [
      { id: "m0", seq: 0, at, kind: "prompt", source: "ceo", text: "Draw the board's family tree." },
      { id: "m1", seq: 1, at, kind: "reply", blocks: [
        { kind: "text", text: "Two sub-agents look into it while I draw." },
        { kind: "call", tool: "Agent", subject: "Map harness plumbing", state: "done", result: "Async agent launched successfully.", agent: { node: "subagent:toolu_A", type: "Explore" } },
        { kind: "call", tool: "Agent", subject: "Check the lineage list at phone width", state: "running", agent: { node: "subagent:toolu_B", type: "general-purpose" } },
        { kind: "call", tool: "Agent", subject: "Survey the old canvas", state: "done", result: "The old canvas draws every child.", agent: { node: "subagent:toolu_Z", type: "Explore" } },
        ...round.map((label, index) => ({ kind: "call", tool: "Agent", subject: label, state: "done", result: "Async agent launched successfully.", agent: { node: "subagent:toolu_R" + index, type: "general-purpose" } })),
      ] },
    ],
  };
}
