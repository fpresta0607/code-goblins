import { useRef, useState, type PointerEvent, type ReactNode } from "react";
import type { Task } from "./types";
import { message, request } from "./api";
import { object } from "./types";
import { pageOf } from "./fit";
import { Pager } from "./Pager";
import { orderShown, pendingSettled, rankLabel } from "./priority";
import { useFit } from "./useFit";
import { useSortable } from "./useSortable";

// A board list whose order is its priority, top first: a number on each card
// shows its place and turns into a grip on hover or focus. Dropping a card
// saves the order with the supervisor, Tasks as backlog.md's Queued order and
// In progress as the CFO's attention order, and shows it until the snapshot
// agrees; a refused order goes back with the reason. A list longer than the
// board shows a page at a time: a drag places a card within its page, and a
// keyboard move past the page's edge carries the page with the card.
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
  const frameRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const fit = useFit(listed.length, frameRef, listRef);
  const save = async (order: string[], moved: string) => {
    const title = listed.find((task) => task.id === moved)?.title || moved;
    latest.current = order;
    setPending({ order, revision: Number.MAX_SAFE_INTEGER });
    setError("");
    setNote(`Moved ${title} to ${order.indexOf(moved) + 1} of ${order.length}.`);
    fit.show(pageOf(order.indexOf(moved), fit.size));
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
  const { order, onPointerDown, onKeyDown, onClickCapture } = useSortable(listed.map((task) => task.id), (next, moved) => void save(next, moved), fit.start, listRef);
  const shown = order.map((id) => listed.find((task) => task.id === id)).filter((task): task is Task => !!task);
  // A finger on a card's rank drags the card, so only a swipe elsewhere turns
  // the page.
  const onFramePointerDown = (event: PointerEvent<HTMLDivElement>) => {
    if (!(event.target as HTMLElement).closest(".rank")) fit.onPointerDown(event);
  };
  return <>
    <div ref={frameRef} className="fit-list" onPointerDown={onFramePointerDown} onPointerUp={fit.onPointerUp}>
      <div ref={listRef} className="task-cards ranked">
        {shown.slice(fit.start, fit.start + fit.size).map((task, at) => { const index = fit.start + at; return <div key={task.id} className="ranked-item" data-sort-id={task.id} onPointerDown={onPointerDown} onKeyDown={onKeyDown} onClickCapture={onClickCapture}>
          <span className="rank" aria-hidden="true">
            <span className="rank-number">{index + 1}</span>
            <svg className="grip" viewBox="0 0 14 14"><circle cx="4" cy="3" r="1.3" /><circle cx="10" cy="3" r="1.3" /><circle cx="4" cy="7" r="1.3" /><circle cx="10" cy="7" r="1.3" /><circle cx="4" cy="11" r="1.3" /><circle cx="10" cy="11" r="1.3" /></svg>
          </span>
          {renderCard(task, rankLabel(index, shown.length))}
        </div>; })}
        {!shown.length && empty}
      </div>
    </div>
    <Pager page={fit.page} size={fit.size} count={shown.length} onTurn={fit.turn} />
    {error && <p className="order-error" role="alert">{error}</p>}
    <p className="sr-only" role="status">{note}</p>
  </>;
}
