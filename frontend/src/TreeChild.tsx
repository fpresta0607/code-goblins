import type { TreeNode } from "./types";
import { BabyGoblin } from "./BabyGoblin";
import { babyFor, forHowLong, formatMemory, isDimmed, phaseOf, stateWord } from "./fleet-tree";

// One child of a goblin as a small card under it: its baby goblin, what it
// is doing, its state and for how long, and its memory. Idle and finished
// children are dimmed; a silent one shows its last line in its tip.
export function TreeChild({ node, now }: { node: TreeNode; now: number }) {
  const phase = phaseOf(node), when = forHowLong(node, now), memory = formatMemory(node.memory);
  const detail = node.kind === "subagent" || node.kind === "process" ? node.detail : "";
  return <div className={"tree-child" + (isDimmed(node) ? " dim" : "")} data-state={node.state}
    {...(node.state === "silent" && node.last_line ? { "data-tip": "Last line: " + node.last_line } : {})}>
    <BabyGoblin baby={babyFor(node)} silent={node.state === "silent"} />
    <span className="tree-child-copy">
      <strong>{node.label}</strong>
      <span className={"plain-status phase-" + phase}><span className="status-dot" />{stateWord(node)}{when && " " + when}{detail && <span className="tree-child-detail"> · {detail}</span>}</span>
    </span>
    {memory && <span className="tree-memory">{memory}</span>}
  </div>;
}
