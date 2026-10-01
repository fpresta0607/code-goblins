import { createElement, Fragment, type ReactNode } from "react";

// A question or message as its asker wrote it, with the little structure the
// question contract allows: **bold** for the verdict or the blocking item,
// `code` for a value to enter somewhere, a blank line between paragraphs,
// "- " at the start of a bullet line, and a Markdown pipe table (a header
// row, a separator row of dashes, then its rows) for values that belong
// together, such as DNS records. Everything else is text, so markup in a
// message is shown, never run.
export interface Span { text: string; bold: boolean; code?: true }
export type Block = { kind: "paragraph"; spans: Span[] } | { kind: "list"; items: Span[][] } | { kind: "table"; header: Span[][]; rows: Span[][][] };

// A mark opens only before text and closes only after it, so "5 ** 2" and a
// lone mark stay as written. A code value is taken as written, marks and all.
const BOLD = /\*\*(?=\S)([\s\S]*?\S)\*\*/g;
const CODE = /`([^`\n]+)`/g;
const BULLET = /^- /;
const TABLE_ROW = /^\|.*\|$/;
const TABLE_SEPARATOR = /^\|(\s*:?-{3,}:?\s*\|)+$/;

function boldSpans(text: string): Span[] {
  const out: Span[] = [];
  let at = 0;
  for (const match of text.matchAll(BOLD)) {
    if (match.index > at) out.push({ text: text.slice(at, match.index), bold: false });
    out.push({ text: match[1], bold: true });
    at = match.index + match[0].length;
  }
  if (at < text.length) out.push({ text: text.slice(at), bold: false });
  return out;
}

function spans(text: string): Span[] {
  const out: Span[] = [];
  let at = 0;
  for (const match of text.matchAll(CODE)) {
    if (match.index > at) out.push(...boldSpans(text.slice(at, match.index)));
    out.push({ text: match[1], bold: false, code: true });
    at = match.index + match[0].length;
  }
  if (at < text.length) out.push(...boldSpans(text.slice(at)));
  return out;
}

const cells = (row: string) => row.slice(1, -1).split("|").map((cell) => spans(cell.trim()));

export function messageBlocks(text: string): Block[] {
  const blocks: Block[] = [];
  for (const part of text.replace(/\r\n?/g, "\n").split(/\n\s*\n/)) {
    let prose: string[] = [];
    let items: Span[][] = [];
    const flush = () => {
      if (prose.length) blocks.push({ kind: "paragraph", spans: spans(prose.join("\n")) });
      if (items.length) blocks.push({ kind: "list", items });
      prose = [];
      items = [];
    };
    const lines = part.split("\n").map((raw) => raw.trim()).filter(Boolean);
    for (let n = 0; n < lines.length; n++) {
      const line = lines[n];
      if (TABLE_ROW.test(line) && TABLE_SEPARATOR.test(lines[n + 1] || "")) {
        flush();
        const rows: Span[][][] = [];
        for (n += 2; n < lines.length && TABLE_ROW.test(lines[n]); n++) rows.push(cells(lines[n]));
        n--;
        blocks.push({ kind: "table", header: cells(line), rows });
      } else if (BULLET.test(line)) {
        if (prose.length) { blocks.push({ kind: "paragraph", spans: spans(prose.join("\n")) }); prose = []; }
        items.push(spans(line.slice(2).trim()));
      } else {
        if (items.length) { blocks.push({ kind: "list", items }); items = []; }
        prose.push(line);
      }
    }
    flush();
  }
  return blocks;
}

// renderCode draws a code value, such as with a button that copies it; a
// plain code element otherwise.
export type CodeRenderer = (value: string, key: number) => ReactNode;
const plainCode: CodeRenderer = (value, key) => createElement("code", { key }, value);

const inline = (parts: Span[], code: CodeRenderer) => parts.map((span, n) => span.code ? code(span.text, n) : span.bold ? createElement("strong", { key: n }, span.text) : span.text);

export function messageElements(text: string, code: CodeRenderer = plainCode): ReactNode {
  return createElement(Fragment, null, ...messageBlocks(text).map((block, n) => block.kind === "paragraph"
    ? createElement("p", { key: n }, ...inline(block.spans, code))
    : block.kind === "list"
      ? createElement("ul", { key: n }, ...block.items.map((item, i) => createElement("li", { key: i }, ...inline(item, code))))
      : createElement("div", { key: n, className: "message-table" }, createElement("table", null,
        createElement("thead", null, createElement("tr", null, ...block.header.map((cell, i) => createElement("th", { key: i }, ...inline(cell, code))))),
        createElement("tbody", null, ...block.rows.map((row, r) => createElement("tr", { key: r }, ...row.map((cell, i) => createElement("td", { key: i }, ...inline(cell, code))))))))));
}

// A message's lead, its first line, as inline text for a card's heading, and
// the rest that follows it.
export function leadAndRest(text: string): { lead: string; rest: string } {
  const [lead = "", ...rest] = text.replace(/\r\n?/g, "\n").trim().split("\n");
  return { lead: lead.trim(), rest: rest.join("\n") };
}

export const inlineElements = (text: string, code: CodeRenderer = plainCode): ReactNode => createElement(Fragment, null, ...inline(spans(text), code));

// The message on one line, for a list row: marks dropped, bullets joined, and
// a table left out, since its values only read in the card.
export function plainMessage(text: string): string {
  return messageBlocks(text).flatMap((block) => block.kind === "paragraph"
    ? [block.spans.map((span) => span.text).join("").replace(/\s*\n\s*/g, " ")]
    : block.kind === "list" ? [block.items.map((item) => item.map((span) => span.text).join("")).join("; ")] : []).join(" ");
}
