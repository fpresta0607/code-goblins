import { useState } from "react";
import type { FleetTree, Task, TreeNode } from "./types";
import { babyName, running } from "./fleet-tree";
import { TreeCount } from "./TreeCount";
import { TreeChild } from "./TreeChild";

// A goblin's running children in the lineage list, where the canvas lays
// itself out at phone width: the count under its card, and once opened, each
// child on a rail, which opens its panel.
export function TreeUnder({ goblin, tree, title, now, selected, onOpen }: { goblin: Task; tree: FleetTree; title: string; now: number; selected?: string; onOpen: (child: TreeNode, source: HTMLElement) => void }) {
  const [isOpen, setOpen] = useState(false);
  return <div className="tree-lineage">
    <TreeCount tree={tree} title={title} expanded={isOpen} onToggle={() => setOpen((prior) => !prior)} />
    {isOpen && <ul className="tree-rail" aria-label={"What runs under " + title}>
      {running(tree).map((child) => <li key={child.id}><TreeChild node={child} name={babyName(goblin, child)} now={now} isSelected={selected === child.id} onOpen={(source) => onOpen(child, source)} /></li>)}
    </ul>}
  </div>;
}
