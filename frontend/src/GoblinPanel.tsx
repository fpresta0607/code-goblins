import { useState, type ReactNode } from "react";
import type { BoardActivity, Session, Snapshot, Task } from "./types";
import { PanelHeader } from "./PanelHeader";
import { TaskView } from "./Details";
import { WorkspaceDetails } from "./WorkspaceDetails";
import { ownsTaskSession } from "./lineageTree";
import type { ReviewControls } from "./review";
import { panelViews } from "./cards";
import { QueuedTasks } from "./QueuedTasks";
import type { CardStarter } from "./useStart";
import { queuedTasks } from "./workflow";
import { TaskControls } from "./task-controls";
import { TaskAdjustment } from "./task-adjustment";
import { LifecycleDetails } from "./lifecycle-details";
import { PanelRow, type PanelControl } from "./panel-row";

export type PanelView = "task" | "terminal";

// One panel for a goblin, the same from Board and Orchestration: its task
// view and its live terminal, one tap apart on the pill. The terminals
// themselves live in the terminal deck below the panel, which keeps each one
// live while the board is open, so switching goblins never reconnects.
// The CFO's Task view also lists every queued task, as the Tasks column does.
export function GoblinPanel({ task, node, snapshot, connected, reviews, view, now, presentations, cardStart, onView, onAnswer, onOpenTask, row }: {
  task?: Task; node?: Session; snapshot: Snapshot; connected: boolean; reviews: ReviewControls;
  view: PanelView; now: number; presentations: BoardActivity[]; onView: (view: PanelView) => void; onAnswer: (key: string) => void; onOpenTask: (task: Task) => void;
  // row is the top row's controls, corner button and notice (see PanelRow).
  cardStart: CardStarter; row: { controls: PanelControl[]; corner: ReactNode; notice?: ReactNode };
}) {
  const owner = !!task && ownsTaskSession(node, task);
  const terminal = panelViews(task, node).includes("terminal");
  // A failure's Open the log opens the Activity section and brings it into view.
  const [isLogOpen, setLogOpen] = useState(false);
  const openLog = () => {
    setLogOpen(true);
    requestAnimationFrame(() => document.getElementById("task-activity")?.scrollIntoView({ block: "start", behavior: "smooth" }));
  };
  return <section className={"goblin-panel" + (view === "terminal" ? " showing-terminal" : "")} aria-labelledby="panel-title">
    <PanelRow view={terminal ? view : undefined} onView={onView} {...row} />
    <PanelHeader task={task} node={node} snapshot={snapshot} compact={view === "terminal"} onAnswer={onAnswer} onOpenTask={onOpenTask} onOpenLog={openLog} />
    <div className="panel-task" hidden={view !== "task"}>
      {task && <div className="panel-content lifecycle-panel">
        <TaskControls task={task} snapshot={snapshot} labelled />
        <LifecycleDetails task={task} />
        {task.phase === "queued" && <TaskAdjustment task={task} snapshot={snapshot} />}
      </div>}
      {!task?.archived && owner ? <TaskView task={task} snapshot={snapshot} connected={connected} reviews={reviews} log={{ open: isLogOpen, onOpenChange: setLogOpen }} now={now} onRepair={onAnswer} /> : <div className="panel-content"><WorkspaceDetails task={task} node={node} runs={snapshot.runs} instance={snapshot.instance} startAtLogin={snapshot.start_at_login} onRepair={onAnswer} />
        {!task && !node && <section className="cfo-queue" aria-label="Queued tasks">
          <h3>Tasks<span className="column-count">{queuedTasks(snapshot).length}</span></h3>
          <p className="column-hint">Top starts first, when memory allows.</p>
          <QueuedTasks snapshot={snapshot} now={now} presentations={presentations} cardStart={cardStart} onSelect={(next) => onOpenTask(next)} />
        </section>}
      </div>}
    </div>
  </section>;
}
