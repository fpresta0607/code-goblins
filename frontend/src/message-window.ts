// The rows of a long conversation the page holds: those within reach of what
// he reads, and room for the rest above and below, so a session of thousands
// of messages costs the page what a screenful does.

// A row not yet drawn is taken to be this tall until it is measured.
export const ESTIMATED_ROW_PX = 120;
// Rows this far above and below what he sees are drawn too, so a scroll or a
// fling finds them ready.
export const OVERSCAN_PX = 1200;

export interface MessageWindow { start: number; end: number; before: number; after: number }

// messageWindow is which rows to draw, start to end with end excluded, for
// rows of heights, read from top with height in view: every row that reaches
// within overscan of the view, and the heights of the rows left out before
// and after. A top past the end reads the last rows, as a view that follows
// the newest messages does.
export function messageWindow(heights: number[], top: number, height: number, overscan = OVERSCAN_PX): MessageWindow {
  const total = heights.reduce((sum, each) => sum + each, 0);
  const viewTop = Math.max(0, Math.min(top, total - height));
  const from = viewTop - overscan, to = viewTop + height + overscan;
  let start = heights.length, end = 0, before = 0, at = 0;
  for (let index = 0; index < heights.length; index++) {
    const rowTop = at, rowBottom = at + heights[index];
    at = rowBottom;
    if (rowBottom <= from) { before = rowBottom; continue; }
    if (rowTop >= to) break;
    if (index < start) start = index;
    end = index + 1;
  }
  if (start > end) return { start: heights.length, end: heights.length, before: total, after: 0 };
  const drawn = heights.slice(start, end).reduce((sum, each) => sum + each, 0);
  return { start, end, before, after: total - before - drawn };
}
