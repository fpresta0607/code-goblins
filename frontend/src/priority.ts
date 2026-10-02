// A ranked list's order is its priority, top first: Tasks is the order the
// CFO starts queued work in and In progress the order it attends to goblins.
// These are the pure steps of moving a card; the supervisor saves the result.

export function moveTo(ids: string[], id: string, index: number): string[] {
  const from = ids.indexOf(id);
  if (from < 0) return ids;
  const rest = ids.filter((other) => other !== id);
  return [...rest.slice(0, index), id, ...rest.slice(index)];
}

// Alt with Up or Down moves the focused card one place; null means it is
// already at that end.
export function stepped(ids: string[], id: string, step: -1 | 1): string[] | null {
  const from = ids.indexOf(id);
  const to = from + step;
  if (from < 0 || to < 0 || to >= ids.length) return null;
  return moveTo(ids, id, to);
}

export interface Box { left: number; top: number; width: number; height: number }

// Where a dragged card lands among the others, given their boxes in list
// order, the pointer in the same frame and how many columns the list lays its
// cards out in. One column reads top to bottom; a grid reads row by row, so a
// card lands before the first card in a later row, or in its own row, right
// of the pointer.
export function dropIndex(boxes: Box[], x: number, y: number, columns: number): number {
  const before = boxes.findIndex((box) => columns > 1
    ? y < box.top || y <= box.top + box.height && x < box.left + box.width / 2
    : y < box.top + box.height / 2);
  return before < 0 ? boxes.length : before;
}

// The room at the top and bottom edge of what scrolls a list in which a
// dragged card scrolls it, and the most it scrolls in a frame.
const EDGE = 64;
const FASTEST = 20;

// How far a dragged card scrolls its list in a frame, given the pointer and
// the top and bottom of what scrolls the list, as the screen shows them:
// nothing while the pointer is clear of both edges, and more the nearer it is
// to one, or past it. Up is negative. So a card reaches any place in a list
// longer than the screen.
export function edgeScroll(y: number, top: number, bottom: number): number {
  const past = y < top + EDGE ? y - top - EDGE : y > bottom - EDGE ? y - bottom + EDGE : 0;
  return Math.sign(past) * Math.ceil(Math.min(1, Math.abs(past) / EDGE) * FASTEST);
}

// The list as the Overlord just dropped it, while the supervisor saves it; a
// card the drop did not know about keeps its place after the dropped ones.
export function orderShown<T extends { id: string }>(items: T[], pending: string[] | null): T[] {
  if (!pending) return items;
  const rank = (item: T) => { const at = pending.indexOf(item.id); return at < 0 ? pending.length : at; };
  return [...items].sort((a, b) => rank(a) - rank(b));
}

// A dropped order stops overriding the list once the supervisor's list agrees
// with it, or once cards came or went, when the supervisor's list wins.
export function pendingSettled(listed: string[], pending: string[]): boolean {
  if (listed.length !== pending.length || listed.some((id) => !pending.includes(id))) return true;
  return listed.every((id, index) => pending[index] === id);
}

export function rankLabel(index: number, count: number): string {
  return `priority ${index + 1} of ${count}`;
}
