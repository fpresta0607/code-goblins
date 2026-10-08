import type { TreeNode } from "./types";
import { BabyGoblin } from "./BabyGoblin";
import { BABY_NAMES, babyFor, babyTask, forHowLong, formatMemory, isDimmed, phaseOf, stateWord } from "./fleet-tree";

// One child of a goblin as a small card under it: its baby goblin, its name
// and title, its state and for how long as a plain chip, and its memory.
// Its tip is its task, as every card's is. A finished child is dimmed. With
// onOpen it is a button that opens its panel, pressed while its panel shows.
export function TreeChild({ node, name, now, isSelected = false, onOpen }: { node: TreeNode; name: string; now: number; isSelected?: boolean; onOpen?: (source: HTMLElement) => void }) {
  const when = forHowLong(node, now), memory = formatMemory(node.memory);
  const content = <>
    <BabyGoblin baby={babyFor(node)} hasTip={false} />
    <span className="tree-child-copy">
      <strong>{name}</strong>
      <span className={"state-chip phase-" + phaseOf(node)}><span className="status-dot" />{stateWord(node)}{when && " " + when}</span>
    </span>
    {memory && <span className="tree-memory">{memory}</span>}
  </>;
  const shared = { className: "tree-child" + (isDimmed(node) ? " dim" : "") + (isSelected ? " selected" : ""), "data-state": node.state, "data-tip": babyTask(node) };
  return onOpen
    ? <button type="button" {...shared} aria-pressed={isSelected} aria-label={name + ". " + BABY_NAMES[babyFor(node)] + ". " + stateWord(node) + (when && " " + when)} onClick={(event) => onOpen(event.currentTarget)}>{content}</button>
    : <div {...shared}>{content}</div>;
}
