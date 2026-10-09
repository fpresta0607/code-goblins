import type { MergeTrain, Session, Snapshot, Task } from "./types.ts";
import { lineageRoots, ownsTaskSession, sessionTitle, tasksWithoutSession } from "./lineageTree.ts";
import { goblinName, waitStatus } from "./task-words.ts";
import { isHeldByTree, isHelperHeld, LINE_GAP } from "./fleet-tree.ts";
import { awaitedTest } from "./pull-request-test.ts";

export type Persona = "cfo" | "builder" | "reviewer" | "tester" | "planner" | "finisher" | "general"
  | "debugger" | "security" | "database" | "designer" | "documentation" | "operations"
  | "researcher" | "performance" | "integrations" | "git" | "accessibility" | "releases";
export type Point = { x: number; y: number };
export const NODE_WIDTH = 292;
export const NODE_HEIGHT = 132;

// The canvas zooms between a tenth and one and a half times, so the whole of
// any tree fits it.
export const MIN_ZOOM = .1, MAX_ZOOM = 1.5;

// The orchestration graph grows to fill the visible canvas and centers there,
// capped at 1.25x so cards never get huge, and shrinks as far as the whole
// tree needs.
export function fitScale(graph: { width: number; height: number }, canvas: { width: number; height: number }): number {
  if (graph.width <= 0 || graph.height <= 0 || canvas.width <= 0 || canvas.height <= 0) return 1;
  return Math.max(MIN_ZOOM, Math.min(1.25, (canvas.width - 48) / graph.width, (canvas.height - 48) / graph.height));
}

// A view of the canvas: how far it is zoomed, and where the graph's origin
// sits in the canvas.
export interface View { scale: number; x: number; y: number }

// zoomAt zooms a view to scale, within the zoom range, keeping the point of
// the graph under the pointer where it is, as a map does.
export function zoomAt(view: View, scale: number, pointer: Point): View {
  const next = Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, scale));
  return { scale: next, x: pointer.x - (pointer.x - view.x) * next / view.scale, y: pointer.y - (pointer.y - view.y) * next / view.scale };
}

// Paused holds only goblins whose terminals were stopped to free memory: a
// memory pause, the floor's or the CFO's, and the Overlord's own Pause, as
// does a pause from before conditions were kept, which needs his Resume too
// (the Overlord, 2026-10-08: "paused is completely stopped terminals for
// memory reasons"). A goblin paused to wait on something, its own pull
// request or merge train, another task, a question, an allowance reset, CI
// or a deploy, is still at its work and stays in In progress. A queued task
// the supervisor is starting leaves Tasks at once, while one whose Start
// waits its turn stays there, so the card the Overlord clicked does not move
// until the supervisor starts it.
const PAUSED_FOR_MEMORY = new Set(["memory", "overlord", ""]);

export function taskColumn(task: Task): "Tasks" | "In progress" | "Paused" | "Completed" {
  if (task.archived || task.phase === "stopped" || task.phase === "stopping") return "Completed";
  if (["paused", "pausing", "resuming"].includes(task.phase) && PAUSED_FOR_MEMORY.has(task.lifecycle?.pause?.reason ?? "")) return "Paused";
  if (task.phase === "queued") return task.starting && !task.asked ? "In progress" : "Tasks";
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
  return task?.id ? stablePersona(task.id) : "general";
}

// stablePersona is the persona a task id always gets, for a goblin whose work
// names no specialist or whose task the board no longer holds.
export function stablePersona(id: string): Persona {
  let hash = 0;
  for (const character of id) hash = (hash * 31 + character.charCodeAt(0)) >>> 0;
  return stablePersonas[hash % stablePersonas.length];
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
    queued: "Queued", working: "Working", active: "Working", started: "Starting",
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

// asksOverlord is whether a goblin explicitly waits on the Overlord.
// A goblin's question is the CFO's to answer.
export function asksOverlord(snapshot: Snapshot, taskID: string): boolean {
  if (!taskID) return false;
  const task = snapshot.tasks.find((candidate) => candidate.id === taskID);
  return task?.phase === "waiting" && task.waiting_on === "overlord";
}

// waitingTarget is the goblin a waiting task waits on, when it is one the
// board shows; a wait on the Overlord, CI, a deploy or memory names no goblin.
export function waitingTarget(snapshot: Snapshot, task: Task): Task | undefined {
  if (task.phase !== "waiting" || Object.hasOwn(WAITS, task.waiting_on)) return undefined;
  return snapshot.tasks.find((candidate) => candidate.id === task.waiting_on && candidate.id !== task.id);
}

// A goblin waiting on the Overlord waits through the CFO, which carries the
// question to him; only the pinned CFO says Waiting on you.
const WAITS: Record<string, string> = { overlord: "the CFO", ci: "CI", deploy: "deploy", memory: "memory" };
const GATE_STEPS: Record<string, string> = { review: "code review", lint: "lint", push: "push", test: "tests", ci: "CI", pr: "PR", document: "docs" };

// A resume or stop that did not finish is the task's state until the next
// one starts, whatever its goblin last reported, drawn as that action under
// way: the CFO or the Overlord asked for a stop, so one that did not finish is
// never the goblin failing (the Overlord, 2026-10-07), and the CFO hears of
// it. Only a goblin that did not start again has failed. A pause that did not
// finish has no state of its own: the card says what the goblin does, and one
// whose session ended the supervisor serves as paused (the Overlord,
// 2026-10-08, of "Pause did not finish" in yellow on Shirley's card).
const FAILED_ACTIONS: Record<string, { status: string; phase: string }> = {
  resume: { status: "Resume failed", phase: "failed" },
  stop: { status: "Stop did not finish", phase: "stopping" },
};
const actionFailed = (task: Task) => task.lifecycle?.phase === "failed" && !["pausing", "resuming", "stopping"].includes(task.phase) ? FAILED_ACTIONS[task.lifecycle.action] : undefined;

// isStartFailed says a queued task's last start failed and no new one is
// under way: it waits for Start, and the CFO was told why.
const isStartFailed = (task: Task) => task.phase === "queued" && !!task.start_error && !task.starting;

// statusPhase is the phase a task's status is drawn in: the action's for one
// that did not finish, started for one whose start is under way, its pull
// request's test's tone for one that waits on it, and failed for a goblin
// that did not come back or a start that failed. trains are the board's
// merge trains.
export function statusPhase(task: Task, trains: MergeTrain[] = []): string {
  const awaited = awaitedTest(task, trains);
  return actionFailed(task)?.phase || (task.starting ? "started" : awaited ? "pr-" + awaited.tone : task.comeback?.state === "stopped" || isStartFailed(task) ? "failed" : task.phase);
}

// A wait on another goblin names it by its goblin name while tasks, the
// board's, hold it. A goblin that waits only on its pull request's test reads
// that test, on a merge train among trains, the board's, or in its own CI.
export function nodeStatus(node: WorkflowNode, asking = false, tasks: Task[] = [], trains: MergeTrain[] = []): string {
  if (node.status) return node.status;
  const unfinished = node.task && actionFailed(node.task);
  if (unfinished) return unfinished.status;
  if (node.task?.starting) return "Starting";
  if (node.task && isStartFailed(node.task)) return "Start failed";
  // A queued task has no session to stop: Stop removes it from the queue.
  if (node.task?.phase === "stopping" && !node.task.generation) return "Removing";
  const awaited = node.task && ownsTaskSession(node.session, node.task) ? awaitedTest(node.task, trains) : undefined;
  if (awaited) return awaited.text;
  if (node.task && ["paused", "pausing", "resuming", "stopping", "stopped"].includes(node.task.phase)) return statusText(node.task.phase);
  if (node.task?.comeback?.state === "waiting") return "Comes back after the restart when memory allows";
  if (node.task?.comeback?.state === "stopped") return "Did not come back after the restart";
  if (node.task?.archived) return node.task.merged ? "Merged" : node.task.closed ? "Closed" : "Finished";
  if (node.task?.phase === "queued" && node.task.finished) return "Already finished";
  if (node.task?.phase === "queued" && node.task.waits.length) return waitStatus(node.task, tasks);
  if (node.task && ownsTaskSession(node.session, node.task)) {
    const { phase, reason, verified } = node.task;
    if (asking) return "Waiting on the CFO";
    if ((phase === "blocked" || phase === "failed") && reason.startsWith("Waiting on the CFO")) return "Waiting on the CFO";
    if (phase === "waiting" && node.task.waiting_on) {
      const awaited = tasks.find((candidate) => candidate.id === node.task?.waiting_on);
      return "Waiting on " + (WAITS[node.task.waiting_on] || (awaited ? goblinName(awaited) : node.task.waiting_on));
    }
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
  const sessions = snapshot.sessions.filter((session) => !isHeldByTree(snapshot, session));
  const ids = new Set(sessions.map((node) => node.id));
  const nodes: WorkflowNode[] = [
    ...sessions.map((session) => {
      const task = snapshot.tasks.find((task) => task.id === session.task_id);
      return {
        id: "session:" + session.id, title: sessionTitle(session, task), task, session,
        parent: session.parent && ids.has(session.parent) ? "session:" + session.parent : undefined,
        relation: session.parent && ids.has(session.parent) ? session.relation || "Reported child"
          : session.role === "cfo" ? "Supervisor"
            : snapshot.retired.includes(session.parent) ? "Parent retired" : "Parent unreported",
      };
    }),
    ...tasksWithoutSession(snapshot.tasks.filter((task) => !task.archived && taskColumn(task) !== "Tasks" && !isHelperHeld(snapshot, task.id)), snapshot.sessions).map((task) => ({
      id: "task:" + task.id, title: goblinName(task), task, relation: "Session unreported",
    })),
  ];
  // A helper no family tree holds, such as one whose parent is paused, hangs
  // under its parent's card: its record names its parent.
  const owns = (node: WorkflowNode) => node.task !== undefined && ownsTaskSession(node.session, node.task);
  const helpers = new Set<WorkflowNode>();
  for (const node of nodes) {
    const parent = owns(node) && node.task?.parent ? nodes.find((other) => other.task?.id === node.task?.parent && owns(other)) : undefined;
    if (parent) { node.parent = parent.id; node.relation = "Helper goblin"; helpers.add(node); }
  }
  // Every live task record was dispatched by the CFO through cfo spawn, so a
  // task no native hook reported hangs under the CFO: the reported session
  // when there is one, otherwise the supervisor root drawn for it. Sessions
  // keep only the parents they reported.
  const dispatched = nodes.filter((node) => node.id.startsWith("task:") && !helpers.has(node));
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
// of the goblin it waits on, as the dashed line between them shows; a helper
// hung under the goblin waiting on it is shown by their connector instead.
export function waitingOn(snapshot: Snapshot, nodes: WorkflowNode[]): Record<string, string> {
  const edges: Record<string, string> = {};
  for (const node of nodes) {
    const awaited = node.task && ownsTaskSession(node.session, node.task) ? waitingTarget(snapshot, node.task) : undefined;
    const target = awaited && nodes.find((other) => other.task?.id === awaited.id && ownsTaskSession(other.session, other.task));
    if (target && target.parent !== node.id) edges[node.id] = target.id;
  }
  return edges;
}

// The canvas grid: a card and its gap across, a row down.
const COLUMN = NODE_WIDTH + 44, ROW = 244;

// The gap kept between one card, with what shows under it, and the next.
const GAP = 44;

// What hangs under a card on the canvas: how wide it is, centred under the
// card and never narrower than it, how far under the card it reaches, and
// where, from the card's middle, each line runs down from it: its own
// middle, the side of its branches its connectors to goblins under them run
// down, and each gap between the columns of its branches.
export interface Extent { width: number; below: number; drops: number[] }
const CARD: Extent = { width: NODE_WIDTH, below: 0, drops: [0] };

// Two cards clash when what each takes, the card and what hangs under it, is
// nearer to the other's than the gap, both across and down.
const clashes = (one: Point, other: Point, oneTakes: Extent = CARD, otherTakes: Extent = CARD) =>
  Math.abs(one.x - other.x) < (oneTakes.width + otherTakes.width) / 2 + COLUMN - NODE_WIDTH
  && one.y < other.y + NODE_HEIGHT + otherTakes.below + GAP && other.y < one.y + NODE_HEIGHT + oneTakes.below + GAP;

// A card on the canvas and what hangs under it.
interface Taken { point: Point; takes: Extent }

// nearestFree is the place nearest to where a card wants to be that no other
// card covers: a column either side along its row, then the rows below it,
// and else past every card in its row.
function nearestFree(want: Point, taken: Taken[], takes: Extent = CARD): Point {
  for (let row = 0; row < 4; row++) for (const step of [0, -1, 1, -2, 2, -3, 3]) {
    const place = { x: want.x + step * COLUMN, y: want.y + row * ROW };
    if (place.x >= 0 && !taken.some((other) => clashes(place, other.point, takes, other.takes))) return place;
  }
  const inRow = taken.filter((other) => clashes({ x: other.point.x, y: want.y }, other.point, takes, other.takes));
  return { x: Math.max(want.x, ...inRow.map((other) => other.point.x + (other.takes.width + takes.width) / 2 + COLUMN - NODE_WIDTH)), y: want.y };
}

// rowsFor splits a family of goblins with no cards under them into the rows
// that show the family largest in a canvas of this shape: one row in a wide
// canvas, more in a tall one, each goblin taking as many grid columns as it
// and what hangs under it need.
function rowsFor<T>(children: T[], columns: (child: T) => number, below: (child: T) => number, canvas: { width: number; height: number }): T[][] {
  let best: { rows: T[][]; scale: number } | undefined;
  for (let count = 1; count <= children.length; count++) {
    const size = Math.ceil(children.length / count), rows: T[][] = [];
    for (let i = 0; i < children.length; i += size) rows.push(children.slice(i, i + size));
    if (best && rows.length === best.rows.length) continue;
    const width = (Math.max(...rows.map((row) => row.reduce((sum, child) => sum + columns(child), 0))) + (rows.length > 1 ? .5 : 0)) * COLUMN;
    const height = ROW + rows.reduce((sum, row) => sum + ROW + Math.max(...row.map(below)), 0);
    const scale = Math.min(canvas.width / width, canvas.height / height);
    if (!best || scale > best.scale * 1.02) best = { rows, scale };
  }
  return best ? best.rows : [children];
}

// Positioning changes presentation only. Cycles retain a visible node but do
// not become recursively laid-out family relationships. A family of goblins
// with no cards under them wraps into the rows that show it largest in the
// canvas, each row after the first offset so its connectors drop clear of
// the middles of the goblins above, and a goblin takes as many grid
// columns as what hangs under its card needs (extents). A goblin waiting on
// another sits in the row under it, half a card over, so the dashed line
// between them is short and its own connector drops through a gap; a sibling
// with nothing of its own under it gives up that place and takes the nearest
// free one. Only a card with no children of its own moves, and only under a
// card that stays in its family's row, so a chain or a cycle of waits keeps
// its places.
export function arrange(nodes: WorkflowNode[], canvas: { width: number; height: number }, waits: Record<string, string> = {}, extents: Record<string, Extent> = {}): Record<string, Point> {
  const positions: Record<string, Point> = {};
  const visited = new Set<string>();
  const ids = new Set(nodes.map((node) => node.id));
  const parents = new Set(nodes.flatMap((node) => node.parent ? [node.parent] : []));
  const waiting = new Map(Object.entries(waits).filter(([id, target]) => id !== target && ids.has(id) && ids.has(target) && !parents.has(id)));
  const below = [...waiting].filter(([, target]) => !waiting.has(target));
  const fixed = new Set(below.flat());
  for (const [id] of below) visited.add(id);
  const takes = (id: string) => extents[id] || CARD;
  const columns = (node: WorkflowNode) => Math.ceil((takes(node.id).width + COLUMN - NODE_WIDTH) / COLUMN);
  const taken = () => Object.entries(positions).map(([id, point]) => ({ point, takes: takes(id) }));
  let leaf = 0;
  const place = (node: WorkflowNode, depth: number): number => {
    visited.add(node.id);
    const children = nodes.filter((child) => child.parent === node.id && !visited.has(child.id));
    const rows = children.length > 1 && children.every((child) => !nodes.some((other) => other.parent === child.id))
      ? rowsFor(children, columns, (child) => takes(child.id).below, canvas) : [children];
    if (rows.length > 1) {
      const widest = Math.max(...rows.map((row) => row.reduce((sum, child) => sum + columns(child), 0))), first = leaf;
      // Each row after the first is offset so no connector into it drops
      // down a line that runs down from a goblin above, a middle or a
      // line beside or between its branches, where it would read as that
      // goblin's child: by half a card where it can be, as through the gaps
      // of a row of single cards, else by the eighth of a card nearest that
      // keeps clear.
      const drops: number[] = [];
      rows.forEach((row, index) => {
        const at = (offset: number) => row.map((child, i) => first + offset + row.slice(0, i).reduce((sum, other) => sum + columns(other), 0) + (columns(child) - 1) / 2);
        const covered = (offset: number) => at(offset).filter((middle) => drops.some((drop) => Math.abs(middle - drop) < .1)).length;
        const offset = index ? [.5, .25, .75, 0, .375, .625, .125, .875].reduce((best, next) => covered(next) < covered(best) ? next : best) : 0;
        at(offset).forEach((middle, i) => {
          visited.add(row[i].id);
          positions[row[i].id] = { x: 40 + middle * COLUMN, y: 72 + (depth + 1 + index) * ROW };
          drops.push(...takes(row[i].id).drops.map((drop) => middle + drop / COLUMN));
        });
      });
      leaf += widest + 1;
      const x = 40 + (first + (widest - 1) / 2 + .25) * COLUMN;
      positions[node.id] = { x, y: 72 + depth * ROW };
      return x;
    }
    const xs = children.filter((child) => !visited.has(child.id)).map((child) => place(child, depth + 1));
    let x: number;
    if (xs.length) x = (xs[0] + xs[xs.length - 1]) / 2;
    else {
      x = 40 + (leaf + (columns(node) - 1) / 2) * COLUMN;
      leaf += columns(node);
    }
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
    const covering = (place: Point) => Object.keys(positions).filter((other) => clashes(place, positions[other], takes(id), takes(other)));
    const free = under.find((place) => !covering(place).length);
    const blocking = covering(under[0]);
    const sibling = blocking.length === 1 && !fixed.has(blocking[0]) && !parents.has(blocking[0]) ? blocking[0] : "";
    if (free) positions[id] = free;
    else if (sibling) {
      const from = positions[sibling];
      positions[id] = under[0];
      delete positions[sibling];
      positions[sibling] = nearestFree(from, taken(), takes(sibling));
    } else positions[id] = nearestFree(under[0], taken(), takes(id));
  }
  return positions;
}

// makeRoom moves the rows of cards under a goblin whose children show
// beneath its card down far enough that nothing covers them: below is how far
// under each such card its children reach. Every card of a row moves alike,
// so the layout keeps its rows.
export function makeRoom(positions: Record<string, Point>, below: Record<string, number>): Record<string, Point> {
  const rows = [...new Set(Object.values(positions).map((point) => point.y))].sort((one, other) => one - other);
  const moved = new Map<number, number>();
  let shift = 0, reach = -Infinity;
  for (const y of rows) {
    shift = Math.max(shift, reach + GAP - y);
    moved.set(y, y + shift);
    for (const [id, point] of Object.entries(positions)) if (point.y === y) reach = Math.max(reach, y + shift + NODE_HEIGHT + (below[id] || 0));
  }
  return Object.fromEntries(Object.entries(positions).map(([id, point]) => [id, { x: point.x, y: moved.get(point.y)! }]));
}

// A card on the canvas with what hangs under it, as a box a connector keeps
// clear of.
export interface Box { left: number; right: number; top: number; bottom: number }

// A connector's way down past the rows between its parent and its card: down
// at x, then across at turn toward its card.
export interface Detour { x: number; turn: number }

// detours routes the connectors leaving a parent's bottom at y whose straight
// drop to its card, among targets, the middles of their tops, would pass
// behind a card or what hangs under it, among taken, as one to a later row
// of goblins does through the rows above it. Each runs down the way clear of
// them nearest its card, a line's gap from the next in it, and turns toward
// its card in the gap above that card's row, the one further from its card
// turning lower, so none crosses another. A connector with a clear drop has
// no detour.
export function detours(y: number, targets: Point[], taken: Box[]): (Detour | undefined)[] {
  const distance = (x: number, [from, to]: number[]) => Math.max(0, from - x, x - to);
  const ways = targets.map((target) => {
    const between = taken.filter((box) => box.bottom > y && box.top < target.y - GAP / 2)
      .map((box) => [box.left - LINE_GAP, box.right + LINE_GAP]).sort((one, other) => one[0] - other[0]);
    if (!between.some(([from, to]) => target.x > from && target.x < to)) return undefined;
    const clear: number[][] = [];
    let from = -Infinity;
    for (const [left, right] of between) {
      if (left > from) clear.push([from, left]);
      from = Math.max(from, right);
    }
    clear.push([from, Infinity]);
    return clear.reduce((best, way) => distance(target.x, way) < distance(target.x, best) ? way : best);
  });
  // The ways round the left of everything are one, hugging the nearest of
  // them, and so are those round the right. In a way the connectors turning
  // left run nearest its left and those turning right nearest its right,
  // each the outer the sooner it turns off, so one turning off crosses none
  // still running down.
  const side = (way: number[]) => way[0] === -Infinity ? "left" : way[1] === Infinity ? "right" : String(way);
  const xs: number[] = [];
  for (const key of new Set(ways.flatMap((way) => way ? [side(way)] : []))) {
    const using = ways.flatMap((way, i) => way && side(way) === key ? [{ i, way }] : []);
    const from = Math.max(...using.map(({ way }) => way[0])), to = Math.min(...using.map(({ way }) => way[1]));
    const middle = from === -Infinity ? to : to === Infinity ? from : (from + to) / 2;
    const order = [...using.filter(({ i }) => targets[i].x < middle).sort((one, other) => targets[one.i].y - targets[other.i].y || targets[one.i].x - targets[other.i].x),
      ...using.filter(({ i }) => targets[i].x >= middle).sort((one, other) => targets[other.i].y - targets[one.i].y || targets[one.i].x - targets[other.i].x)];
    const first = from === -Infinity ? to - (order.length - 1) * LINE_GAP : to === Infinity ? from : middle - (order.length - 1) * LINE_GAP / 2;
    order.forEach(({ i }, j) => { xs[i] = first + j * LINE_GAP; });
  }
  const turns: number[] = [];
  for (const row of new Set(targets.map((target) => target.y))) for (const way of [1, -1]) {
    const turning = targets.map((_, i) => i).filter((i) => ways[i] && targets[i].y === row && (targets[i].x > xs[i] ? 1 : -1) === way)
      .sort((one, other) => (xs[one] - xs[other]) * way);
    turning.forEach((i, j) => { turns[i] = row - GAP / 2 + ((turning.length - 1) / 2 - j) * LINE_GAP; });
  }
  return ways.map((way, i) => way && { x: xs[i], turn: turns[i] });
}

// settle is where each card shows: where the Overlord placed it by hand, else
// its arranged place, or the free place nearest to it when a card he placed
// covers that, so no card ever covers another, nor what hangs under it. A
// goblin arranged under the goblin it waits on (waits) keeps to the places
// under that goblin first: half a card either side, then the rows below.
export function settle(arranged: Record<string, Point>, placed: Record<string, Point>, extents: Record<string, Extent> = {}, waits: Record<string, string> = {}): Record<string, Point> {
  const positions: Record<string, Point> = {};
  for (const id of Object.keys(arranged)) if (placed[id]) positions[id] = placed[id];
  for (const [id, want] of Object.entries(arranged)) if (!placed[id]) {
    const taken = Object.entries(positions).map(([other, point]) => ({ point, takes: extents[other] || CARD }));
    const takes = extents[id] || CARD, awaited = arranged[waits[id]];
    const isUnder = awaited && want.y > awaited.y && Math.abs(want.x - awaited.x) < NODE_WIDTH;
    const under = isUnder ? [0, 1, 2, 3].flatMap((row) => [want.x, 2 * awaited.x - want.x].map((x) => ({ x, y: want.y + row * ROW }))) : [];
    positions[id] = under.find((place) => place.x >= 0 && !taken.some((other) => clashes(place, other.point, takes, other.takes))) || nearestFree(want, taken, takes);
  }
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
