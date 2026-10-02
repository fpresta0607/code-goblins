import type { Ref } from "react";
import { Icon } from "./Icon";
import { pageLabel } from "./fit";

// The pager under a list that holds more cards than fit: which cards show,
// and the earlier and next pages. Each arrow says in data-turn which way it
// turns, so a card dragged over it can turn the page.
export function Pager({ start, end, count, onTurn, ref }: { start: number; end: number; count: number; onTurn: (step: number) => void; ref?: Ref<HTMLDivElement> }) {
  const label = pageLabel(start, end, count);
  if (!label) return null;
  return <div ref={ref} className="pager" role="group" aria-label="Pages">
    <button className="icon-button raised pager-back" aria-label="Earlier cards" data-turn="-1" disabled={start === 0} onClick={() => onTurn(-1)}><Icon name="chevron" /></button>
    <span aria-live="polite">{label}</span>
    <button className="icon-button raised" aria-label="Next cards" data-turn="1" disabled={end >= count} onClick={() => onTurn(1)}><Icon name="chevron" /></button>
  </div>;
}
