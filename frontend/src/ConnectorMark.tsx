import { BrandGlyph } from "./brand-glyph";
import { Icon } from "./Icon";
import type { Mark } from "./connectors";

// A service's mark on a round tile, named by its tooltip and accessible label.
export function ConnectorMark({ mark, label }: { mark: Mark; label: string }) {
  if ("glyph" in mark) return <span className="mark" role="img" aria-label={label} data-tip={label}><Icon name={mark.glyph} /></span>;
  return <span className="mark" role="img" aria-label={label} data-tip={label}><BrandGlyph brand={mark.brand} /></span>;
}
