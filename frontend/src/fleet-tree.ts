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

export interface TreeSummary { working: number; silent: number; idle: number; kinds: [Baby, number][] }

// summarize is a goblin's children at a glance: how many work, are silent or
// idle, and how many of each kind are still running.
export function summarize(tree?: FleetTree): TreeSummary {
  const children = tree?.children || [];
  const counts = new Map<Baby, number>();
  for (const node of running(tree)) counts.set(babyFor(node), (counts.get(babyFor(node)) || 0) + 1);
  return {
    working: children.filter((node) => node.state === "working").length,
    silent: children.filter((node) => node.state === "silent").length,
    idle: children.filter(isIdle).length,
    kinds: BABIES.flatMap((baby): [Baby, number][] => counts.has(baby) ? [[baby, counts.get(baby)!]] : []),
  };
}

// silentChild is the child that has been silent longest, which the goblin's
// card names with its last line; none while no child is silent.
export function silentChild(tree?: FleetTree): TreeNode | undefined {
  return (tree?.children || []).filter((node) => node.state === "silent")
    .sort((one, other) => (known(one.last_activity) || "").localeCompare(known(other.last_activity) || ""))[0];
}

// hasChildren says the goblin has anything under it to show in its panel,
// finished children included.
export const hasChildren = (tree?: FleetTree) => (tree?.children.length || 0) > 0;

// hasRunningChildren says the goblin has a child still running or idle,
// which is all the canvas and the lineage list draw under it; its panel lists
// the finished ones too.
export const hasRunningChildren = (tree?: FleetTree) => running(tree).length > 0;

// A baby goblin on its branch, and the branches' measures: the twig from a
// spine to a baby, the gaps between columns and rows, the drop from the
// goblin's card to the first row, and the bend of a joint.
export const BABY_WIDTH = 166, BABY_HEIGHT = 124;
const TWIG = 18, COLUMN_GAP = 14, ROW_GAP = 14, DROP = 44, BEND = 8;

// Branches are a goblin's running children laid out under its card: where
// each baby goblin sits, the lines that join them to the card, and the end of
// each line at a baby. Every point is from the top left of the block, which
// is centred under the card, as wide as the card at least, with its top at
// the card's bottom.
export interface Branches { width: number; height: number; places: { x: number; y: number }[]; lines: string[]; ends: { x: number; y: number }[] }

// branchLayout hangs count baby goblins under a card in about as many
// columns as rows, up to four columns: one hangs straight under the card;
// more hang each from a twig off its column's spine, the spines joined to
// the card by a trunk and a bar, so a big family grows down, not across.
export function branchLayout(count: number, cardWidth: number): Branches {
  const columns = Math.min(4, Math.ceil(Math.sqrt(count))), rows = Math.ceil(count / columns);
  const step = TWIG + BABY_WIDTH + COLUMN_GAP, span = columns * step - COLUMN_GAP;
  const width = Math.max(cardWidth, span), centre = width / 2, left = (width - span) / 2;
  const height = DROP + rows * (BABY_HEIGHT + ROW_GAP) - ROW_GAP;
  if (columns === 1) return { width, height, places: [{ x: centre - BABY_WIDTH / 2, y: DROP }], lines: [`M${centre},0 L${centre},${DROP}`], ends: [{ x: centre, y: DROP }] };
  const bar = DROP / 2, spine = (column: number) => left + column * step, last = spine(columns - 1);
  const places = Array.from({ length: count }, (_, i) => ({ x: spine(i % columns) + TWIG, y: DROP + Math.floor(i / columns) * (BABY_HEIGHT + ROW_GAP) }));
  const middle = (place: { y: number }) => place.y + BABY_HEIGHT / 2;
  const lines = [`M${centre},0 L${centre},${bar} M${spine(0)},${bar + BEND} Q${spine(0)},${bar} ${spine(0) + BEND},${bar} L${last - BEND},${bar} Q${last},${bar} ${last},${bar + BEND}`];
  for (let column = 0; column < columns; column++) {
    const lowest = places.filter((_, i) => i % columns === column).at(-1)!;
    lines.push(`M${spine(column)},${bar + BEND} L${spine(column)},${middle(lowest) - BEND}`);
  }
  for (const place of places) lines.push(`M${place.x - TWIG},${middle(place) - BEND} Q${place.x - TWIG},${middle(place)} ${place.x - TWIG + BEND},${middle(place)} L${place.x},${middle(place)}`);
  return { width, height, places, lines, ends: places.map((place) => ({ x: place.x, y: middle(place) })) };
}

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
