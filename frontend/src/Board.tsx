import type { BoardActivity, Snapshot, Task } from "./types";
import { CfoPin } from "./CfoPin";
import { FitList } from "./FitList";
import { QueuedTasks } from "./QueuedTasks";
import { RankedCards } from "./RankedCards";
import { RenderBoundary } from "./render-boundary";
import { TaskCard } from "./TaskCard";
import type { CardStarter } from "./useStart";
import { taskColumn } from "./workflow";

const COLUMNS = [
  { name: "Tasks", list: "queued", hint: "Top starts first, when memory allows.", empty: "Nothing queued" },
  { name: "In progress", list: "progress", hint: "Top gets the CFO's attention first.", empty: "No work in progress" },
  { name: "Completed", list: "", hint: "History, newest first.", empty: "Delivered and stopped tasks will appear here" },
] as const;

export type BoardLayout = "kanban" | "stacked";

// The board is a kanban by default, its columns side by side, or stacked,
// one under another. Paused tasks keep their work inside In progress, under a
// divider after its working cards, and show only while one is paused. In
// progress shows every goblin at once; Tasks, Paused and Completed page past
// ten cards.
export function Board({ snapshot, layout, selected, now, onSelect, onTerminal, onOpenCfo, onOpenCommand, onStartCfo, cardStart, presentations }: {
  presentations:BoardActivity[]; layout: BoardLayout;
  snapshot: Snapshot; selected?: string; now: number;
  onSelect: (task: Task, source: HTMLElement) => void;
  onTerminal: (task: Task, source: HTMLElement) => void;
  onOpenCfo: (source: HTMLElement) => void;
  onOpenCommand: () => void;
  onStartCfo: () => void;
  cardStart: CardStarter;
}) {
  const card = (task: Task, rank?: string) => <TaskCard key={task.id} task={task} snapshot={snapshot} selected={selected === task.id} presentations={presentations} now={now} rank={rank} onSelect={onSelect} onTerminal={onTerminal} />;
  const paused = snapshot.tasks.filter((task) => taskColumn(task) === "Paused");
  return <section className={"task-board" + (layout === "stacked" ? " stacked" : "")} aria-label="Task board">
    <CfoPin snapshot={snapshot} onOpen={onOpenCfo} onCommand={onOpenCommand} onStart={onStartCfo} />
    {COLUMNS.map((column) => {
      const tasks = snapshot.tasks.filter((task) => taskColumn(task) === column.name);
      const empty = <p className="column-empty">{column.empty}</p>;
      return <section key={column.name} className="board-column" aria-label={column.name}>
        <h2>{column.name}<span className="column-count">{tasks.length}</span></h2>
        <p className="column-hint">{column.hint}</p>
        {column.list === "queued" ? <QueuedTasks snapshot={snapshot} selected={selected} now={now} presentations={presentations} cardStart={cardStart} onSelect={onSelect} />
          : <RenderBoundary scope="list">{column.list ? <RankedCards list={column.list} tasks={tasks} instance={snapshot.instance} revision={snapshot.revision} empty={empty} renderCard={card} />
            : <FitList items={tasks} keyOf={(task) => task.id} empty={empty} renderItem={(task) => card(task)} />}</RenderBoundary>}
        {column.list === "progress" && paused.length > 0 && <section className="paused-tasks" aria-label="Paused">
          <h3 className="column-divider">Paused<span className="column-count">{paused.length}</span></h3>
          <RenderBoundary scope="list"><FitList items={paused} keyOf={(task) => task.id} empty={null} renderItem={(task) => card(task)} /></RenderBoundary>
        </section>}
      </section>;
    })}
  </section>;
}
