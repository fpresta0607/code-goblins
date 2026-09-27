import { useRef, type ReactNode } from "react";
import { Pager } from "./Pager";
import { useFit } from "./useFit";

// A list of cards in their order, newest first for history, showing as many
// as fit the board's visible canvas and a pager for the rest.
export function FitList<T>({ items, keyOf, empty, renderItem }: { items: T[]; keyOf: (item: T) => string; empty: ReactNode; renderItem: (item: T) => ReactNode }) {
  const frameRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const fit = useFit(items.length, frameRef, listRef);
  return <>
    <div ref={frameRef} className="fit-list" onPointerDown={fit.onPointerDown} onPointerUp={fit.onPointerUp} onPointerCancel={fit.onPointerCancel}>
      <div ref={listRef} className="task-cards">
        {items.slice(fit.start, fit.start + fit.size).map((item) => <div key={keyOf(item)} className="fit-item">{renderItem(item)}</div>)}
        {!items.length && empty}
      </div>
    </div>
    <Pager page={fit.page} size={fit.size} count={items.length} onTurn={fit.turn} />
  </>;
}
