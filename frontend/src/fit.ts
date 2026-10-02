// A paged list past ten cards shows as many as fit the space it has, and never
// fewer than five, one page at a time, with a pager and a sideways swipe for
// the rest. These are the pure steps of paging.

// Up to this many cards a list shows whole, with no pager, and the board
// scrolls when they run past the screen.
const PAGING_STARTS_PAST = 10;

// A page holds at least this many cards while the list has them, however
// little room there is, and the board scrolls: on 2026-10-02 the memory meter
// and tall cards left a page of Tasks room for two of its 19.
const PAGE_HOLDS_AT_LEAST = 5;

// Where each page of a list starts: one page for up to PAGING_STARTS_PAST
// cards however tall, and past that the pages pageStarts fills.
export function listPageStarts(heights: number[], available: number, gap: number, columns: number): number[] {
  return heights.length > PAGING_STARTS_PAST ? pageStarts(heights, available, gap, columns) : [0];
}

// Where each page starts: from the first card on, a page takes rows of cards,
// gap apart and each as tall as its tallest card, while they fit in available
// pixels, and the next page starts at the first row that does not. A page
// holds at least PAGE_HOLDS_AT_LEAST cards whether or not they fit. Each page
// is sized by its own cards, and every start follows from the cards before
// it, so the pages never depend on which one is shown.
export function pageStarts(heights: number[], available: number, gap: number, columns: number): number[] {
  const perRow = Math.max(1, columns);
  const starts = [0];
  let used = 0;
  for (let at = 0; at < heights.length; at += perRow) {
    const row = Math.max(...heights.slice(at, at + perRow));
    const held = at - starts[starts.length - 1];
    if (held >= PAGE_HOLDS_AT_LEAST && used + gap + row > available) {
      starts.push(at);
      used = row;
    } else {
      used = held > 0 ? used + gap + row : row;
    }
  }
  return starts;
}

// The height a list may fill: with the columns side by side, the visible
// canvas below where the list starts; with them stacked, one screen below its
// column's heading, so a column far down the page still shows a screenful.
// reserve keeps room for the pager and the padding under the list.
export function availableHeight({ view, top, heading, stacked, reserve }: { view: number; top: number; heading: number; stacked: boolean; reserve: number }): number {
  return Math.max(0, (stacked ? view - heading : view - top) - reserve);
}

// The key a ranked card's height is kept by: the first card renders
// differently from the rest (the top queued card carries the Next chip), so a
// card moved to or from the top is measured again in its new place.
export function fitKey(id: string, index: number): string {
  return index === 0 ? `${id}#first` : id;
}

export function clampPage(page: number, pages: number): number {
  return Math.min(Math.max(0, page), Math.max(1, pages) - 1);
}

// The page a card at index is on, so a card moved past its page's edge
// carries the view with it.
export function pageOf(index: number, starts: number[]): number {
  let page = 0;
  while (page + 1 < starts.length && starts[page + 1] <= index) page++;
  return page;
}

// The page to show while the list keeps the card at index in view, so the
// page follows that card as heights are measured and the order changes; once
// the card has left the list (index -1), the last page shown, clamped to the
// pages there are.
export function pageShowing(index: number, last: number, starts: number[]): number {
  return index >= 0 ? pageOf(index, starts) : clampPage(last, starts.length);
}

// Which cards the page shows, such as 1–5 of 18; empty when one page holds
// the whole list.
export function pageLabel(start: number, end: number, count: number): string {
  if (start === 0 && end >= count) return "";
  return `${start + 1}–${end} of ${count}`;
}

// A sideways swipe of a finger: -1 for the earlier cards, 1 for the next, 0
// for anything shorter or more vertical, which scrolls.
export function swipeStep(dx: number, dy: number): -1 | 0 | 1 {
  if (Math.abs(dx) < 48 || Math.abs(dx) < 1.5 * Math.abs(dy)) return 0;
  return dx < 0 ? 1 : -1;
}
