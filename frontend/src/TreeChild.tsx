import type { TreeNode } from "./types";
import { BabyGoblin } from "./BabyGoblin";
import { BABY_NAMES, babyFor, babyTask, forHowLong, formatMemory, isDimmed, phaseOf, stateWord, whatItRuns } from "./fleet-tree";

// One child of a goblin as a small card under it: its baby goblin, its name
// and title, what it runs in plain words, its state and for how long as a
// plain chip, and its memory.
// Its tip is its task, as every card's is. A finished child is dimmed. With
// onOpen it is a button that opens its panel, pressed while its panel shows.
export function TreeChild({ node, name, now, isSelected = false, onOpen }: { node: TreeNode; name: string; now: number; isSelected?: boolean; onOpen?: (source: HTMLElement) => void }) {
  const when = forHowLong(node, now), memory = formatMemory(node.memory), runs = whatItRuns(node);
  const model = node.kind === "subagent" || node.kind === "helper" ? node.model && node.model !== "default" ? node.model : "Model not recorded" : "";
  const content = <>
    <BabyGoblin baby={babyFor(node)} hasTip={false} />
    <span className="tree-child-copy">
      <strong>{name}</strong>
      <span className="tree-child-runs">{runs}</span>
      {model && <span className="tree-child-model" data-tip={model}>{model}</span>}
      <span className={"state-chip phase-" + phaseOf(node)}><span className="status-dot" /><span className="state-chip-text">{stateWord(node)}{when && " " + when}</span></span>
    </span>
    {memory && <span className="tree-memory">{memory}</span>}
  </>;
  const shared = { className: "tree-child" + (isDimmed(node) ? " dim" : "") + (isSelected ? " selected" : ""), "data-state": node.state, "data-tip": babyTask(node) };
  return onOpen
    ? <button type="button" {...shared} aria-pressed={isSelected} aria-label={name + ". " + BABY_NAMES[babyFor(node)] + ", " + runs + (model && ". " + model) + ". " + stateWord(node) + (when && " " + when)} onClick={(event) => onOpen(event.currentTarget)}>{content}</button>
    : <div {...shared}>{content}</div>;
}
