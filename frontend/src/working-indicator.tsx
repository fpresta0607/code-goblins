import { useLayoutEffect, useRef } from "react";
import "./working.css";

// The ordered Bayer 4x4 threshold pattern: each cell lights in this order, so
// the fill shimmers like a dissolving halftone.
const BAYER_4 = [0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5];
const DITHER_MS = 1100;
// Seven rows of two dots, as the reference's helix.
const ROWS = 7;
const HELIX_MS = 1400;
// How long the helix takes to fade into the cells and back.
const MORPH_MS = 8000;
const NEAR = { transform: "translateX(.375em) scale(1)", opacity: 1, easing: "ease-in-out" };
const FAR = { transform: "translateX(-.375em) scale(.5)", opacity: .45, easing: "ease-in-out" };
const SHOWN = { transform: "scale(1)", opacity: 1, easing: "ease-in-out" };
const HIDDEN = { transform: "scale(.78)", opacity: 0, easing: "ease-in-out" };
const morph = (first: Keyframe, second: Keyframe): Keyframe[] => [{ ...first, offset: 0 }, { ...first, offset: .3 }, { ...second, offset: .45 }, { ...second, offset: .85 }, { ...first, offset: 1 }];
const turning = (id: string, duration: number, delay = 0): KeyframeAnimationOptions => ({ id, duration, delay, iterations: Infinity });

// WorkingIndicator is one session's working state in its harness's colour,
// the ink of the harness's own mark: the helix fading smoothly into sixteen
// cells lighting in Bayer order and back (the Overlord, 2026-10-30: "have the
// helix animaoitn please ... make color of the helix match teh harnes icon
// coloring"). Every part of it moves by transform and opacity alone, which
// the compositor draws with no work on the page's main thread, and it runs
// only while a turn runs. It animates by element.animate, not CSS: Chrome
// wakes the main thread at every turn of a CSS animation while anything in
// the page listens for animationiteration, as React's root always does, and
// thirty dots each turning on a phase of its own woke it about fifty times a
// second. Reduced motion keeps the helix still. It carries an accessible
// name. It is drawn only in Modern Chat's working spot; the board's cards,
// panel headers and tree rows show a plain blue dot for working (the
// Overlord, 2026-10-27: "can just be blue dot as before").
export function WorkingIndicator({ ink, label = "Working" }: { ink: string; label?: string }) {
  const helix = useRef<HTMLSpanElement>(null);
  const dither = useRef<HTMLSpanElement>(null);
  useLayoutEffect(() => {
    const [dots, cells] = [helix.current, dither.current];
    if (!dots || !cells || matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const running = [
      dots.animate(morph(SHOWN, HIDDEN), turning("working-helix-morph", MORPH_MS)),
      cells.animate(morph(HIDDEN, SHOWN), turning("working-dither-morph", MORPH_MS)),
      ...[...dots.children].flatMap((row, at) => [...row.children].map((dot, side) =>
        dot.animate(side ? [FAR, NEAR, FAR] : [NEAR, FAR, NEAR], turning(side ? "working-helix-b" : "working-helix-a", HELIX_MS, (at / ROWS) * HELIX_MS)))),
      ...[...cells.children].map((cell, at) => cell.animate([{ opacity: .1 }, { opacity: 1 }, { opacity: .1 }], turning("working-dither", DITHER_MS, (BAYER_4[at] / BAYER_4.length) * DITHER_MS))),
    ];
    return () => running.forEach((animation) => animation.cancel());
  }, []);
  return <span className="working-indicator" role="status" aria-label={label} style={{ color: ink }}>
    <span className="working-helix" aria-hidden="true" ref={helix}>
      {Array.from({ length: ROWS }, (_, row) => <span className="working-helix-row" key={row} style={{ top: `${(row / (ROWS - 1)) * 75}%` }}>
        <i /><i className="b" />
      </span>)}
    </span>
    <span className="working-dither" aria-hidden="true" ref={dither}>
      {BAYER_4.map((_, at) => <i key={at} />)}
    </span>
  </span>;
}
