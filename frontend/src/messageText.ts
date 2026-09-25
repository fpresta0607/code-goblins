import { createElement, Fragment, type ReactNode } from "react";

// A question or message as its asker wrote it, with the little structure the
// question contract allows: **bold** for the verdict or the blocking item,
// a blank line between paragraphs and "- " at the start of a bullet line.
// Everything else is text, so markup in a message is shown, never run.
export interface Span { text: string; bold: boolean }
export type Block = { kind: "paragraph"; spans: Span[] } | { kind: "list"; items: Span[][] };

// A mark opens only before text and closes only after it, so "5 ** 2" and a
// lone mark stay as written.
const BOLD = /\*\*(?=\S)([\s\S]*?\S)\*\*/g;
const BULLET = /^- /;

function spans(text: string): Span[] {
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
    for (const raw of part.split("\n")) {
      const line = raw.trim();
      if (!line) continue;
      if (BULLET.test(line)) {
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

const inline = (parts: Span[]) => parts.map((span, n) => span.bold ? createElement("strong", { key: n }, span.text) : span.text);

export function messageElements(text: string): ReactNode {
  return createElement(Fragment, null, ...messageBlocks(text).map((block, n) => block.kind === "paragraph"
    ? createElement("p", { key: n }, ...inline(block.spans))
    : createElement("ul", { key: n }, ...block.items.map((item, i) => createElement("li", { key: i }, ...inline(item))))));
}

// The message on one line, for a list row: marks dropped, bullets joined.
export function plainMessage(text: string): string {
  return messageBlocks(text).map((block) => block.kind === "paragraph"
    ? block.spans.map((span) => span.text).join("").replace(/\s*\n\s*/g, " ")
    : block.items.map((item) => item.map((span) => span.text).join("")).join("; ")).join(" ");
}
