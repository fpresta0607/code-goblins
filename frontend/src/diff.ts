// Adapted from Cline Kanban shared/diff-renderer.tsx (parsePatchToRows).
// Copyright 2026 Cline Bot Inc. Apache-2.0. See public/assets/NOTICE.txt.
// CFO changes: preserve both line coordinates and hunk headers, parse Go's
// unified patch directly, and align removed/added runs for split rendering.
export interface DiffRow {
  key: string;
  variant: "added" | "removed" | "context" | "hunk";
  old: number | null;
  next: number | null;
  text: string;
}

export function reviewRange(rows: DiffRow[], first: number, last: number, side: "old" | "new"): boolean {
  if (first < 1 || last < first || last - first >= 200) return false;
  const selected = rows.filter((row) => {
    const line = side === "old" ? row.old : row.next;
    return line !== null && line >= first && line <= last;
  });
  return selected.length === last - first + 1 && selected.every((row, index) => (side === "old" ? row.old : row.next) === first + index);
}
// A drag across diff rows comments on the lines it covers: on the new side
// when any covered row has a new line, otherwise on the old side. In split
// view the column the drag began in is preferred.
export function dragRange(rows: DiffRow[], prefer?: "old" | "new"): { side: "old" | "new"; line: number; end: number } | null {
  const order: ("old" | "new")[] = prefer === "old" ? ["old", "new"] : ["new", "old"];
  for (const side of order) {
    const lines = rows.flatMap((row) => { const line = side === "old" ? row.old : row.next; return line === null ? [] : [line]; });
    if (lines.length) return { side, line: Math.min(...lines), end: Math.max(...lines) };
  }
  return null;
}
// Only a single-click press that moved more than 4 px is a drag, so a click,
// double-click or triple-click keeps its text selection for copying.
export function isDrag(detail: number, from: { x: number; y: number }, to: { x: number; y: number }) {
  return detail === 1 && Math.hypot(to.x - from.x, to.y - from.y) > 4;
}
export function parsePatchToRows(patch: string): DiffRow[] {
  const rows: DiffRow[] = [];
  let old = 0,
    next = 0,
    inHunk = false;
  for (const [index, line] of patch.split("\n").entries()) {
    const match = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line);
    if (match) {
      old = Number(match[1]);
      next = Number(match[2]);
      inHunk = true;
      rows.push({
        key: `h-${index}`,
        variant: "hunk",
        old: null,
        next: null,
        text: line,
      });
      continue;
    }
    if (!inHunk || !line || line.startsWith("\\")) continue;
    if (line[0] === "+")
      rows.push({
        key: `a-${index}`,
        variant: "added",
        old: null,
        next: next++,
        text: line.slice(1),
      });
    else if (line[0] === "-")
      rows.push({
        key: `r-${index}`,
        variant: "removed",
        old: old++,
        next: null,
        text: line.slice(1),
      });
    else if (line[0] === " ")
      rows.push({
        key: `c-${index}`,
        variant: "context",
        old: old++,
        next: next++,
        text: line.slice(1),
      });
  }
  return rows;
}
export function splitRows(
  rows: DiffRow[],
): { key: string; left?: DiffRow; right?: DiffRow }[] {
  const result: { key: string; left?: DiffRow; right?: DiffRow }[] = [];
  for (let i = 0; i < rows.length; ) {
    const row = rows[i];
    if (row.variant === "removed" || row.variant === "added") {
      const removed: DiffRow[] = [],
        added: DiffRow[] = [];
      while (i < rows.length && rows[i].variant === "removed")
        removed.push(rows[i++]);
      while (i < rows.length && rows[i].variant === "added")
        added.push(rows[i++]);
      for (let n = 0; n < Math.max(removed.length, added.length); n++)
        result.push({
          key: `${row.key}-${n}`,
          left: removed[n],
          right: added[n],
        });
    } else {
      result.push({ key: row.key, left: row, right: row });
      i++;
    }
  }
  return result;
}
