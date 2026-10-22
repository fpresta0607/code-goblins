import type { Terminal } from "@xterm/xterm";
import { wheelLines } from "./terminalInput";

const PIXEL_MODE = 0;

// terminalText is a terminal's history and screen as plain text, to select
// on a touch screen: each row that wraps on is joined to the next.
export function terminalText(term: Terminal): string {
  const buffer = term.buffer.active;
  let text = "";
  for (let row = 0; row < buffer.length; row++) {
    const next = buffer.getLine(row + 1);
    text += (buffer.getLine(row)?.translateToString(!next?.isWrapped) ?? "") + (next && !next.isWrapped ? "\n" : "");
  }
  return text.trimEnd();
}

// One finger dragged up or down a terminal scrolls it by the lines it moved
// over, as the wheel does, and never moves the page: xterm reads the wheel
// alone, so a phone's swipe fell through to the page under it (the Overlord,
// 2026-10-22, "scroll in terminal on mobile remote doesnt work, it scrolls
// the screen"). scroll is told the lines, down positive as the wheel's are,
// so a finger moving up shows what is further down, and where the finger is.
// Two fingers are the browser's, to zoom. It returns what detaches it.
export function attachTouchScroll(element: HTMLElement, rowHeight: () => number, scroll: (lines: number, at: { clientX: number; clientY: number }) => void): () => void {
  let finger: number | null = null, lastY = 0, rest = 0;
  const start = (event: TouchEvent) => {
    finger = event.touches.length === 1 ? event.touches[0].identifier : null;
    lastY = event.touches[0]?.clientY ?? 0;
    rest = 0;
  };
  const move = (event: TouchEvent) => {
    const touch = event.touches.length === 1 && event.touches[0].identifier === finger ? event.touches[0] : null;
    if (!touch) { finger = null; return; }
    event.preventDefault();
    // A terminal not drawn yet has no rows to count.
    const row = rowHeight();
    if (!(row > 0)) return;
    const { lines, rest: left } = wheelLines(rest, lastY - touch.clientY, PIXEL_MODE, row, 0);
    rest = left;
    lastY = touch.clientY;
    if (lines) scroll(lines, touch);
  };
  const end = (event: TouchEvent) => { if (!event.touches.length) finger = null; };
  const listening = new AbortController();
  const { signal } = listening;
  element.addEventListener("touchstart", start, { passive: true, signal });
  element.addEventListener("touchmove", move, { passive: false, signal });
  element.addEventListener("touchend", end, { signal });
  element.addEventListener("touchcancel", end, { signal });
  return () => listening.abort();
}
