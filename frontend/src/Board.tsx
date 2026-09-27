import type { BoardActivity, Snapshot, Task } from "./types";
import { CfoPin } from "./CfoPin";
import { RankedCards } from "./RankedCards";
import { TaskCard } from "./TaskCard";
import { taskColumn } from "./workflow";

const COLUMNS = [
  { name: "Tasks", list: "queued", hint: "Top starts first, when memory allows.", empty: "Nothing queued" },
  { name: "In progress", list: "progress", hint: "Top gets the CFO's attention first.", empty: "No work in progress" },
  { name: "Completed", list: "", hint: "History, newest first.", empty: "Verified work will appear here" },
] as const;

export function Board({ snapshot, selected, now, onSelect, onTerminal, onOpenCfo, onStartCfo, presentations }: {
  presentations:BoardActivity[];
  snapshot: Snapshot; selected?: string; now: number;
  onSelect: (task: Task, source: HTMLElement) => void;
  onTerminal: (task: Task, source: HTMLElement) => void;
  onOpenCfo: (source: HTMLElement) => void;
  onStartCfo: () => void;
}) {
  const card = (task: Task, rank?: string) => <TaskCard key={task.id} task={task} snapshot={snapshot} selected={selected === task.id} presentations={presentations} now={now} rank={rank} onSelect={onSelect} onTerminal={onTerminal} />;
  return <section className="task-board" aria-label="Task board">
    <CfoPin snapshot={snapshot} onOpen={onOpenCfo} onStart={onStartCfo} />
    {COLUMNS.map((column) => {
      const tasks = snapshot.tasks.filter((task) => taskColumn(task) === column.name);
      const empty = <p className="column-empty">{column.empty}</p>;
      return <section key={column.name} className="board-column" aria-label={column.name}>
        <h2>{column.name}<span className="column-count">{tasks.length}</span></h2>
        <p className="column-hint">{column.hint}</p>
        {column.list ? <RankedCards list={column.list} tasks={tasks} instance={snapshot.instance} revision={snapshot.revision} empty={empty} renderCard={card} />
          : <div className="task-cards">{tasks.map((task) => card(task))}{!tasks.length && empty}</div>}
      </section>;
    })}
  </section>;
}
