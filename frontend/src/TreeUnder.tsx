import { useState } from "react";
import type { FleetTree } from "./types";
import { canvasChildren } from "./fleet-tree";
import { TreeCount } from "./TreeCount";
import { TreeChild } from "./TreeChild";

// A goblin's children in the lineage list, where the canvas lays itself out
// at phone width: the count under its card, and once opened, each child on a
// rail.
export function TreeUnder({ tree, title, now }: { tree: FleetTree; title: string; now: number }) {
  const [isOpen, setOpen] = useState(false);
  return <div className="tree-lineage">
    <TreeCount tree={tree} title={title} expanded={isOpen} onToggle={() => setOpen((prior) => !prior)} />
    {isOpen && <ul className="tree-rail" aria-label={"What runs under " + title}>
      {canvasChildren(tree).map((child) => <li key={child.id}><TreeChild node={child} now={now} /></li>)}
    </ul>}
  </div>;
}
