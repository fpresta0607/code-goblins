import "./working.css";

// The ordered Bayer 4x4 threshold pattern: each cell's place in it sets how
// brightly it is lit, so the cells read as a halftone.
const BAYER_4 = [0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5];

// WorkingIndicator is one session's working state in its harness's colour: a
// still halftone of sixteen cells, lit in Bayer order, that breathes as one.
// That breath is the one animation Modern Chat runs, of opacity alone, which
// the compositor draws without the page's main thread: the helix and the
// sixteen cells that each animated ran 32 animations, which kept the main
// thread 44 percent busy with nothing happening (the Overlord, 2026-10-28:
// "32 animations??? remvoe like all but the streaming one"). Reduced motion
// keeps it still. It carries an accessible name. It is drawn only in Modern
// Chat's working spot; the board's cards, panel headers and tree rows show a
// plain blue dot for working (the Overlord, 2026-10-27: "can just be blue dot
// as before").
export function WorkingIndicator({ ink, label = "Working" }: { ink: string; label?: string }) {
  return <span className="working-indicator" role="status" aria-label={label} style={{ color: ink }}>
    <span className="working-dither" aria-hidden="true">
      {BAYER_4.map((order, at) => <i key={at} style={{ opacity: .15 + .85 * order / (BAYER_4.length - 1) }} />)}
    </span>
  </span>;
}
