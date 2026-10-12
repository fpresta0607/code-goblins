import type { FleetTree, TreeNode } from "./types.ts";
import { running } from "./fleet-tree.ts";

// What each goblin's running baby goblins were at the last snapshot, by the
// goblin's canvas id, in the order the canvas hangs them.
export type TreeSeen = Map<string, TreeNode[]>;

// A baby goblin that left its goblin's branches, and the place it hung in.
export interface Departure { goblin: string; node: TreeNode; index: number }

export interface TreeTraffic {
  seen: TreeSeen;
  // Baby goblins a goblin started, those that finished or went, and those
  // that reported something back: new activity, a new last line, or a new
  // state.
  born: { goblin: string; child: string }[];
  left: Departure[];
  reported: { goblin: string; child: string }[];
}

const said = (node: TreeNode) => [node.last_activity, node.last_line, node.state].join("\n");

// treeTraffic compares each goblin's running baby goblins with what they were
// at the last snapshot. The first snapshot has nothing to compare with, so it
// moves nothing.
export function treeTraffic(prior: TreeSeen | null, trees: Record<string, FleetTree>): TreeTraffic {
  const seen: TreeSeen = new Map(Object.entries(trees).map(([goblin, tree]) => [goblin, running(tree)]));
  const traffic: TreeTraffic = { seen, born: [], left: [], reported: [] };
  if (!prior) return traffic;
  for (const [goblin, children] of seen) {
    const before = new Map((prior.get(goblin) || []).map((node) => [node.id, node]));
    for (const child of children) {
      const was = before.get(child.id);
      if (!was) traffic.born.push({ goblin, child: child.id });
      else if (said(was) !== said(child)) traffic.reported.push({ goblin, child: child.id });
    }
  }
  for (const [goblin, children] of prior) {
    const now = new Set((seen.get(goblin) || []).map((node) => node.id));
    children.forEach((node, index) => { if (!now.has(node.id)) traffic.left.push({ goblin, node, index }); });
  }
  return traffic;
}

// withDepartures is a goblin's running baby goblins with those still leaving
// put back where they hung, so the others keep their places until they go.
export function withDepartures(children: TreeNode[], leaving: Departure[]): TreeNode[] {
  const shown = children.filter((node) => !leaving.some((gone) => gone.node.id === node.id));
  for (const gone of [...leaving].sort((one, other) => one.index - other.index)) shown.splice(Math.min(gone.index, shown.length), 0, gone.node);
  return shown;
}
