import type { BoardActivity, Snapshot, Task } from "./types";
import { CfoPin } from "./CfoPin";
import { FitList } from "./FitList";
import { RankedCards } from "./RankedCards";
import { TaskCard } from "./TaskCard";
import { MemoryMeter } from "./MemoryMeter";
import { nextChip, type AcceptedStart } from "./start";
import { useStart } from "./useStart";
import { taskColumn } from "./workflow";

const COLUMNS = [
  { name: "Tasks", list: "queued", hint: "Top starts first, when memory allows.", empty: "Nothing queued" },
  { name: "In progress", list: "progress", hint: "Top gets the CFO's attention first.", empty: "No work in progress" },
  { name: "Completed", list: "", hint: "History, newest first.", empty: "Verified work will appear here" },
] as const;

export function Board({ snapshot, selected, now, onSelect, onTerminal, onOpenCfo, onStartCfo, awaitingStart, onStarted, presentations }: {
  presentations:BoardActivity[];
  snapshot: Snapshot; selected?: string; now: number;
  onSelect: (task: Task, source: HTMLElement) => void;
  onTerminal: (task: Task, source: HTMLElement) => void;
  onOpenCfo: (source: HTMLElement) => void;
  onStartCfo: () => void;
  // awaitingStart is the accepted Start the board waits on; onStarted hears
  // that a queued task's Start was accepted.
  awaitingStart: AcceptedStart | null;
  onStarted: (accepted: AcceptedStart) => void;
}) {
  const cardStart = useStart(snapshot, awaitingStart, onStarted);
  const memory = snapshot.memory;
  const card = (task: Task, rank?: string, index = -1) => {
    const queued = task.phase === "queued" && index >= 0;
    return <TaskCard key={task.id} task={task} snapshot={snapshot} selected={selected === task.id} presentations={presentations} now={now} rank={rank}
      next={queued && index === 0 ? { text: nextChip(memory), waiting: !!memory && memory.available < memory.next } : undefined}
      start={queued ? cardStart(task, index) : undefined} onSelect={onSelect} onTerminal={onTerminal} />;
  };
  return <section className="task-board" aria-label="Task board">
    <CfoPin snapshot={snapshot} onOpen={onOpenCfo} onStart={onStartCfo} />
    {COLUMNS.map((column) => {
      const tasks = snapshot.tasks.filter((task) => taskColumn(task) === column.name);
      const empty = <p className="column-empty">{column.empty}</p>;
      return <section key={column.name} className="board-column" aria-label={column.name}>
        <h2>{column.name}<span className="column-count">{tasks.length}</span></h2>
        <p className="column-hint">{column.hint}</p>
        {column.list === "queued" && memory && <MemoryMeter memory={memory} />}
        {column.list ? <RankedCards list={column.list} tasks={tasks} instance={snapshot.instance} revision={snapshot.revision} empty={empty} renderCard={card} />
          : <FitList items={tasks} keyOf={(task) => task.id} empty={empty} renderItem={(task) => card(task)} />}
      </section>;
    })}
  </section>;
}
