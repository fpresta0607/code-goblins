import type { ReactNode } from "react";
import type { Session, Snapshot, Task } from "./types";
import { Icon } from "./Icon";
import { PanelHeader } from "./PanelHeader";
import { TaskView } from "./Details";
import { WorkspaceDetails } from "./WorkspaceDetails";
import { ownsTaskSession } from "./lineageTree";
import type { ReviewControls } from "./review";

export type PanelView = "task" | "terminal";

// One panel for a goblin, the same from Board and Orchestration: its task
// view and its live terminal, one tap apart on the pill. The terminals
// themselves live in the terminal deck below the panel, which keeps each one
// live while the board is open, so switching goblins never reconnects.
export function GoblinPanel({ task, node, snapshot, connected, reviews, view, onView, onAnswer, onOpenTask, leading, trailing }: {
  task?: Task; node?: Session; snapshot: Snapshot; connected: boolean; reviews: ReviewControls;
  view: PanelView; onView: (view: PanelView) => void; onAnswer: (key: string) => void; onOpenTask: (task: Task) => void; leading?: ReactNode; trailing: ReactNode;
}) {
  const owner = !!task && ownsTaskSession(node, task);
  return <section className={"goblin-panel" + (view === "terminal" ? " showing-terminal" : "")} aria-labelledby="panel-title">
    <div className="panel-top">
      <div className="panel-top-side">{leading}</div>
      <div className="panel-pill" role="group" aria-label="Panel view">
        <button aria-pressed={view === "task"} onClick={() => onView("task")}><Icon name="task" />Task</button>
        <button aria-pressed={view === "terminal"} onClick={() => onView("terminal")}><Icon name="terminal" />Terminal</button>
      </div>
      <div className="panel-top-side end">{trailing}</div>
    </div>
    <PanelHeader task={task} node={node} snapshot={snapshot} compact={view === "terminal"} onAnswer={onAnswer} onOpenTask={onOpenTask} />
    <div className="panel-task" hidden={view !== "task"}>
      {owner ? <TaskView task={task} snapshot={snapshot} connected={connected} reviews={reviews} /> : <div className="panel-content"><WorkspaceDetails task={task} node={node} /></div>}
    </div>
  </section>;
}
