import { useRef, useState, type PointerEvent, type ReactNode } from "react";
import type { Task } from "./types";
import { message, request } from "./api";
import { object } from "./types";
import { fitKey } from "./fit";
import { Pager } from "./Pager";
import { RenderBoundary } from "./render-boundary";
import { orderShown, pendingSettled, rankLabel } from "./priority";
import { useFit } from "./useFit";
import { useSortable } from "./useSortable";
import { plainText, withoutHarness } from "./task-words";

// A board list whose order is its priority, top first: a number on each card
// shows its place and turns into a grip on hover or focus. Dropping a card
// saves the order with the supervisor, Tasks as backlog.md's Queued order and
// In progress as the CFO's attention order, and shows it until the snapshot
// agrees; a refused order goes back with the reason. In progress shows every
// goblin's card at once and the board scrolls; Tasks past ten cards shows a
// page at a time: a drag places a card within its page, a card held over a
// page arrow turns the page and goes with it, and a keyboard move past the
// page's edge carries the page with the card.
export function RankedCards({ list, tasks, instance, revision, empty, renderCard }: {
  list: "queued" | "progress"; tasks: Task[]; instance: string; revision: number; empty: ReactNode;
  renderCard: (task: Task, rank: string, index: number) => ReactNode;
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
  const pagerRef = useRef<HTMLDivElement>(null);
  const fit = useFit(listed.map((task, index) => fitKey(task.id, index)), frameRef, listRef, list === "queued");
  const save = async (order: string[], moved: string) => {
    const before = listed.findIndex((task) => task.id === moved);
    const title = withoutHarness(listed[before]?.title || "") || moved;
    latest.current = order;
    setPending({ order, revision: Number.MAX_SAFE_INTEGER });
    setError("");
    setNote(`Moved ${title} to ${order.indexOf(moved) + 1} of ${order.length}.`);
    fit.show(fitKey(moved, order.indexOf(moved)));
    try {
      const saved = object(await request("/api/order", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ list, order }) }));
      if (latest.current !== order) return;
      setPending({ order, revision: typeof saved.revision === "number" ? saved.revision : 0 });
    } catch (failure: unknown) {
      if (latest.current !== order) return;
      setPending(null);
      setError(message(failure));
      fit.show(fitKey(moved, before));
    }
  };
  const { order, onPointerDown, onKeyDown, onClickCapture } = useSortable(listed.map((task) => task.id), (next, moved) => void save(next, moved), fit.start, listRef, { pager: pagerRef, carry: fit.carry });
  const shown = order.map((id) => listed.find((task) => task.id === id)).filter((task): task is Task => !!task);
  const rank = (index: number) => <span className="rank" aria-hidden="true">
    <span className="rank-number">{index + 1}</span>
    <svg className="grip" viewBox="0 0 14 14"><circle cx="4" cy="3" r="1.3" /><circle cx="10" cy="3" r="1.3" /><circle cx="4" cy="7" r="1.3" /><circle cx="10" cy="7" r="1.3" /><circle cx="4" cy="11" r="1.3" /><circle cx="10" cy="11" r="1.3" /></svg>
  </span>;
  // A finger on a card's rank drags the card, so only a swipe elsewhere turns
  // the page.
  const onFramePointerDown = (event: PointerEvent<HTMLDivElement>) => {
    if ((event.target as HTMLElement).closest(".rank")) fit.onPointerCancel();
    else fit.onPointerDown(event);
  };
  return <>
    <div ref={frameRef} className="fit-list" onPointerDown={onFramePointerDown} onPointerUp={fit.onPointerUp} onPointerCancel={fit.onPointerCancel}>
      <div ref={listRef} className="task-cards ranked">
        {shown.slice(fit.start, fit.end).map((task, at) => { const index = fit.start + at; return <div key={task.id} className="ranked-item" data-sort-id={task.id} data-fit-key={fitKey(task.id, index)} onPointerDown={onPointerDown} onKeyDown={onKeyDown} onClickCapture={onClickCapture}>
          {rank(index)}
          <RenderBoundary scope="card">{renderCard(task, rankLabel(index, shown.length), index)}</RenderBoundary>
        </div>; })}
        {!shown.length && empty}
      </div>
      {fit.unmeasured.length > 0 && <div className="task-cards ranked fit-measure" aria-hidden="true" inert>
        {listed.map((task, index) => fit.unmeasured.includes(fitKey(task.id, index)) && <div key={task.id} className="ranked-item" data-fit-key={fitKey(task.id, index)}>
          {rank(index)}
          <RenderBoundary scope="card">{renderCard(task, rankLabel(index, listed.length), index)}</RenderBoundary>
        </div>)}
      </div>}
    </div>
    <Pager ref={pagerRef} start={fit.start} end={fit.end} count={shown.length} onTurn={fit.turn} />
    {error && <p className="order-error" role="alert">{plainText(error)}</p>}
    <p className="sr-only" role="status">{note}</p>
  </>;
}
