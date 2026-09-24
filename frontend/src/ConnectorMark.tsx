import { BRAND_MARKS } from "./brandMarks";
import { Icon } from "./Icon";
import type { Mark } from "./connectors";

// A service's mark on a round tile, named by its tooltip and accessible label.
export function ConnectorMark({ mark, label }: { mark: Mark; label: string }) {
  if ("glyph" in mark) return <span className="mark" role="img" aria-label={label} data-tip={label} data-tip-align="start"><Icon name={mark.glyph} /></span>;
  const brand = BRAND_MARKS[mark.brand];
  return <span className="mark" role="img" aria-label={label} data-tip={label} data-tip-align="start">
    <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false" fill={brand.ink}><path d={brand.path} /></svg>
  </span>;
}
