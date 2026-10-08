import type { BoardActivity, Snapshot, Task } from "./types";
import { DiskMeter } from "./DiskMeter";
import { MemoryMeter } from "./MemoryMeter";
import { RankedCards } from "./RankedCards";
import { RenderBoundary } from "./render-boundary";
import { TaskCard } from "./TaskCard";
import { nextInOrder, startOrder } from "./start";
import type { CardStarter } from "./useStart";

// The queue in the order the supervisor starts it, top first, as the Tasks
// column and the CFO's Task tab both show it: free memory against the mark at which the CFO starts the next task, free
// disk against its floor, and each queued task with its rank, its drag and
// its Start. The subscription dials are on the CFO's bar, not here.
export function QueuedTasks({ snapshot, selected, now, presentations, cardStart, onSelect }: {
  snapshot: Snapshot; selected?: string; now: number; presentations: BoardActivity[];
  // cardStart is the board's one Start, shared by every list of queued tasks.
  cardStart: CardStarter;
  onSelect: (task: Task, source: HTMLElement) => void;
}) {
  const memory = snapshot.memory;
  const tasks = startOrder(snapshot);
  const next = nextInOrder(snapshot, now);
  return <>
    {(memory || snapshot.disk) && <div className="task-meters">
      {memory && <MemoryMeter memory={memory} scheduling={snapshot.scheduling} disk={snapshot.disk ?? null} />}
      {!memory && snapshot.disk && <div className="memory"><DiskMeter disk={snapshot.disk} /></div>}
    </div>}
    <RenderBoundary scope="list"><RankedCards list="queued" tasks={tasks} instance={snapshot.instance} revision={snapshot.revision} empty={<p className="column-empty">Nothing queued</p>}
      renderCard={(task, rank) => <TaskCard task={task} snapshot={snapshot} selected={selected === task.id} presentations={presentations} now={now} rank={rank}
        next={task.id === next?.id ? next : undefined}
        start={cardStart(task)} onSelect={onSelect} />} /></RenderBoundary>
  </>;
}
