import type { BoardActivity, Snapshot, Task } from "./types";
import { MemoryMeter } from "./MemoryMeter";
import { RankedCards } from "./RankedCards";
import { TaskCard } from "./TaskCard";
import { nextChip } from "./start";
import type { CardStarter } from "./useStart";
import { queuedTasks } from "./workflow";

// The queue, top first, as the Tasks column and the CFO's Task tab both show
// it: free memory against the mark at which the CFO starts the next task, and
// each queued task with its rank, its drag and its Start.
export function QueuedTasks({ snapshot, selected, now, presentations, cardStart, onSelect }: {
  snapshot: Snapshot; selected?: string; now: number; presentations: BoardActivity[];
  // cardStart is the board's one Start, shared by every list of queued tasks.
  cardStart: CardStarter;
  onSelect: (task: Task, source: HTMLElement) => void;
}) {
  const memory = snapshot.memory;
  return <>
    {memory && <MemoryMeter memory={memory} />}
    <RankedCards list="queued" tasks={queuedTasks(snapshot)} instance={snapshot.instance} revision={snapshot.revision} empty={<p className="column-empty">Nothing queued</p>}
      renderCard={(task, rank, index) => <TaskCard task={task} snapshot={snapshot} selected={selected === task.id} presentations={presentations} now={now} rank={rank}
        next={index === 0 ? { text: nextChip(memory), waiting: !!memory && memory.available < memory.next } : undefined}
        start={cardStart(task)} onSelect={onSelect} onTerminal={onSelect} />} />
  </>;
}
