import { BRAND_MARKS, type BrandKey } from "./brandMarks";

// A service's own mark, bundled with the board, in its brand ink.
export function BrandGlyph({ brand }: { brand: BrandKey }) {
  const mark = BRAND_MARKS[brand];
  return <svg viewBox={"viewBox" in mark ? mark.viewBox : "0 0 24 24"} aria-hidden="true" focusable="false" fill={mark.ink}><path d={mark.path} /></svg>;
}
