import { BRAND_MARKS } from "./brandMarks";
import { Icon } from "./Icon";
import type { Mark } from "./connectors";

// A service's mark on a round tile, named by its tooltip and accessible label.
// align says which edge the tooltip keeps to: start, or end for a mark at the
// right edge of its box.
export function ConnectorMark({ mark, label, align = "start" }: { mark: Mark; label: string; align?: "start" | "end" }) {
  if ("glyph" in mark) return <span className="mark" role="img" aria-label={label} data-tip={label} data-tip-align={align}><Icon name={mark.glyph} /></span>;
  const brand = BRAND_MARKS[mark.brand];
  return <span className="mark" role="img" aria-label={label} data-tip={label} data-tip-align={align}>
    <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false" fill={brand.ink}><path d={brand.path} /></svg>
  </span>;
}
