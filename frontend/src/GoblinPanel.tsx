import type { ReactNode } from "react";
import type { BoardActivity, Session, Snapshot, Task } from "./types";
import { Icon } from "./Icon";
import { PanelHeader } from "./PanelHeader";
import { TaskView } from "./Details";
import { WorkspaceDetails } from "./WorkspaceDetails";
import { ownsTaskSession } from "./lineageTree";
import type { ReviewControls } from "./review";
import { panelViews } from "./cards";
import { QueuedTasks } from "./QueuedTasks";
import type { CardStarter } from "./useStart";
import { queuedTasks } from "./workflow";

export type PanelView = "task" | "terminal";

// One panel for a goblin, the same from Board and Orchestration: its task
// view and its live terminal, one tap apart on the pill. The terminals
// themselves live in the terminal deck below the panel, which keeps each one
// live while the board is open, so switching goblins never reconnects.
// The CFO's Task view also lists every queued task, as the Tasks column does.
export function GoblinPanel({ task, node, snapshot, connected, reviews, view, now, presentations, cardStart, onView, onAnswer, onOpenTask, leading, trailing }: {
  task?: Task; node?: Session; snapshot: Snapshot; connected: boolean; reviews: ReviewControls;
  view: PanelView; now: number; presentations: BoardActivity[]; onView: (view: PanelView) => void; onAnswer: (key: string) => void; onOpenTask: (task: Task) => void;
  cardStart: CardStarter; leading?: ReactNode; trailing: ReactNode;
}) {
  const owner = !!task && ownsTaskSession(node, task);
  const terminal = panelViews(task, node).includes("terminal");
  return <section className={"goblin-panel" + (view === "terminal" ? " showing-terminal" : "")} aria-labelledby="panel-title">
    <div className="panel-top">
      <div className="panel-top-side">{leading}</div>
      {terminal ? <div className="panel-pill" role="group" aria-label="Panel view">
        <button aria-pressed={view === "task"} onClick={() => onView("task")}><Icon name="task" />Task</button>
        <button aria-pressed={view === "terminal"} onClick={() => onView("terminal")}><Icon name="terminal" />Terminal</button>
      </div> : <div />}
      <div className="panel-top-side end">{trailing}</div>
    </div>
    <PanelHeader task={task} node={node} snapshot={snapshot} compact={view === "terminal"} onAnswer={onAnswer} onOpenTask={onOpenTask} />
    <div className="panel-task" hidden={view !== "task"}>
      {owner ? <TaskView task={task} snapshot={snapshot} connected={connected} reviews={reviews} onRepair={onAnswer} /> : <div className="panel-content"><WorkspaceDetails task={task} node={node} onRepair={onAnswer} />
        {!task && !node && <section className="cfo-queue" aria-label="Queued tasks">
          <h3>Tasks<span className="column-count">{queuedTasks(snapshot).length}</span></h3>
          <p className="column-hint">Top starts first, when memory allows.</p>
          <QueuedTasks snapshot={snapshot} now={now} presentations={presentations} cardStart={cardStart} onSelect={(next) => onOpenTask(next)} />
        </section>}
      </div>}
    </div>
  </section>;
}
