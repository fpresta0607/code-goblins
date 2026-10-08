import type { FleetTree, Session, Snapshot, Task, TreeNode } from "./types.ts";
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

// Finished children are dimmed. An idle one is not, since it can still be
// opened: the Overlord, 2026-10-08, "why are the idle ones unclickable".
export const isDimmed = isFinished;

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

// What a baby goblin of each kind is called when its own description says
// nothing a title can be made of.
const KIND_TITLES: Record<Baby, string> = {
  subagent: "Scout", shell: "Shell Runner", monitor: "Watcher", gate: "Gatekeeper", server: "Server Keeper",
  test: "Test Runner", build: "Builder", browser: "Browser Pilot", other: "Process Wrangler", helper: "Helper",
};

// The one who does each thing a description can start with.
const DOERS: Record<string, string> = {
  add: "Adder", analyze: "Analyst", audit: "Auditor", build: "Builder", check: "Checker", clean: "Cleaner", compare: "Comparer",
  confirm: "Confirmer", count: "Counter", debug: "Debugger", design: "Designer", draft: "Drafter", explore: "Explorer", fetch: "Fetcher",
  find: "Finder", fix: "Fixer", gather: "Gatherer", inspect: "Inspector", investigate: "Investigator", list: "Lister", locate: "Locator",
  map: "Mapper", measure: "Measurer", migrate: "Migrator", monitor: "Monitor", plan: "Planner", poll: "Poller", probe: "Prober",
  prove: "Prover", read: "Reader", refactor: "Refactorer", render: "Renderer", research: "Researcher", review: "Reviewer", run: "Runner",
  scan: "Scanner", search: "Searcher", start: "Starter", summarize: "Summarizer", survey: "Surveyor", test: "Tester", trace: "Tracer",
  track: "Tracker", update: "Updater", verify: "Verifier", watch: "Watcher", write: "Writer",
};

// Words a description's object is never.
const SMALL_WORDS = new Set(["a", "an", "the", "of", "for", "to", "in", "on", "at", "and", "or", "with", "by", "from", "its", "it", "this", "that", "all", "each", "every"]);

// titleFor is a short fun title for what a baby goblin does, made from its
// own description as the one who does it: "Start the board" is a Board
// Starter, "Test dictation" a Dictation Tester. A description that starts
// with no such deed leaves the title of its kind.
export function titleFor(node: TreeNode): string {
  const words = node.label.split(/\s+/).map((word) => word.replace(/^[^\p{L}\d]+|[^\p{L}\d]+$/gu, "")).filter(Boolean);
  const doer = words.length ? DOERS[words[0].toLowerCase()] : undefined;
  if (!doer) return KIND_TITLES[babyFor(node)];
  const object = words.slice(1).reverse().find((word) => /^\p{L}[\p{L}\d'-]*$/u.test(word) && !SMALL_WORDS.has(word.toLowerCase()));
  if (!object) return doer;
  const one = object !== object.toLowerCase() ? object
    : /(ch|sh|x|ss)es$/.test(object) ? object.slice(0, -2) : /ies$/.test(object) ? object.slice(0, -3) + "y"
      : /[^su]s$/.test(object) && object.length > 3 ? object.slice(0, -1) : object;
  return one[0].toUpperCase() + one.slice(1) + " " + doer;
}

// The ordinal after a goblin's name its baby goblins take in turn: Jr., then
// II, III and on.
function ordinal(index: number): string {
  if (!index) return "Jr.";
  let rest = index + 1, numeral = "";
  for (const [value, letters] of [[100, "C"], [90, "XC"], [50, "L"], [40, "XL"], [10, "X"], [9, "IX"], [5, "V"], [4, "IV"], [1, "I"]] as [number, string][]) {
    for (; rest >= value; rest -= value) numeral += letters;
  }
  return numeral;
}

// babyName is what a goblin's baby goblin is called: after its goblin, in
// the order they started, Kip Jr., then Kip II, Kip III and on, each with a
// title for its own job, as goblins are called by name and title. A helper
// is a goblin of its own and keeps its own name.
export function babyName(goblin: Task, node: TreeNode): string {
  if (node.kind === "helper") return node.label;
  const title = titleFor(node);
  if (!goblin.goblin_name) return title;
  const order = (goblin.tree?.children || []).filter((child) => child.kind !== "helper")
    .sort((one, other) => one.started.localeCompare(other.started) || one.id.localeCompare(other.id));
  return goblin.goblin_name + " " + ordinal(Math.max(0, order.findIndex((child) => child.id === node.id))) + " - " + title;
}

// babyTask is what a baby goblin was asked to do, which its tip and its
// panel say: a sub-agent's prompt, a background command, a helper's task,
// or else what it does.
export const babyTask = (node: TreeNode) => node.task || node.label;

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

// hasChildren says the goblin has anything under it to show in its panel,
// finished children included.
export const hasChildren = (tree?: FleetTree) => (tree?.children.length || 0) > 0;

// hasRunningChildren says the goblin has a child still running or idle,
// which is all the canvas and the lineage list draw under it; its panel lists
// the finished ones too.
export const hasRunningChildren = (tree?: FleetTree) => running(tree).length > 0;

// A baby goblin on its branch, and the branches' measures: the room beside a
// column where its spine runs, the gaps between columns and rows, the drop
// from the goblin's card to the first row, and the bend of a joint.
export const BABY_WIDTH = 166, BABY_HEIGHT = 124;
const TWIG = 18, COLUMN_GAP = 14, ROW_GAP = 28, DROP = 44, BEND = 8;

interface Point { x: number; y: number }

// elbows is the line through points, which turn only at right angles, with
// each turn rounded.
export function elbows(points: Point[]): string {
  const distinct = points.filter((point, i) => !i || point.x !== points[i - 1].x || point.y !== points[i - 1].y);
  const turns = distinct.filter((point, i) => !i || i === distinct.length - 1
    || !(distinct[i - 1].x === point.x && point.x === distinct[i + 1].x || distinct[i - 1].y === point.y && point.y === distinct[i + 1].y));
  const length = (one: Point, other: Point) => Math.abs(one.x - other.x) + Math.abs(one.y - other.y);
  const toward = (from: Point, to: Point, by: number) => ({ x: from.x + Math.sign(to.x - from.x) * by, y: from.y + Math.sign(to.y - from.y) * by });
  let path = `M${turns[0].x},${turns[0].y}`;
  for (let i = 1; i < turns.length - 1; i++) {
    const [before, turn, after] = [turns[i - 1], turns[i], turns[i + 1]];
    const bend = Math.min(BEND, length(before, turn) / 2, length(turn, after) / 2);
    const [into, out] = [toward(turn, before, bend), toward(turn, after, bend)];
    path += ` L${into.x},${into.y} Q${turn.x},${turn.y} ${out.x},${out.y}`;
  }
  const end = turns[turns.length - 1];
  return turns.length > 1 ? path + ` L${end.x},${end.y}` : path;
}

// Branches are a goblin's running children laid out under its card: where
// each baby goblin sits, the branch from the card to each, which ends at the
// middle of the baby's top, and where each line that runs down sits from the
// card's middle. bar is how far under the card the branches part, and lane
// is where, from the card's middle, a line runs down past every baby goblin
// to a goblin under them. Every point is from the top left of the block,
// which is centred under the card, as wide as the card at least, with its
// top at the card's bottom.
export interface Branches { width: number; height: number; places: Point[]; lines: string[]; ends: Point[]; drops: number[]; bar: number; lane: number }

// branchLayout hangs count baby goblins under a card in about as many
// columns as rows, up to four columns, centred under it, so a big family
// grows down, not across. A trunk drops from the card to a bar, and from the
// bar a branch drops into the top of each baby of the first row; a baby
// lower down is reached by the spine beside its column, which turns into the
// gap above it and drops into its top.
export function branchLayout(count: number, cardWidth: number): Branches {
  const columns = Math.min(4, Math.ceil(Math.sqrt(count))), rows = Math.ceil(count / columns);
  const step = TWIG + BABY_WIDTH + COLUMN_GAP, span = columns * step - TWIG - COLUMN_GAP;
  const width = Math.max(cardWidth, span + 2 * TWIG), centre = width / 2, first = centre - span / 2;
  const height = DROP + rows * (BABY_HEIGHT + ROW_GAP) - ROW_GAP, bar = DROP / 2;
  const spine = (column: number) => first + column * step - TWIG;
  const places = Array.from({ length: count }, (_, i) => ({ x: first + (i % columns) * step, y: DROP + Math.floor(i / columns) * (BABY_HEIGHT + ROW_GAP) }));
  const ends = places.map((place) => ({ x: place.x + BABY_WIDTH / 2, y: place.y }));
  const lines = places.map((place, i) => {
    const end = ends[i], column = i % columns, gap = place.y - ROW_GAP / 2;
    const down = place.y === DROP ? [{ x: end.x, y: bar }] : [{ x: spine(column), y: bar }, { x: spine(column), y: gap }, { x: end.x, y: gap }];
    return elbows([{ x: centre, y: 0 }, { x: centre, y: bar }, ...down, end]);
  });
  return { width, height, places, lines, ends, bar, lane: spine(0) - centre, drops: [0, ...Array.from({ length: columns }, (_, column) => spine(column) - centre)] };
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
