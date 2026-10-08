import type { Task, TreeNode } from "./types";
import { BabyGoblin } from "./BabyGoblin";
import { babyFor, babyName, babyTask, forHowLong, phaseOf, stateWord } from "./fleet-tree";

// The top of a baby goblin's panel, where a goblin's panel names the goblin:
// its head, its name and its task on one line, and its state, for how long
// and what kind of agent it is.
export function BabyHeader({ goblin, child, now }: { goblin: Task; child: TreeNode; now: number }) {
  const when = forHowLong(child, now);
  const detail = child.kind === "subagent" ? child.detail : "";
  return <header className="panel-header compact baby-header">
    <BabyGoblin baby={babyFor(child)} hasTip={false} />
    <div className="panel-identity">
      <h2 id="panel-title" className="panel-child-title"><span>{babyName(goblin, child)}</span><span className="panel-child-task">{babyTask(child)}</span></h2>
      <p className={"panel-status plain-status phase-" + phaseOf(child)}><span className="status-dot" />{stateWord(child)}{when && " " + when}{detail && " · " + detail}</p>
    </div>
  </header>;
}
