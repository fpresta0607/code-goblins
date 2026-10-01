import type { Session, Snapshot, Task } from "./types.ts";
import { lineageRoots, ownsTaskSession, sessionTitle, tasksWithoutSession } from "./lineageTree.ts";

export type Persona = "cfo" | "builder" | "reviewer" | "tester" | "planner" | "finisher" | "general"
  | "debugger" | "security" | "database" | "designer" | "documentation" | "operations"
  | "researcher" | "performance" | "integrations" | "git" | "accessibility" | "releases";
export type Point = { x: number; y: number };
export const NODE_WIDTH = 292;
export const NODE_HEIGHT = 132;

// The orchestration graph grows to fill the visible canvas and centers there,
// capped at 1.25x so cards never get huge and never below 0.35x.
export function fitScale(graph: { width: number; height: number }, canvas: { width: number; height: number }): number {
  if (graph.width <= 0 || graph.height <= 0 || canvas.width <= 0 || canvas.height <= 0) return 1;
  return Math.max(.35, Math.min(1.25, (canvas.width - 48) / graph.width, (canvas.height - 48) / graph.height));
}

export function taskColumn(task: Task): "Tasks" | "In progress" | "Paused" | "Completed" {
  if (task.archived || task.phase === "stopped" || task.phase === "stopping") return "Completed";
  if (["paused", "pausing", "resuming"].includes(task.phase)) return "Paused";
  if (task.phase === "queued") return "Tasks";
  return task.phase === "done" && task.verified ? "Completed" : "In progress";
}

// Every queued task, in the order the supervisor ranks them: the Tasks column
// and the CFO's Task tab list the same queue.
export function queuedTasks(snapshot: Snapshot): Task[] {
  return snapshot.tasks.filter((task) => taskColumn(task) === "Tasks");
}

export function personaFor(task?: Task, node?: Session): Persona {
  if (node?.role === "cfo") return "cfo";
  if (task?.archived || task && ownsTaskSession(node, task) && taskColumn(task) === "Completed") return "finisher";
  const meaning = (node?.agent_type || task?.title || "").toLowerCase();
  const specialists: [RegExp, Persona][] = [
    [/\b(debug|debugger|crash|bug|regression)\b/, "debugger"],
    [/\b(security|vulnerability|threat|hardening)\b/, "security"],
    [/\b(database|postgres|sql|schema|migration)\b/, "database"],
    [/\b(designer|styling|visual|typography|brand)\b/, "designer"],
    [/\b(documentation|docs|readme|guide)\b/, "documentation"],
    [/\b(deploy|deployment|operations|infra|infrastructure)\b/, "operations"],
    [/\b(research|researcher|investigate|explore)\b/, "researcher"],
    [/\b(performance|latency|benchmark|optimize)\b/, "performance"],
    [/\b(integration|integrations|webhook|connector)\b/, "integrations"],
    [/\b(git|rebase|merge|conflict|version control)\b/, "git"],
    [/\b(accessibility|a11y|screen reader)\b/, "accessibility"],
    [/\b(release|releases|versioning|publish)\b/, "releases"],
  ];
  const specialist = specialists.find(([pattern]) => pattern.test(meaning));
  if (specialist) return specialist[1];
  if (/^(build|implement|fix|repair|develop)\b/.test(meaning)) return "builder";
  if (/\b(test|tester|testing|qa|keyboard|validate|validation)\b/.test(meaning)) return "tester";
  if (/\b(review|reviewer|audit|diff)\b/.test(meaning)) return "reviewer";
  if (/\b(plan|planner|planning|design|research)\b/.test(meaning)) return "planner";
  if (/\b(build|builder|implement|fix|repair|develop)\b/.test(meaning)) return "builder";
  // No keyword matched: a stable choice per task keeps concurrent goblins
  // apart on the board. It says nothing about the work itself.
  if (task?.id) {
    let hash = 0;
    for (const character of task.id) hash = (hash * 31 + character.charCodeAt(0)) >>> 0;
    return stablePersonas[hash % stablePersonas.length];
  }
  return "general";
}

const stablePersonas: Persona[] = ["builder", "reviewer", "tester", "planner", "debugger", "security", "database", "designer",
  "documentation", "operations", "researcher", "performance", "integrations", "git", "accessibility", "releases"];

// pullRequestLabel names a pull request the way a person would say it.
export function pullRequestLabel(url: string): string {
  const match = /\/([^/]+)\/pull\/(\d+)/.exec(url);
  return match ? match[1] + " #" + match[2] : "Pull request";
}

// A pull request wears GitHub's icon for what it really did: merged, which a
// live task's gate proves with its merged phase and then its verified
// delivery, closed without merging, or neither yet.
export function pullRequestIcon(task: Task): "merge" | "pull-request-closed" | "pull-request" {
  return task.merged || task.phase === "merged" || (task.phase === "done" && task.verified) ? "merge" : task.closed ? "pull-request-closed" : "pull-request";
}

// Only a real GitHub pull request wears the GitHub mark and its bare number;
// any other link keeps its full label.
export function pullRequestBadge(url: string): { github: boolean; label: string } {
  const github = /^https:\/\/github\.com\/[^/]+\/[^/]+\/pull\/(\d+)$/.exec(url);
  return github ? { github: true, label: "#" + github[1] } : { github: false, label: pullRequestLabel(url) };
}

// A goblin writes the status line a pull request link comes from.
export function safePullRequest(url: string): string {
  return /^https:\/\/[^\s]+$/.test(url) ? url : "";
}

// Status words say what is happening in the Overlord's words, not which
// evidence the supervisor holds.
export function statusText(phase: string): string {
  const labels: Record<string, string> = {
    paused: "Paused", pausing: "Pausing", resuming: "Resuming", stopping: "Stopping", stopped: "Stopped",
    queued: "Not started", working: "Working", active: "Working", started: "Starting",
    review: "In review gate", waiting: "Waiting", ready: "Checks passed", done: "Delivered", merged: "Merged, verifying", idle: "Waiting for input",
    blocked: "Blocked", failed: "Failed", unavailable: "No fresh evidence",
    stale: "No fresh evidence", interrupted: "Interrupted", settled: "Turn finished", ended: "Session ended",
  };
  return labels[phase] || "No evidence yet";
}

export function nativeStatus(phase: string): string {
  const labels: Record<string, string> = {
    busy: "Working", working: "Working", active: "Working", started: "Starting",
    idle: "Waiting for input", done: "Turn finished", settled: "Turn finished",
    ended: "Session ended", interrupted: "Interrupted", stale: "No fresh evidence",
    unavailable: "No fresh evidence",
  };
  return labels[phase] || "No evidence yet";
}

// asksOverlord is whether a goblin's question is on the board waiting for the
// Overlord's answer; the CFO's own questions name no task.
export function asksOverlord(snapshot: Snapshot, taskID: string): boolean {
  if (!taskID) return false;
  const task = snapshot.tasks.find((candidate) => candidate.id === taskID);
  return task?.phase === "waiting" && task.waiting_on === "overlord"
    || (snapshot.questions || []).some((question) => question.task === taskID && question.status === "pending");
}

// waitingTarget is the goblin a waiting task waits on, when it is one the
// board shows; a wait on the Overlord, CI or a deploy names no goblin.
export function waitingTarget(snapshot: Snapshot, task: Task): Task | undefined {
  if (task.phase !== "waiting" || Object.hasOwn(WAITS, task.waiting_on)) return undefined;
  return snapshot.tasks.find((candidate) => candidate.id === task.waiting_on && candidate.id !== task.id);
}

// A goblin waiting on the Overlord waits through the CFO, which carries the
// question to him; only the pinned CFO says Waiting on you.
const WAITS: Record<string, string> = { overlord: "the CFO", ci: "CI", deploy: "deploy" };
const GATE_STEPS: Record<string, string> = { review: "code review", lint: "lint", push: "push", test: "tests", ci: "CI", pr: "PR", document: "docs" };

export function nodeStatus(node: WorkflowNode, asking = false): string {
  if (node.status) return node.status;
  if (node.task && ["paused", "pausing", "resuming", "stopping", "stopped"].includes(node.task.phase)) return statusText(node.task.phase);
  if (node.task?.archived) return node.task.merged ? "Merged" : node.task.closed ? "Closed" : "Finished";
  if (node.task && ownsTaskSession(node.session, node.task)) {
    const { phase, reason, verified } = node.task;
    if (asking) return "Waiting on the CFO";
    if ((phase === "blocked" || phase === "failed") && reason.startsWith("Waiting on the CFO")) return "Waiting on the CFO";
    if (phase === "waiting" && node.task.waiting_on) return "Waiting on " + (WAITS[node.task.waiting_on] || node.task.waiting_on);
    if (phase === "review" && Object.hasOwn(GATE_STEPS, node.task.gate_step)) return "In review gate: " + GATE_STEPS[node.task.gate_step];
    return phase === "done" && !verified ? "Done, verifying" : statusText(phase);
  }
  return nativeStatus(node.session?.runtime?.state || node.session?.phase || "");
}

export interface WorkflowNode {
  id: string;
  title: string;
  task?: Task;
  session?: Session;
  parent?: string;
  relation: string;
  // cfo marks the supervisor root drawn when no native CFO session reported,
  // and status is its fixed label.
  cfo?: boolean;
  status?: string;
}

export const CFO_ROOT = "cfo:primary";

export function workflowNodes(snapshot: Snapshot): WorkflowNode[] {
  const ids = new Set(snapshot.sessions.map((node) => node.id));
  const nodes: WorkflowNode[] = [
    ...snapshot.sessions.map((session) => {
      const task = snapshot.tasks.find((task) => task.id === session.task_id);
      return {
        id: "session:" + session.id, title: sessionTitle(session, task), task, session,
        parent: session.parent && ids.has(session.parent) ? "session:" + session.parent : undefined,
        relation: session.parent && ids.has(session.parent) ? session.relation || "Reported child"
          : session.role === "cfo" ? "Supervisor"
            : snapshot.retired.includes(session.parent) ? "Parent retired" : "Parent unreported",
      };
    }),
    ...tasksWithoutSession(snapshot.tasks.filter((task) => !task.archived && task.phase !== "queued"), snapshot.sessions).map((task) => ({
      id: "task:" + task.id, title: task.title || task.id, task, relation: "Session unreported",
    })),
  ];
  // Every live task record was dispatched by the CFO through cfo spawn, so a
  // task no native hook reported hangs under the CFO: the reported session
  // when there is one, otherwise the supervisor root drawn for it. Sessions
  // keep only the parents they reported.
  const dispatched = nodes.filter((node) => node.id.startsWith("task:"));
  if (dispatched.length) {
    let cfo = nodes.find((node) => node.session?.role === "cfo");
    if (!cfo) {
      cfo = { id: CFO_ROOT, title: "CFO", relation: "Supervisor", cfo: true, status: snapshot.registration ? "Registration stale" : "Supervising" };
      nodes.unshift(cfo);
    }
    for (const node of dispatched) { node.parent = cfo.id; node.relation = "Dispatched by the CFO"; }
  }
  const byID = new Map(nodes.map((node) => [node.id, node]));
  const cyclic = new Set<string>();
  for (const node of nodes) {
    const seen = new Set([node.id]);
    let parent = node.parent;
    while (parent && !seen.has(parent)) { seen.add(parent); parent = byID.get(parent)?.parent; }
    if (parent) cyclic.add(node.id);
  }
  return nodes.map((node) => cyclic.has(node.id) ? { ...node, parent: undefined, relation: "Cyclic parent link" } : node);
}

// waitingOn maps the card of each goblin waiting on another goblin to the card
// of the goblin it waits on, as the dashed line between them shows.
export function waitingOn(snapshot: Snapshot, nodes: WorkflowNode[]): Record<string, string> {
  const edges: Record<string, string> = {};
  for (const node of nodes) {
    const awaited = node.task && ownsTaskSession(node.session, node.task) ? waitingTarget(snapshot, node.task) : undefined;
    const target = awaited && nodes.find((other) => other.task?.id === awaited.id && ownsTaskSession(other.session, other.task));
    if (target) edges[node.id] = target.id;
  }
  return edges;
}

// The canvas grid: a card and its gap across, a row down.
const COLUMN = NODE_WIDTH + 44, ROW = 244;

// Two cards clash when they are nearer than a card and its gap both across
// and down.
const clashes = (one: Point, other: Point) => Math.abs(one.x - other.x) < COLUMN && Math.abs(one.y - other.y) < NODE_HEIGHT + 44;

// nearestFree is the place nearest to where a card wants to be that no other
// card covers: a column either side along its row, then the rows below it,
// and else past every card in its row.
function nearestFree(want: Point, taken: Point[]): Point {
  for (let row = 0; row < 4; row++) for (const step of [0, -1, 1, -2, 2, -3, 3]) {
    const place = { x: want.x + step * COLUMN, y: want.y + row * ROW };
    if (place.x >= 0 && !taken.some((other) => clashes(place, other))) return place;
  }
  return { x: Math.max(want.x, ...taken.filter((other) => Math.abs(other.y - want.y) < NODE_HEIGHT + 44).map((other) => other.x + COLUMN)), y: want.y };
}

// Positioning changes presentation only. Cycles retain a visible node but do
// not become recursively laid-out family relationships. A goblin waiting on
// another sits in the row under it, half a card over, so the dashed line
// between them is short and its own connector drops through a gap; a sibling
// with nothing of its own under it gives up that place and takes the nearest
// free one. Only a card with no children of its own moves, and only under a
// card that stays in its family's row, so a chain or a cycle of waits keeps
// its places.
export function arrange(nodes: WorkflowNode[], waits: Record<string, string> = {}): Record<string, Point> {
  const positions: Record<string, Point> = {};
  const visited = new Set<string>();
  const ids = new Set(nodes.map((node) => node.id));
  const parents = new Set(nodes.flatMap((node) => node.parent ? [node.parent] : []));
  const waiting = new Map(Object.entries(waits).filter(([id, target]) => id !== target && ids.has(id) && ids.has(target) && !parents.has(id)));
  const below = [...waiting].filter(([, target]) => !waiting.has(target));
  const fixed = new Set(below.flat());
  for (const [id] of below) visited.add(id);
  let leaf = 0;
  const place = (node: WorkflowNode, depth: number): number => {
    visited.add(node.id);
    const children = nodes.filter((child) => child.parent === node.id && !visited.has(child.id));
    // Two rows, the second offset by half a card so each of its cards sits
    // under a gap in the first: its connector drops through that gap instead
    // of behind a sibling, which would read as the wrong parent.
    if (children.length > 3 && children.every((child) => !nodes.some((other) => other.parent === child.id))) {
      const columns = Math.ceil(children.length / 2), first = leaf;
      children.forEach((child, i) => {
        const row = Math.floor(i / columns);
        visited.add(child.id);
        positions[child.id] = { x: 40 + (first + i % columns + row / 2) * COLUMN, y: 72 + (depth + 1 + row) * ROW };
      });
      leaf += columns + 1;
      const x = 40 + (first + (columns - 1) / 2 + .25) * COLUMN;
      positions[node.id] = { x, y: 72 + depth * ROW };
      return x;
    }
    const xs = children.filter((child) => !visited.has(child.id)).map((child) => place(child, depth + 1));
    const x = xs.length ? (xs[0] + xs[xs.length - 1]) / 2 : 40 + leaf++ * COLUMN;
    positions[node.id] = { x, y: 72 + depth * ROW };
    return x;
  };
  const sessions = nodes.flatMap((node) => node.session ? [node.session] : []);
  const rootIDs = new Set(lineageRoots(sessions).map((session) => "session:" + session.id));
  for (const node of nodes) if (!visited.has(node.id) && (!node.parent || rootIDs.has(node.id))) place(node, 0);
  for (const node of nodes) if (!visited.has(node.id)) place(node, 0);
  for (const [id, target] of below) {
    const awaited = positions[target];
    const under = [awaited.x + COLUMN / 2, awaited.x - COLUMN / 2].filter((x) => x >= 0).map((x) => ({ x, y: awaited.y + ROW }));
    const covering = (place: Point) => Object.keys(positions).filter((other) => clashes(place, positions[other]));
    const free = under.find((place) => !covering(place).length);
    const blocking = covering(under[0]);
    const sibling = blocking.length === 1 && !fixed.has(blocking[0]) && !parents.has(blocking[0]) ? blocking[0] : "";
    if (free) positions[id] = free;
    else if (sibling) {
      const from = positions[sibling];
      positions[id] = under[0];
      delete positions[sibling];
      positions[sibling] = nearestFree(from, Object.values(positions));
    } else positions[id] = nearestFree(under[0], Object.values(positions));
  }
  return positions;
}

// settle is where each card shows: where the Overlord placed it by hand, else
// its arranged place, or the free place nearest to it when a card he placed
// covers that, so no card ever covers another.
export function settle(arranged: Record<string, Point>, placed: Record<string, Point>): Record<string, Point> {
  const positions: Record<string, Point> = {};
  for (const id of Object.keys(arranged)) if (placed[id]) positions[id] = placed[id];
  for (const [id, want] of Object.entries(arranged)) if (!placed[id]) positions[id] = nearestFree(want, Object.values(positions));
  return positions;
}

export function harnessName(id: string): string {
  const names: Record<string, string> = { codex: "Codex", claude: "Claude Code", pi: "Pi", kimi: "Kimi" };
  return names[id] || id;
}

// A harness mark's tip: the harness, then the model and effort it runs, when known.
export function harnessTip(harness: string, model: string, effort: string): string {
  return [harnessName(harness), model, effort].filter(Boolean).join(" · ");
}

// fleetTraffic signs what each live task last reported, its status line and
// the newest wake record it filed, and names the tasks whose signature moved
// since the previous snapshot. Those are real reports reaching the CFO.
export function fleetTraffic(previous: Map<string, string> | null, snapshot: Snapshot): { signatures: Map<string, string>; moved: string[] } {
  const newest = new Map<string, number>();
  for (const decision of snapshot.decisions) newest.set(decision.key, Math.max(newest.get(decision.key) || 0, decision.seq));
  const signatures = new Map(snapshot.tasks.filter((task) => !task.archived && task.phase !== "queued")
    .map((task) => [task.id, task.activity + "\u0000" + (newest.get(task.id) || 0)]));
  const moved = previous ? [...signatures].filter(([id, signature]) => previous.has(id) && previous.get(id) !== signature).map(([id]) => id) : [];
  return { signatures, moved };
}

// A connector pulses for PULSE_MS after its goblin reports. Each pulse is
// keyed by the time of its report, so it expires on its own schedule however
// many snapshots follow, and a newer report plays alongside an older one
// until each expires.
export const PULSE_MS = 6000;

export function reportTraffic(traffic: Record<string, number[]>, moved: string[], at: number): Record<string, number[]> {
  return { ...traffic, ...Object.fromEntries(moved.map((id) => [id, [...(traffic[id] || []), at]])) };
}

export function expireTraffic(traffic: Record<string, number[]>, moved: string[], at: number): Record<string, number[]> {
  return Object.fromEntries(Object.entries(traffic)
    .map(([id, times]): [string, number[]] => [id, moved.includes(id) ? times.filter((when) => when !== at) : times])
    .filter(([, times]) => times.length));
}
