import { useRef, type ReactNode } from "react";
import { Pager } from "./Pager";
import { RenderBoundary } from "./render-boundary";
import { useFit } from "./useFit";

// A list of cards in their order, newest first for history, showing up to ten
// whole and, past that, as many as fit the board's visible canvas and a pager
// for the rest.
export function FitList<T>({ items, keyOf, empty, renderItem }: { items: T[]; keyOf: (item: T) => string; empty: ReactNode; renderItem: (item: T) => ReactNode }) {
  const frameRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const fit = useFit(items.map(keyOf), frameRef, listRef);
  return <>
    <div ref={frameRef} className="fit-list" onPointerDown={fit.onPointerDown} onPointerUp={fit.onPointerUp} onPointerCancel={fit.onPointerCancel}>
      <div ref={listRef} className="task-cards">
        {items.slice(fit.start, fit.end).map((item) => <div key={keyOf(item)} className="fit-item" data-fit-key={keyOf(item)}><RenderBoundary scope="card">{renderItem(item)}</RenderBoundary></div>)}
        {!items.length && empty}
      </div>
      {fit.unmeasured.length > 0 && <div className="task-cards fit-measure" aria-hidden="true" inert>
        {items.filter((item) => fit.unmeasured.includes(keyOf(item))).map((item) => <div key={keyOf(item)} className="fit-item" data-fit-key={keyOf(item)}><RenderBoundary scope="card">{renderItem(item)}</RenderBoundary></div>)}
      </div>}
    </div>
    <Pager start={fit.start} end={fit.end} count={items.length} onTurn={fit.turn} />
  </>;
}
