import { useRef, useState, type ReactNode } from "react";
import type { Task } from "./types";
import { message, request } from "./api";
import { object } from "./types";
import { orderShown, pendingSettled, rankLabel } from "./priority";
import { useSortable } from "./useSortable";

// A board list whose order is its priority, top first: a number on each card
// shows its place and turns into a grip on hover or focus. Dropping a card
// saves the order with the supervisor, Tasks as backlog.md's Queued order and
// In progress as the CFO's attention order, and shows it until the snapshot
// agrees; a refused order goes back with the reason.
export function RankedCards({ list, tasks, instance, revision, empty, renderCard }: {
  list: "queued" | "progress"; tasks: Task[]; instance: string; revision: number; empty: ReactNode;
  renderCard: (task: Task, rank: string) => ReactNode;
}) {
  const [pending, setPending] = useState<{ order: string[]; revision: number } | null>(null);
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  // The order dropped last; an answer for an earlier drop never overrides it.
  const latest = useRef<string[] | null>(null);
  // A saved order is in every snapshot from the revision the save answered
  // with, and one the list no longer fits gives way to the supervisor's.
  if (pending && (revision >= pending.revision || pendingSettled(tasks.map((task) => task.id), pending.order))) setPending(null);
  const listed = orderShown(tasks, pending?.order || null);
  const save = async (order: string[], moved: string) => {
    const title = listed.find((task) => task.id === moved)?.title || moved;
    latest.current = order;
    setPending({ order, revision: Number.MAX_SAFE_INTEGER });
    setError("");
    setNote(`Moved ${title} to ${order.indexOf(moved) + 1} of ${order.length}.`);
    try {
      const saved = object(await request("/api/order", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ list, order }) }));
      if (latest.current !== order) return;
      setPending({ order, revision: typeof saved.revision === "number" ? saved.revision : 0 });
    } catch (failure: unknown) {
      if (latest.current !== order) return;
      setPending(null);
      setError(message(failure));
    }
  };
  const { listRef, order, onPointerDown, onKeyDown, onClickCapture } = useSortable(listed.map((task) => task.id), (next, moved) => void save(next, moved));
  const shown = order.map((id) => listed.find((task) => task.id === id)).filter((task): task is Task => !!task);
  return <>
    <div ref={listRef} className="task-cards ranked">
      {shown.map((task, index) => <div key={task.id} className="ranked-item" data-sort-id={task.id} onPointerDown={onPointerDown} onKeyDown={onKeyDown} onClickCapture={onClickCapture}>
        <span className="rank" aria-hidden="true">
          <span className="rank-number">{index + 1}</span>
          <svg className="grip" viewBox="0 0 14 14"><circle cx="4" cy="3" r="1.3" /><circle cx="10" cy="3" r="1.3" /><circle cx="4" cy="7" r="1.3" /><circle cx="10" cy="7" r="1.3" /><circle cx="4" cy="11" r="1.3" /><circle cx="10" cy="11" r="1.3" /></svg>
        </span>
        {renderCard(task, rankLabel(index, shown.length))}
      </div>)}
      {!shown.length && empty}
    </div>
    {error && <p className="order-error" role="alert">{error}</p>}
    <p className="sr-only" role="status">{note}</p>
  </>;
}
