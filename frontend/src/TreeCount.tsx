import type { FleetTree } from "./types";
import { BabyGoblin } from "./BabyGoblin";
import { Chevron } from "./Chevron";
import { formatMemory, running, summarize } from "./fleet-tree";

// What a goblin has running under it, collapsed to a count: a baby goblin
// and a number for each kind still running, how many work, are silent or
// idle, and their memory. Pressing it opens or closes the goblin's running
// children.
export function TreeCount({ tree, title, expanded, onToggle }: { tree: FleetTree; title: string; expanded: boolean; onToggle: () => void }) {
  const summary = summarize(tree);
  const memory = formatMemory(running(tree).reduce((sum, node) => sum + node.memory, 0));
  const words = [
    summary.working && summary.working + " working",
    summary.silent && summary.silent + " silent",
    !summary.working && !summary.silent && summary.idle && summary.idle + " idle",
  ].filter(Boolean).join(", ");
  return <button className={"tree-count" + (summary.silent ? " has-silent" : "")} aria-expanded={expanded}
    aria-label={(expanded ? "Hide" : "Show") + " what runs under " + title + ": " + words + (memory ? ", " + memory : "")} onClick={onToggle}>
    {summary.kinds.map(([baby, count]) => <span key={baby} className="tree-tally"><BabyGoblin baby={baby} small />{count}</span>)}
    {words && <span className={"tree-count-words" + (summary.silent ? " phase-silent" : summary.working ? " phase-working" : "")}><span className="status-dot" />{words}</span>}
    {memory && <span className="tree-count-memory">{memory}</span>}
    <Chevron collapsed={!expanded} />
  </button>;
}
