import { Fragment, useMemo, useState, type KeyboardEvent } from "react";
import type { FileDiff } from "./types";
import { parsePatchToRows, splitRows, type DiffRow } from "./diff";
import { CodePreview, SyntaxLine } from "./syntax";
import { ReviewComment, type ReviewControls } from "./review";

export function DiffView({ diff, reviews, connected }: {
  diff: FileDiff;
  reviews: ReviewControls;
  connected: boolean;
}) {
  const [mode, setMode] = useState("unified");
  const [limit, setLimit] = useState(300);
  const rows = useMemo(() => parsePatchToRows(diff.patch), [diff.patch]);
  const visible = rows.slice(0, limit);
  const draft = reviews.drafts[reviews.keyFor(diff)];
  const selection = draft && !draft.hidden ? draft.selection : undefined;
  const selected = (row: DiffRow) => {
    if (!selection) return false;
    const line = selection.side === "old" ? row.old : row.next;
    return line !== null && line >= selection.line && line <= selection.end_line;
  };
  const end = (row?: DiffRow) => row && selection &&
    (selection.side === "old" ? row.old : row.next) === selection.end_line;
  const comment = <ReviewComment diff={diff} reviews={reviews} connected={connected} />;
  const lineKey = (event: KeyboardEvent<HTMLButtonElement>, line: number, side: "old" | "new") => {
    if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
    const candidates = visible.flatMap((row) => {
      const coordinate = side === "old" ? row.old : row.next;
      return coordinate === null ? [] : [coordinate];
    });
    const next = candidates[candidates.indexOf(line) + (event.key === "ArrowUp" ? -1 : 1)];
    if (next === undefined) return;
    event.preventDefault();
    event.currentTarget.closest(".diff-view")?.querySelector<HTMLButtonElement>(
      'button[data-side="' + side + '"][data-line="' + next + '"]',
    )?.focus();
    if (event.shiftKey) reviews.select(diff, next, side, true);
  };
  const number = (row: DiffRow, side: "old" | "new") => {
    const line = side === "old" ? row.old : row.next;
    return line === null ? <span className="line-number" aria-hidden="true" /> : <button
      className="line-number" data-side={side} data-line={line}
      aria-label={"Select " + side + " line " + line}
      aria-pressed={!!selection && selection.side === side && selected(row)}
      onClick={(event) => reviews.select(diff, line, side, event.shiftKey)}
      onKeyDown={(event) => lineKey(event, line, side)}>
      {line}<span className="comment-plus" aria-hidden="true">+</span>
    </button>;
  };
  const sign = (row: DiffRow) => <span className="diff-sign" aria-hidden="true">{row.variant === "added" ? "+" : row.variant === "removed" ? "-" : " "}</span>;
  const cell = (row: DiffRow | undefined, side: "old" | "new") => row
    ? <div className={"split-cell " + row.variant + (selection?.side === side && selected(row) ? " selected-line" : "")}>
      {number(row, side)}{sign(row)}<SyntaxLine text={row.text} path={diff.path} />
    </div> : <div className="split-cell blank" />;
  const inlineVisible = mode !== "code" && visible.some((row) => end(row));
  return <section className="diff-view" aria-label={"Diff for " + diff.path}>
    <div className="diff-toolbar">
      <span className="diff-stats"><b>+{rows.filter((row) => row.variant === "added").length}</b><i>−{rows.filter((row) => row.variant === "removed").length}</i></span>
      <label><span className="sr-only">Diff format for {diff.path}</span>
        <select aria-label={"Diff format for " + diff.path} value={mode} onChange={(event) => setMode(event.target.value)}>
          <option value="unified">Unified</option><option value="split">Split</option><option value="code">Code</option>
        </select>
      </label>
      {mode !== "code" && !diff.binary && <span className="selection-hint">Select a line. Shift-select to extend.</span>}
    </div>
    {diff.binary ? <div className="padded">Binary file changed. Text preview is unavailable.</div> :
      mode === "code" ? <CodePreview code={diff.code} path={diff.path} limit={limit} /> :
        mode === "split" ? <div className="diff-scroll">
          <div className="split-head"><span>Before</span><span>After</span></div>
          {splitRows(visible).map((pair) => <Fragment key={pair.key}>
            {pair.left?.variant === "hunk" ? <div className="hunk">{pair.left.text}</div> :
              <div className="split-row">{cell(pair.left, "old")}{cell(pair.right, "new")}</div>}
            {(selection?.side === "old" ? end(pair.left) : end(pair.right)) && comment}
          </Fragment>)}
        </div> : <div className="diff-scroll">
          {visible.map((row) => <Fragment key={row.key}>
            {row.variant === "hunk" ? <div className="hunk">{row.text}</div> :
              <div className={"diff-row " + row.variant + (selected(row) ? " selected-line" : "")}>
                {number(row, "old")}{number(row, "new")}{sign(row)}<SyntaxLine text={row.text} path={diff.path} />
              </div>}
            {end(row) && comment}
          </Fragment>)}
        </div>}
    {!inlineVisible && selection && comment}
    {!diff.binary && (mode === "code" ? diff.code.split("\n").length : rows.length) > limit &&
      <button className="load-more" onClick={() => setLimit(limit + 300)}>Show the next 300 lines</button>}
    {!diff.binary && rows.length === 0 && mode !== "code" && <p className="muted padded">No text changes in this file.</p>}
  </section>;
}
