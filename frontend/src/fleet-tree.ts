import type { FleetTree, Session, Snapshot, TreeNode } from "./types.ts";
import { clockText } from "./cards.ts";

// Baby is the baby goblin a child is drawn as: its kind, and for a job of
// processes what that job is doing. The heads are one generated atlas,
// goblin-babies.png, in this order; a helper goblin, which has no head of
// its own, wears the sub-agent's.
export type Baby = "subagent" | "shell" | "monitor" | "gate" | "server" | "test" | "build" | "browser" | "other" | "helper";
export const BABIES: Baby[] = ["subagent", "shell", "monitor", "gate", "server", "test", "build", "browser", "other", "helper"];

// What each baby goblin is, as its tip says.
export const BABY_NAMES: Record<Baby, string> = {
  subagent: "Sub-agent", shell: "Background shell", monitor: "Monitor", gate: "Gate run",
  server: "Dev server", test: "Test run", build: "Build", browser: "Browser", other: "Process", helper: "Helper goblin",
};

const JOB_BABIES: Record<string, Baby> = { "dev-server": "server", test: "test", build: "build", browser: "browser" };

export function babyFor(node: TreeNode): Baby {
  if (node.kind === "subagent" || node.kind === "shell" || node.kind === "monitor" || node.kind === "gate" || node.kind === "helper") return node.kind;
  return JOB_BABIES[node.group] || "other";
}

export const isFinished = (node: TreeNode) => node.state === "done" || node.state === "failed";

// A child alive but not moving is idle, except a gate, which waits on a
// decision.
const isIdle = (node: TreeNode) => node.state === "waiting" && node.kind !== "gate";

// Idle and finished children are dimmed: the eye goes to what works.
export const isDimmed = (node: TreeNode) => isFinished(node) || isIdle(node);

// phaseOf is the status class a child's dot and words take.
export function phaseOf(node: TreeNode): string {
  if (isIdle(node)) return "idle";
  return node.state || "unknown";
}

export function stateWord(node: TreeNode): string {
  if (isIdle(node)) return "Idle";
  const words: Record<string, string> = { working: "Working", waiting: "Waiting", done: "Done", failed: "Failed", silent: "Silent" };
  return words[node.state] || "Unknown";
}

const known = (timestamp: string) => timestamp && !timestamp.startsWith("0001") ? timestamp : "";

// forHowLong says for how long a child has been where it is: working since
// it started, silent or idle since it last did anything, finished how long
// ago.
export function forHowLong(node: TreeNode, now: number): string {
  if (isFinished(node)) {
    const ago = clockText(known(node.finished) || known(node.last_activity), now, "running");
    return ago && ago !== "just started" ? ago + " ago" : ago && "just now";
  }
  return clockText(node.state === "working" ? known(node.started) : known(node.last_activity) || known(node.started), now, "running");
}

// formatMemory is a child's or a goblin's memory, empty for none.
export function formatMemory(bytes: number): string {
  if (bytes <= 0) return "";
  if (bytes < 2 ** 30) return Math.max(1, Math.round(bytes / 2 ** 20)) + " MB";
  return (bytes / 2 ** 30).toFixed(1) + " GB";
}

// running are the children still at work or alive, and finished those that
// ended, newest first.
export const running = (tree?: FleetTree) => (tree?.children || []).filter((node) => !isFinished(node));
export const finished = (tree?: FleetTree) => (tree?.children || []).filter(isFinished)
  .sort((one, other) => (known(other.finished) || "").localeCompare(known(one.finished) || ""));

export interface TreeSummary { working: number; silent: number; idle: number; finished: number; kinds: [Baby, number][] }

// summarize is a goblin's children at a glance: how many work, are silent,
// idle or finished, and how many of each kind are still running.
export function summarize(tree?: FleetTree): TreeSummary {
  const children = tree?.children || [];
  const counts = new Map<Baby, number>();
  for (const node of running(tree)) counts.set(babyFor(node), (counts.get(babyFor(node)) || 0) + 1);
  return {
    working: children.filter((node) => node.state === "working").length,
    silent: children.filter((node) => node.state === "silent").length,
    idle: children.filter(isIdle).length,
    finished: children.filter(isFinished).length,
    kinds: BABIES.flatMap((baby): [Baby, number][] => counts.has(baby) ? [[baby, counts.get(baby)!]] : []),
  };
}

// silentChild is the child that has been silent longest, which the goblin's
// card names with its last line; none while no child is silent.
export function silentChild(tree?: FleetTree): TreeNode | undefined {
  return (tree?.children || []).filter((node) => node.state === "silent")
    .sort((one, other) => (known(one.last_activity) || "").localeCompare(known(other.last_activity) || ""))[0];
}

// hasChildren says the goblin has anything under it to show.
export const hasChildren = (tree?: FleetTree) => (tree?.children.length || 0) > 0;

// canvasChildren are the children an open goblin shows under its card: every
// one still running and the newest few that finished, so the canvas stays
// calm; its panel lists them all.
export const CANVAS_FINISHED = 3;
export const canvasChildren = (tree?: FleetTree) => [...running(tree), ...finished(tree).slice(0, CANVAS_FINISHED)];

// isHeldByTree is whether a session is a sub-agent a native hook reported whose
// goblin's family tree holds it, or a helper goblin's whose parent's tree
// holds it: it shows as a baby goblin under that goblin, not as a card of its
// own as well.
export const isHeldByTree = (snapshot: Snapshot, session: Session): boolean =>
  session.role === "subagent" && snapshot.tasks.some((task) => task.id === session.task_id && task.tree !== undefined)
  || session.role === "goblin" && isHelperHeld(snapshot, session.task_id);

// isHelperHeld is whether helper task id hangs in its parent's family tree.
export const isHelperHeld = (snapshot: Snapshot, id: string): boolean =>
  snapshot.tasks.some((task) => (task.tree?.children || []).some((node) => node.kind === "helper" && node.id === "helper:" + id));
