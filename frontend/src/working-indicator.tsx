import "./working.css";

// The ordered Bayer 4x4 threshold pattern: each cell lights in this order, so
// the fill shimmers like a dissolving halftone.
const BAYER_4 = [0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5];
const SPEED_SECONDS = 1.1;
// Seven rows of two dots, as the reference's helix.
const ROWS = 7;

// WorkingIndicator is one session's working state in its harness's colour: the
// dither the Overlord chose, sixteen cells lighting in Bayer order, settled
// after a one-time 1.2 s cross-fade from the helix, so the two shapes ease into
// each other rather than cutting. Under reduced motion the morph is gone and
// the dither keeps a calm opacity pulse. It carries an accessible name, and
// compact draws it small enough for a header, a card or a call row.
export function WorkingIndicator({ ink, label = "Working", compact = false }: { ink: string; label?: string; compact?: boolean }) {
  return <span className={"working-indicator" + (compact ? " compact" : "")} role="status" aria-label={label} style={{ color: ink }}>
    <span className="working-helix" aria-hidden="true">
      {Array.from({ length: ROWS }, (_, row) => <span className="working-row" key={row} style={{ top: `${(row / (ROWS - 1)) * 18}px`, animationDelay: `${(row / ROWS) * 1.4}s` }}>
        <i /><i className="b" />
      </span>)}
    </span>
    <span className="working-dither" aria-hidden="true">
      {BAYER_4.map((order, at) => <i key={at} style={{ animationDelay: `${(order / BAYER_4.length) * SPEED_SECONDS}s` }} />)}
    </span>
  </span>;
}
