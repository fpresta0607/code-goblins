import { Icon } from "./Icon";
import { pageLabel } from "./fit";

// The pager under a list that holds more cards than fit: which cards show,
// and the earlier and next pages.
export function Pager({ start, end, count, onTurn }: { start: number; end: number; count: number; onTurn: (step: number) => void }) {
  const label = pageLabel(start, end, count);
  if (!label) return null;
  return <div className="pager" role="group" aria-label="Pages">
    <button className="icon-button raised pager-back" aria-label="Earlier cards" disabled={start === 0} onClick={() => onTurn(-1)}><Icon name="chevron" /></button>
    <span aria-live="polite">{label}</span>
    <button className="icon-button raised" aria-label="Next cards" disabled={end >= count} onClick={() => onTurn(1)}><Icon name="chevron" /></button>
  </div>;
}
