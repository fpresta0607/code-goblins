import { Fragment, useMemo, useState, type KeyboardEvent, type MouseEvent } from "react";
import type { FileDiff } from "./types";
import { dragRange, isDrag, parsePatchToRows, splitRows, type DiffRow } from "./diff";
import { CodePreview, SyntaxLine } from "./syntax";
import { ReviewComment, type ReviewControls } from "./review";
import { Icon } from "./Icon";

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
  const pairs = mode === "split" ? splitRows(visible) : [];
  const openComment = (view: Element | null) => requestAnimationFrame(() => view?.querySelector<HTMLTextAreaElement>(".comment-overlay textarea")?.focus({ preventScroll: true }));
  // Dragging across diff text opens the comment box for the lines it covers,
  // the same as clicking and Shift-clicking line numbers.
  const dragSelect = (container: HTMLElement) => {
    const text = window.getSelection();
    if (!text || text.isCollapsed || !text.anchorNode) return;
    const covers = Array.from(container.querySelectorAll<HTMLElement>("[data-row]")).filter((row) => text.containsNode(row, true));
    if (!covers.length) return;
    const first = Number(covers[0].dataset.row), last = Number(covers[covers.length - 1].dataset.row);
    const anchor = text.anchorNode instanceof Element ? text.anchorNode : text.anchorNode.parentElement;
    const column = anchor?.closest<HTMLElement>("[data-side]")?.dataset.side;
    const prefer = column === "old" || column === "new" ? column : undefined;
    const covered = mode === "split"
      ? pairs.slice(first, last + 1).flatMap((pair) => { const row = prefer === "old" ? pair.left : pair.right; return row ? [row] : []; })
      : visible.slice(first, last + 1);
    const picked = dragRange(covered, prefer);
    if (!picked) return;
    text.removeAllRanges();
    reviews.select(diff, picked.line, picked.side, false);
    if (picked.end !== picked.line) reviews.select(diff, picked.end, picked.side, true);
    openComment(container.closest(".diff-view"));
  };
  const dragStart = (event: MouseEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    const container = event.currentTarget, detail = event.detail, from = { x: event.clientX, y: event.clientY };
    document.addEventListener("mouseup", (up) => {
      if (isDrag(detail, from, { x: up.clientX, y: up.clientY })) dragSelect(container);
    }, { once: true });
  };
  const comment = (floating: boolean) => <ReviewComment diff={diff} reviews={reviews} connected={connected} floating={floating} />;
  // A zero-height anchor after the selected row lets the comment float over
  // the rows below it instead of pushing them apart.
  const anchored = <div className="comment-anchor">{comment(true)}</div>;
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
      aria-label={"Comment on " + side + " line " + line}
      aria-pressed={!!selection && selection.side === side && selected(row)}
      onClick={(event) => {
        reviews.select(diff, line, side, event.shiftKey);
        openComment(event.currentTarget.closest(".diff-view"));
      }}
      onKeyDown={(event) => lineKey(event, line, side)}>
      {line}<span className="comment-mark" aria-hidden="true" data-tip="Comment on this line" data-tip-align="start"><Icon name="comment" /></span>
    </button>;
  };
  const sign = (row: DiffRow) => <span className="diff-sign" aria-hidden="true">{row.variant === "added" ? "+" : row.variant === "removed" ? "-" : " "}</span>;
  const cell = (row: DiffRow | undefined, side: "old" | "new") => row
    ? <div data-side={side} className={"split-cell " + row.variant + (selection?.side === side && selected(row) ? " selected-line" : "")}>
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
      {mode !== "code" && !diff.binary && <span className="selection-hint">Drag across lines, or click a line number, to comment.</span>}
    </div>
    {diff.binary ? <div className="padded">Binary file changed. Text preview is unavailable.</div> :
      mode === "code" ? <CodePreview code={diff.code} path={diff.path} limit={limit} /> :
        mode === "split" ? <div className="diff-scroll" onMouseDown={dragStart}>
          <div className="split-head"><span>Before</span><span>After</span></div>
          {pairs.map((pair, index) => <Fragment key={pair.key}>
            {pair.left?.variant === "hunk" ? <div className="hunk">{pair.left.text}</div> :
              <div className="split-row" data-row={index}>{cell(pair.left, "old")}{cell(pair.right, "new")}</div>}
            {(selection?.side === "old" ? end(pair.left) : end(pair.right)) && anchored}
          </Fragment>)}
        </div> : <div className="diff-scroll" onMouseDown={dragStart}>
          {visible.map((row, index) => <Fragment key={row.key}>
            {row.variant === "hunk" ? <div className="hunk">{row.text}</div> :
              <div data-row={index} className={"diff-row " + row.variant + (selected(row) ? " selected-line" : "")}>
                {number(row, "old")}{number(row, "new")}{sign(row)}<SyntaxLine text={row.text} path={diff.path} />
              </div>}
            {end(row) && anchored}
          </Fragment>)}
        </div>}
    {!inlineVisible && selection && comment(false)}
    {!diff.binary && (mode === "code" ? diff.code.split("\n").length : rows.length) > limit &&
      <button className="load-more" onClick={() => setLimit(limit + 300)}>Show the next 300 lines</button>}
    {!diff.binary && rows.length === 0 && mode !== "code" && <p className="muted padded">No text changes in this file.</p>}
  </section>;
}
