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
