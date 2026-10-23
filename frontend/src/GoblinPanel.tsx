import { useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import type { BoardActivity, Session, Snapshot, Task, TreeNode } from "./types";
import { PanelHeader } from "./PanelHeader";
import { TaskView } from "./Details";
import { WorkspaceDetails } from "./WorkspaceDetails";
import { ownsTaskSession } from "./lineageTree";
import type { ReviewControls } from "./review";
import { QueuedTasks } from "./QueuedTasks";
import type { CardStarter } from "./useStart";
import { queuedTasks } from "./workflow";
import { TaskControls } from "./task-controls";
import { LifecycleDetails } from "./lifecycle-details";
import { PanelRow, type PanelControl } from "./panel-row";
import { BabyHeader } from "./baby-header";
import { Disclosure } from "./Disclosure";

export type PanelView = "terminal" | "task";

// Where a panel draws its task list: beside the stage that holds its messages
// and its terminal, folded under its header on a small screen, or as the
// whole panel where it has no terminal.
export type TaskPlace = "side" | "fold" | "alone";

// One panel for a goblin, the same from Board and Orchestration, one view
// with no tabs (the Overlord, 2026-10-23: "Can't we just have one, instead of
// the messages and terminal being split up?"): its messages, or its live
// terminal, with its task list beside them. The terminals themselves live in
// the terminal deck below the panel, which keeps each one live while the board
// is open, so switching goblins never reconnects, and the messages lie in the
// deck's box, so they never resize a terminal. The task list beside them is
// drawn into side, the column the board keeps beside that box.
// A queued task's panel has the same Start as its card, the board's one
// Start (the Overlord, 2026-10-08: "why no run button when it says play in
// panel"). The CFO's Task view also lists every queued task, as the Tasks column does,
// and says which conversation it could not resume when it came back; its
// Restart is in its header. A goblin's baby goblin (child) has the same
// panel, with its name and its task above its terminal, which is all it shows.
export function GoblinPanel({ task, node, child, snapshot, connected, reviews, taskPlace, side, now, presentations, cardStart, onAnswer, onOpenTask, row }: {
  task?: Task; node?: Session; child?: TreeNode; snapshot: Snapshot; connected: boolean; reviews: ReviewControls;
  taskPlace: TaskPlace; side: HTMLElement | null; now: number; presentations: BoardActivity[]; onAnswer: (key: string) => void; onOpenTask: (task: Task) => void;
  // row is the top row's controls, corner button and notice (see PanelRow).
  cardStart: CardStarter; row: { controls: PanelControl[]; corner: ReactNode; notice?: ReactNode };
}) {
  const owner = !!task && ownsTaskSession(node, task);
  // A failure's Open the log opens the Activity section and brings it into view.
  const [isLogOpen, setLogOpen] = useState(false);
  const openLog = () => {
    setLogOpen(true);
    requestAnimationFrame(() => document.getElementById("task-activity")?.scrollIntoView({ block: "start", behavior: "smooth" }));
  };
  const isStaged = taskPlace !== "alone";
  // Beside a stage the header is compact, so the task list opens with what the
  // full header would add: its project, its activity and its links.
  const taskList = <>
    {isStaged && <PanelHeader task={task} node={node} snapshot={snapshot} compact={false} isDetails onAnswer={onAnswer} onOpenTask={onOpenTask} onOpenLog={openLog} />}
    {task && <div className="panel-content lifecycle-panel">
      <TaskControls task={task} snapshot={snapshot} start={cardStart(task)} />
      <LifecycleDetails task={task} />
    </div>}
    {!task?.archived && owner ? <TaskView task={task} snapshot={snapshot} connected={connected} reviews={reviews} log={{ open: isLogOpen, onOpenChange: setLogOpen }} now={now} onRepair={onAnswer} /> : <div className="panel-content">
      {!task && !node && snapshot.cfo_conversation_left && <p className="cfo-conversation-left">{snapshot.cfo_conversation_left}</p>}
      <WorkspaceDetails task={task} node={node} runs={snapshot.runs} snapshot={snapshot} connected={connected} onRepair={onAnswer} />
      {!task && !node && <section className="cfo-queue" aria-label="Queued tasks">
        <h3>Tasks<span className="column-count">{queuedTasks(snapshot).length}</span></h3>
        <QueuedTasks snapshot={snapshot} now={now} presentations={presentations} onSelect={(next) => onOpenTask(next)} />
      </section>}
    </div>}
  </>;
  // A baby goblin's panel is its terminal alone.
  const list = child ? null : taskPlace === "alone" ? <div className="panel-task">{taskList}</div>
    : taskPlace === "fold" ? <div className="panel-task-fold"><Disclosure title="Task">{taskList}</Disclosure></div>
      : side && createPortal(<section className="panel-task panel-side-task" aria-label="Task">{taskList}</section>, side);
  return <section className={"goblin-panel" + (isStaged ? " showing-terminal" : "")} aria-labelledby="panel-title">
    <PanelRow {...row} />
    {child && task ? <BabyHeader goblin={task} child={child} now={now} /> : <PanelHeader task={task} node={node} snapshot={snapshot} compact={isStaged} onAnswer={onAnswer} onOpenTask={onOpenTask} onOpenLog={openLog} />}
    {list}
  </section>;
}
