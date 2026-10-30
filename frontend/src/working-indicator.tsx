import "./working.css";

// The ordered Bayer 4x4 threshold pattern: each cell lights in this order, so
// the fill shimmers like a dissolving halftone.
const BAYER_4 = [0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5];
const SPEED_SECONDS = 1.1;
// Seven rows of two dots, as the reference's helix.
const ROWS = 7;

// WorkingIndicator is one session's working state in its harness's colour,
// the ink of the harness's own mark: the helix fading smoothly into sixteen
// cells lighting in Bayer order and back (the Overlord, 2026-10-30: "have the
// helix animaoitn please ... make color of the helix match teh harnes icon
// coloring"). Every part of it moves by transform and opacity alone, which
// the compositor draws with no work on the page's main thread, and it runs
// only while a turn runs. Reduced motion keeps the helix still. It carries an
// accessible name. It is drawn only in Modern Chat's working spot; the
// board's cards, panel headers and tree rows show a plain blue dot for
// working (the Overlord, 2026-10-27: "can just be blue dot as before").
export function WorkingIndicator({ ink, label = "Working" }: { ink: string; label?: string }) {
  return <span className="working-indicator" role="status" aria-label={label} style={{ color: ink }}>
    <span className="working-helix" aria-hidden="true">
      {Array.from({ length: ROWS }, (_, row) => <span className="working-helix-row" key={row} style={{ top: `${(row / (ROWS - 1)) * 75}%`, animationDelay: `${(row / ROWS) * 1.4}s` }}>
        <i /><i className="b" />
      </span>)}
    </span>
    <span className="working-dither" aria-hidden="true">
      {BAYER_4.map((order, at) => <i key={at} style={{ animationDelay: `${(order / BAYER_4.length) * SPEED_SECONDS}s` }} />)}
    </span>
  </span>;
}
