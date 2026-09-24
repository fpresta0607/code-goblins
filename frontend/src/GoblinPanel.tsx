import { lazy, Suspense, useState, type ReactNode } from "react";
import type { Session, Snapshot, Task } from "./types";
import { Icon } from "./Icon";
import { PanelHeader } from "./PanelHeader";
import { TaskView } from "./Details";
import { WorkspaceDetails } from "./WorkspaceDetails";
import { ownsTaskSession } from "./lineageTree";
import type { ReviewControls } from "./review";

const NativeTerminal = lazy(() => import("./NativeTerminal").then((module) => ({ default: module.NativeTerminal })));

export type PanelView = "task" | "terminal";

// One panel for a goblin, the same from Board and Orchestration: its task
// view and its live terminal, one tap apart on the pill. Both stay mounted
// once opened, so switching keeps scroll position and selection.
export function GoblinPanel({ task, node, snapshot, connected, reviews, view, onView, onOwner, onOpenTask, leading, trailing }: {
  task?: Task; node?: Session; snapshot: Snapshot; connected: boolean; reviews: ReviewControls;
  view: PanelView; onView: (view: PanelView) => void; onOwner?: () => void; onOpenTask: (task: Task) => void; leading?: ReactNode; trailing: ReactNode;
}) {
  const [terminalOpened, setTerminalOpened] = useState(view === "terminal");
  if (view === "terminal" && !terminalOpened) setTerminalOpened(true);
  const owner = !!task && ownsTaskSession(node, task);
  return <section className="goblin-panel" aria-labelledby="panel-title">
    <div className="panel-top">
      <div className="panel-top-side">{leading}</div>
      <div className="panel-pill" role="group" aria-label="Panel view">
        <button aria-pressed={view === "task"} onClick={() => onView("task")}><Icon name="task" />Task</button>
        <button aria-pressed={view === "terminal"} onClick={() => onView("terminal")}><Icon name="terminal" />Terminal</button>
      </div>
      <div className="panel-top-side end">{trailing}</div>
    </div>
    <PanelHeader task={task} node={node} snapshot={snapshot} compact={view === "terminal"} onOpenTask={onOpenTask} />
    <div className="panel-task" hidden={view !== "task"}>
      {owner ? <TaskView task={task} snapshot={snapshot} connected={connected} reviews={reviews} /> : <div className="panel-content"><WorkspaceDetails task={task} node={node} /></div>}
    </div>
    {terminalOpened && <div className="panel-terminal" hidden={view !== "terminal"}>
      <Suspense fallback={<p className="loading">Opening the terminal...</p>}>
        <NativeTerminal task={task} node={node} instance={snapshot.instance} visible={connected} shown={view === "terminal"} onOwner={onOwner} />
      </Suspense>
    </div>}
  </section>;
}
