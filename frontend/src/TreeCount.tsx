import type { FleetTree } from "./types";
import { WorkingIndicator } from "./working-indicator";
import { harnessInk } from "./harness-ink";
import { BabyGoblin } from "./BabyGoblin";
import { Chevron } from "./Chevron";
import { babies, formatMemory, summarize } from "./fleet-tree";

// What a goblin has running under it, collapsed to a count: a baby goblin
// and a number for each kind still running, how many work, are silent or
// idle, and their memory. Pressing it opens or closes the goblin's running
// children.
export function TreeCount({ tree, title, harness = "", expanded, onToggle }: { tree: FleetTree; title: string; harness?: string; expanded: boolean; onToggle: () => void }) {
  const children = babies(tree), summary = summarize({ ...tree, children });
  const memory = formatMemory(children.reduce((sum, node) => sum + node.memory, 0));
  const words = [
    summary.working && summary.working + " working",
    summary.silent && summary.silent + " silent",
    !summary.working && !summary.silent && summary.idle && summary.idle + " idle",
  ].filter(Boolean).join(", ");
  return <button className="tree-count" aria-expanded={expanded}
    aria-label={(expanded ? "Hide" : "Show") + " what runs under " + title + ": " + words + (memory ? ", " + memory : "")} onClick={onToggle}>
    {summary.kinds.map(([baby, count]) => <span key={baby} className="tree-tally"><BabyGoblin baby={baby} small />{count}</span>)}
    {words && <span className={"tree-count-words" + (summary.silent ? " phase-silent" : summary.working ? " phase-working" : "")}>{summary.working ? <WorkingIndicator ink={harnessInk(harness)} compact /> : <span className="status-dot" />}{words}</span>}
    {memory && <span className="tree-count-memory">{memory}</span>}
    <Chevron collapsed={!expanded} />
  </button>;
}
