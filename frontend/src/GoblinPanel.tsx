import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
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
import { PanelViewSwitch } from "./panel-view-switch";

export type PanelView = "terminal" | "task";

// One panel for a goblin, the same from Board and Orchestration: its
// conversation, or its task panel, chosen by the selector in its header. The
// terminals themselves live in the terminal deck below the panel, which keeps
// each one live while the board is open, so switching goblins never
// reconnects, and the messages lie in the deck's box, so they never resize a
// terminal. The Overlord asked for that selector back on 2026-10-26: a
// segmented control, never the fold that read as a dropdown. On a maximized
// panel both are visible at once and the selector goes, since there is nothing
// left to switch to.
// A queued task's panel has the same Start as its card, the board's one
// Start (the Overlord, 2026-10-08: "why no run button when it says play in
// panel"). The CFO's Task view also lists every queued task, as the Tasks column does,
// and says which conversation it could not resume when it came back; its
// Restart is in its header. A goblin's baby goblin (child) has the same
// panel, with its name and its task above its terminal, which is all it shows.
export function GoblinPanel({ task, node, child, snapshot, connected, reviews, view, views, onView, maximized, side, now, presentations, cardStart, onAnswer, onOpenTask, row }: {
  task?: Task; node?: Session; child?: TreeNode; snapshot: Snapshot; connected: boolean; reviews: ReviewControls;
  view: PanelView; views: PanelView[]; onView: (view: PanelView) => void; maximized: boolean; side: HTMLElement | null;
  now: number; presentations: BoardActivity[]; onAnswer: (key: string) => void; onOpenTask: (task: Task) => void;
  // row is the top row's controls, corner button and notice (see PanelRow).
  cardStart: CardStarter; row: { controls: PanelControl[]; corner: ReactNode; notice?: ReactNode };
}) {
  const owner = !!task && ownsTaskSession(node, task);
  // A failure's Open the log opens the Activity section and brings it into view.
  const [isLogOpen, setLogOpen] = useState(false);
  const [taskHost] = useState(() => document.createElement("section"));
  const taskMount = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    if (child) return;
    const destination = maximized ? side : taskMount.current;
    taskHost.setAttribute("class", maximized ? "panel-task panel-side-task" : "panel-task");
    taskHost.setAttribute("aria-label", "Task");
    destination?.appendChild(taskHost);
    return () => taskHost.remove();
  }, [child, maximized, side, taskHost]);
  const openLog = () => {
    setLogOpen(true);
    requestAnimationFrame(() => document.getElementById("task-activity")?.scrollIntoView({ block: "start", behavior: "smooth" }));
  };
  const taskList = <>
    {!child && <PanelHeader task={task} node={node} snapshot={snapshot} isDetails onAnswer={onAnswer} onOpenTask={onOpenTask} onOpenLog={openLog} />}
    {task && <div className="panel-content lifecycle-panel">
      <TaskControls task={task} snapshot={snapshot} start={cardStart(task)} />
      <LifecycleDetails task={task} />
    </div>}
    {!task?.archived && owner ? <TaskView task={task} snapshot={snapshot} connected={connected} reviews={reviews} log={{ open: isLogOpen, onOpenChange: setLogOpen }} now={now} onRepair={onAnswer} /> : <div className="panel-content">
      {!task && !node && snapshot.cfo_conversation_left && <p className="cfo-conversation-left">{snapshot.cfo_conversation_left}</p>}
      <WorkspaceDetails task={task} node={node} runs={snapshot.runs} snapshot={snapshot} connected={connected} onRepair={onAnswer} />
      {!task && !node && <section className="cfo-queue" aria-label="Queued tasks">
        <h3>Tasks<span className="column-count">{queuedTasks(snapshot).length}</span></h3>
        <QueuedTasks snapshot={snapshot} now={now} presentations={presentations} onSelect={onOpenTask} />
      </section>}
    </div>}
  </>;
  // A baby goblin's panel is its terminal alone. A maximized panel draws its
  // task list in the column beside the stage instead, so both are visible.
  const list = child ? null : <>
    {!maximized && <div ref={taskMount} className="panel-task" hidden={view !== "task"} />}
    {createPortal(taskList, taskHost)}
  </>;
  return <section className={"goblin-panel" + (view === "terminal" && !child ? " showing-terminal" : "")} aria-labelledby="panel-title">
    {child && task ? <><PanelRow {...row} /><BabyHeader goblin={task} child={child} now={now} /></> : <PanelHeader task={task} node={node} snapshot={snapshot} row={row} switcher={!maximized && <PanelViewSwitch view={view} views={views} onView={onView} />} onAnswer={onAnswer} onOpenTask={onOpenTask} onOpenLog={openLog} />}
    {list}
  </section>;
}
