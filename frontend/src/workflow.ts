import type { Session, Snapshot, Task } from "./types.ts";
import { lineageRoots, ownsTaskSession, sessionTitle, tasksWithoutSession } from "./lineageTree.ts";

export type Persona = "cfo" | "builder" | "reviewer" | "tester" | "planner" | "finisher" | "general"
  | "debugger" | "security" | "database" | "designer" | "documentation" | "operations"
  | "researcher" | "performance" | "integrations" | "git" | "accessibility" | "releases";
export type Point = { x: number; y: number };
export const NODE_WIDTH = 292;
export const NODE_HEIGHT = 132;

export function taskColumn(task: Task): "Tasks" | "In progress" | "Completed" {
  if (task.archived) return "Completed";
  if (task.phase === "queued") return "Tasks";
  return task.phase === "done" && task.verified ? "Completed" : "In progress";
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

// A goblin writes the status line a pull request link comes from.
export function safePullRequest(url: string): string {
  return /^https:\/\/[^\s]+$/.test(url) ? url : "";
}

// Status words say what is happening in the Overlord's words, not which
// evidence the supervisor holds.
export function statusText(phase: string): string {
  const labels: Record<string, string> = {
    queued: "Not started", working: "Working", active: "Working", started: "Starting",
    review: "In review gate", ready: "Checks passed", done: "Delivered", merged: "Merged, verifying", idle: "Waiting for input",
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
  return !!taskID && (snapshot.questions || []).some((question) => question.task === taskID && question.status === "pending");
}

export function nodeStatus(node: WorkflowNode, asking = false): string {
  if (node.status) return node.status;
  if (node.task?.archived) return node.task.merged ? "Merged" : "Finished";
  if (node.task && ownsTaskSession(node.session, node.task)) {
    const { phase, reason, verified } = node.task;
    if (asking) return "Waiting on you";
    if ((phase === "blocked" || phase === "failed") && reason.startsWith("Waiting on the CFO")) return "Waiting on the CFO";
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

// Positioning changes presentation only. Cycles retain a visible node but do
// not become recursively laid-out family relationships.
export function arrange(nodes: WorkflowNode[]): Record<string, Point> {
  const positions: Record<string, Point> = {};
  const visited = new Set<string>();
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
        positions[child.id] = { x: 40 + (first + i % columns + row / 2) * (NODE_WIDTH + 44), y: 72 + (depth + 1 + row) * 244 };
      });
      leaf += columns + 1;
      const x = 40 + (first + (columns - 1) / 2 + .25) * (NODE_WIDTH + 44);
      positions[node.id] = { x, y: 72 + depth * 244 };
      return x;
    }
    const xs = children.filter((child) => !visited.has(child.id)).map((child) => place(child, depth + 1));
    const x = xs.length ? (xs[0] + xs[xs.length - 1]) / 2 : 40 + leaf++ * (NODE_WIDTH + 44);
    positions[node.id] = { x, y: 72 + depth * 244 };
    return x;
  };
  const sessions = nodes.flatMap((node) => node.session ? [node.session] : []);
  const rootIDs = new Set(lineageRoots(sessions).map((session) => "session:" + session.id));
  for (const node of nodes) if (!visited.has(node.id) && (!node.parent || rootIDs.has(node.id))) place(node, 0);
  for (const node of nodes) if (!visited.has(node.id)) place(node, 0);
  return positions;
}

export function harnessName(id: string): string {
  const names: Record<string, string> = { codex: "Codex", claude: "Claude Code", pi: "Pi", kimi: "Kimi" };
  return names[id] || id;
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
// many snapshots follow, and a newer report outlives an older one's expiry.
export const PULSE_MS = 6000;

export function reportTraffic(traffic: Record<string, number>, moved: string[], at: number): Record<string, number> {
  return { ...traffic, ...Object.fromEntries(moved.map((id) => [id, at])) };
}

export function expireTraffic(traffic: Record<string, number>, moved: string[], at: number): Record<string, number> {
  return Object.fromEntries(Object.entries(traffic).filter(([id, when]) => !(moved.includes(id) && when === at)));
}
