import { useMemo, useState } from "react";
import type { Feedback, FileDiff } from "./types";
import { parsePatchToRows, splitRows, type DiffRow } from "./diff";
import { CodePreview, SyntaxLine } from "./syntax";

export function DiffView({
  diff,
  onComment,
}: {
  diff: FileDiff;
  onComment: (context: Feedback) => void;
}) {
  const [mode, setMode] = useState("unified");
  const [limit, setLimit] = useState(300);
  const rows = useMemo(() => parsePatchToRows(diff.patch), [diff.patch]);
  const visible = rows.slice(0, limit);
  const comment = (row: DiffRow) => {
    if (row.variant !== "hunk")
      onComment({
        file: diff.path,
        line: (row.variant === "removed" ? row.old : row.next) ?? 1,
        side: row.variant,
        head: diff.head,
        revision: diff.revision,
        diff_id: diff.fingerprint,
      });
  };
  const cell = (row: DiffRow | undefined, side: "left" | "right") =>
    row ? (
      <div className={`split-cell ${row.variant}`}>
        <button
          className="line-number"
          aria-label={`Comment on ${row.variant} line ${side === "left" ? row.old : row.next}`}
          onClick={() => comment(row)}
        >
          {side === "left" ? row.old : row.next}
          <span className="comment-plus">+</span>
        </button>
        <span className="diff-sign" aria-hidden="true">
          {row.variant === "added"
            ? "+"
            : row.variant === "removed"
              ? "-"
              : " "}
        </span>
        <SyntaxLine text={row.text} path={diff.path} />
      </div>
    ) : (
      <div className="split-cell blank" />
    );
  return (
    <section className="diff-view">
      <div className="diff-toolbar">
        <span className="diff-path mono">{diff.path}</span>
        <span className="diff-stats">
          <b>+{rows.filter((row) => row.variant === "added").length}</b>
          <i>−{rows.filter((row) => row.variant === "removed").length}</i>
        </span>
        <div className="segmented" aria-label="Code view">
          {["unified", "split", "code"].map((value) => (
            <button
              key={value}
              aria-pressed={mode === value}
              onClick={() => setMode(value)}
            >
              {value === "code"
                ? "Code"
                : value === "split"
                  ? "Split"
                  : "Unified"}
            </button>
          ))}
        </div>
      </div>
      {diff.binary ? (
        <div className="empty-state">
          Binary file changed. Text preview is unavailable.
        </div>
      ) : mode === "code" ? (
        <CodePreview code={diff.code} path={diff.path} limit={limit} />
      ) : mode === "split" ? (
        <div className="diff-scroll">
          <div className="split-head">
            <span>Before</span>
            <span>After</span>
          </div>
          {splitRows(visible).map((pair) =>
            pair.left?.variant === "hunk" ? (
              <div className="hunk" key={pair.key}>
                {pair.left.text}
              </div>
            ) : (
              <div className="split-row" key={pair.key}>
                {cell(pair.left, "left")}
                {cell(pair.right, "right")}
              </div>
            ),
          )}
        </div>
      ) : (
        <div className="diff-scroll">
          {visible.map((row) =>
            row.variant === "hunk" ? (
              <div className="hunk" key={row.key}>
                {row.text}
              </div>
            ) : (
              <div className={`diff-row ${row.variant}`} key={row.key}>
                <span className="line-number" aria-hidden="true">
                  {row.old}
                </span>
                <button
                  className="line-number"
                  aria-label={`Comment on ${row.variant} line ${row.variant === "removed" ? row.old : row.next}`}
                  onClick={() => comment(row)}
                >
                  {row.next}
                  <span className="comment-plus">+</span>
                </button>
                <span className="diff-sign" aria-hidden="true">
                  {row.variant === "added"
                    ? "+"
                    : row.variant === "removed"
                      ? "-"
                      : " "}
                </span>
                <SyntaxLine text={row.text} path={diff.path} />
              </div>
            ),
          )}
        </div>
      )}
      {!diff.binary &&
        (mode === "code" ? diff.code.split("\n").length : rows.length) >
          limit && (
          <button className="load-more" onClick={() => setLimit(limit + 300)}>
            Show the next 300 lines
          </button>
        )}
      {!diff.binary && rows.length === 0 && mode !== "code" && (
        <p className="muted padded">No text changes in this file.</p>
      )}
      <footer className="diff-footer">
        Select a line number to leave contextual feedback.
        <span className="mono">HEAD {diff.head.slice(0, 8)}</span>
      </footer>
    </section>
  );
}
