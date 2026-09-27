// A list that can grow long shows as many cards as fit the space it has, one
// page at a time, with a pager and a sideways swipe for the rest, so the
// board never becomes a long scroll. These are the pure steps of paging.

// How many cards a page holds: the rows of unit-high cards, gap apart, that
// fit in available pixels, times the grid's columns; at least one.
export function pageSizeFor(available: number, unit: number, gap: number, columns: number): number {
  if (unit <= 0) return 1;
  const rows = Math.max(1, Math.floor((available + gap) / (unit + gap)));
  return rows * Math.max(1, columns);
}

// The card height a page is sized by: the tallest card seen at this list
// width, so turning to shorter cards never grows the page back and the size
// settles instead of flipping; a new width measures again.
export function tallestCard(prior: { width: number; unit: number }, width: number, heights: number[]): { width: number; unit: number } {
  return { width, unit: Math.max(width === prior.width ? prior.unit : 0, ...heights) };
}

// The height a list may fill: with the columns side by side, the visible
// canvas below where the list starts; with them stacked, one screen below its
// column's heading, so a column far down the page still shows a screenful.
// reserve keeps room for the pager and the padding under the list.
export function availableHeight({ view, top, heading, stacked, reserve }: { view: number; top: number; heading: number; stacked: boolean; reserve: number }): number {
  return Math.max(0, (stacked ? view - heading : view - top) - reserve);
}

export function clampPage(page: number, size: number, count: number): number {
  const pages = Math.max(1, Math.ceil(count / size));
  return Math.min(Math.max(0, page), pages - 1);
}

// Which cards the page shows, such as 1–5 of 18; empty when one page holds
// the whole list.
export function pageLabel(page: number, size: number, count: number): string {
  if (count <= size) return "";
  const start = page * size;
  return `${start + 1}–${Math.min(count, start + size)} of ${count}`;
}

// A sideways swipe of a finger: -1 for the earlier cards, 1 for the next, 0
// for anything shorter or more vertical, which scrolls.
export function swipeStep(dx: number, dy: number): -1 | 0 | 1 {
  if (Math.abs(dx) < 48 || Math.abs(dx) < 1.5 * Math.abs(dy)) return 0;
  return dx < 0 ? 1 : -1;
}
