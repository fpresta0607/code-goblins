import { createElement, Fragment, type MouseEvent, type ReactNode } from "react";
import { boardPlace, openBoardAddress } from "./board-address.ts";
import { documentLinks, type DocumentLink } from "./document-links.ts";
import { scrawlLink } from "./scrawl-link.ts";
import type { SaidLink } from "./task-words.ts";

// A question or message as its asker wrote it, with the little structure the
// question contract allows: **bold** for the verdict or the blocking item,
// `code` for a value to enter somewhere, a blank line between paragraphs,
// "- " at the start of a bullet line, and a Markdown pipe table (a header
// row, a separator row of dashes, then its rows) for values that belong
// together, such as DNS records. An http or https address is a link (the
// Overlord, 2026-10-17: "I should be able to click it as a hyperlink to
// navigate to it"), and a Markdown link to one reads as its label.
// Everything else is text, so markup in a message is shown, never run.
// A link span's text is what it reads and link is the address it opens.
export interface Span { text: string; bold: boolean; code?: true; link?: string; italic?: true }
export type Block = { kind: "paragraph"; spans: Span[] } | { kind: "list"; items: Span[][]; start?: number } | { kind: "table"; header: Span[][]; rows: Span[][][] };

// A mark opens only before text and closes only after it, so "5 ** 2" and a
// lone mark stay as written. A code value is taken as written, marks and all,
// and bold may hold one.
const BOLD = /\*\*(?=\S)([\s\S]*?\S)\*\*/g;
const ITALIC = /(?<!\*)\*(?!\*)(?=\S)([^*]*?\S)\*(?!\*)/g;
const CODE = /`([^`\n]+)`/g;
const BULLET = /^(?:[-+*]|(\d+)[.)])\s+/;
const TABLE_ROW = /^\|.*\|$/;
const TABLE_SEPARATOR = /^\|(\s*:?-{3,}:?\s*\|)+$/;
// An address ends at a space or a quote, and the punctuation that ends its
// sentence after it is not its own.
const WEB_LINK = /\bhttps?:\/\/[^\s<>"'`]*[^\s<>"'`.,;:!?)\]}]/g;
// A Markdown link whose target is an http or https address, with parentheses
// in the address only in balanced pairs.
const MARKDOWN_WEB_LINK = /(?<!!)\[([^\]\n]+)\]\((https?:\/\/[^\s<>"'`()]+(?:\([^\s<>"'`()]*\)[^\s<>"'`()]*)*)\)/g;

export const isWebLink = (value: string) => /^https?:\/\/[^\s<>"'`]+$/.test(value);

// webAddresses finds each http or https address in plain text, such as a
// terminal's line, by where it starts and ends.
export const webAddresses = (text: string) => [...text.matchAll(WEB_LINK)].map((match) => ({ start: match.index, end: match.index + match[0].length, address: match[0] }));

// A pull request, issue or CI run on GitHub, any page of it.
const GITHUB_ITEM = /^https:\/\/github\.com\/[^/\s]+\/([^/\s]+)\/(pull|issues|actions\/runs)\/(\d+)(?:[/?#]\S*)?$/;
const GITHUB_ITEM_WORDS: Record<string, string> = { pull: " PR #", issues: " issue #", "actions/runs": " CI run " };
// A review page's session on this PC, as the review tool writes its address.
const REVIEW_SESSION = /^http:\/\/(?:127\.0\.0\.1|localhost|\[::1\]):\d+\/session\/[^/?#\s]+(?:[?#]\S*)?$/;

// webLinkName is what a link to an address reads: a pull request, issue or
// CI run by its short name, such as "code-goblins PR #2358", a review page's
// session as Review page, and any other address as written (the Overlord,
// 2026-11-11: "Should be clickable links or buttons"). Each is known by its
// exact host and path, so a look-alike host keeps its own words.
export function webLinkName(address: string): string {
  const match = GITHUB_ITEM.exec(address);
  if (match) return match[1] + GITHUB_ITEM_WORDS[match[2]] + match[3];
  return REVIEW_SESSION.test(address) ? "Review page" : address;
}

// webLink opens an address in a new tab, which the desktop window hands to
// his default browser, so the board is never navigated away. A phone
// validates a Scrawl session's private route; the shown address stays as the
// asker wrote it. An address the board knows reads as a chip with its short
// name, and a link shown by a name holds the whole address in its tip. A board
// address opens its place in this page instead, with no reload (#2363).
// drawn draws the link's words where its place has a way of its own, as
// terminal text gives a long address the breaks it may wrap at.
export function webLink(address: string, key: number, label?: string, drawn: (name: string) => ReactNode = (name) => name): ReactNode {
  const name = label ?? webLinkName(address);
  const isChip = label === undefined && name !== address;
  const tip = name !== address ? { title: address } : {};
  if (typeof location !== "undefined" && boardPlace(address, location.origin)) {
    return createElement("a", { key, className: "message-link", href: address, onClick: (event: MouseEvent) => { event.preventDefault(); openBoardAddress(address); }, ...tip }, drawn(name));
  }
  return createElement("a", { key, className: isChip ? "message-link message-chip" : "message-link", href: scrawlLink(address), target: "_blank", rel: "noreferrer", ...tip }, drawn(name));
}

// A document link that starts inside a web address is part of the address.
function documentsOutsideWebLinks(text: string): DocumentLink[] {
  const webLinks = [...text.matchAll(WEB_LINK)];
  return documentLinks(text).filter((link) => !webLinks.some((web) => web.index < link.start && web.index + web[0].length > link.start));
}

function coverDocuments(text: string, hasDocuments: boolean): string {
  if (!hasDocuments) return text;
  let covered = text;
  for (const link of documentsOutsideWebLinks(text)) covered = covered.slice(0, link.start) + "~".repeat(link.end - link.start) + covered.slice(link.end);
  return covered;
}

// Text with each address in it a link span of its own. A Markdown link to an
// address is one span that reads as its label.
function linkSpans(text: string, bold: boolean, hasDocuments = false): Span[] {
  const covered = coverDocuments(text, hasDocuments);
  const found = [...covered.matchAll(MARKDOWN_WEB_LINK)].map((match) => ({ start: match.index, end: match.index + match[0].length, label: text.slice(match.index + 1, match.index + 1 + match[1].length), address: match[2] }));
  for (const match of covered.matchAll(WEB_LINK)) {
    if (!found.some((link) => match.index >= link.start && match.index < link.end)) found.push({ start: match.index, end: match.index + match[0].length, label: match[0], address: match[0] });
  }
  found.sort((a, b) => a.start - b.start);
  const out: Span[] = [];
  let at = 0;
  for (const link of found) {
    if (link.start > at) out.push({ text: text.slice(at, link.start), bold });
    out.push({ text: link.label, bold, link: link.address });
    at = link.end;
  }
  if (at < text.length) out.push({ text: text.slice(at), bold });
  return out;
}

function spans(text: string, hasDocuments = false): Span[] {
  // A code value inside a document link belongs to its label, so the link
  // stays whole for its card, while a link inside a code value stays code.
  const documents = hasDocuments ? documentsOutsideWebLinks(text) : [];
  const codes = [...text.matchAll(CODE)].filter((code) => !documents.some((link) => link.start < code.index && code.index + code[0].length <= link.end));
  const out: Span[] = [];
  const add = (from: number, to: number, bold: boolean) => {
    for (const code of codes) {
      const end = code.index + code[0].length;
      if (end <= from || code.index >= to) continue;
      if (code.index > from) out.push(...linkSpans(text.slice(from, code.index), bold, hasDocuments));
      out.push({ text: code[1], bold, code: true });
      from = end;
    }
    if (from < to) out.push(...linkSpans(text.slice(from, to), bold, hasDocuments));
  };
  // Bold is looked for with each code value covered, so marks inside a value
  // stay its own and a bold phrase can run across one.
  const covered = coverDocuments(text.replace(CODE, (value) => "x".repeat(value.length)), hasDocuments);
  let at = 0;
  for (const match of covered.matchAll(BOLD)) {
    add(at, match.index, false);
    add(match.index + 2, match.index + match[0].length - 2, true);
    at = match.index + match[0].length;
  }
  add(at, text.length, false);
  return out.flatMap((span) => {
    if (span.code || span.link) return [span];
    const parts: Span[] = [];
    let from = 0;
    for (const match of coverDocuments(span.text, hasDocuments).matchAll(ITALIC)) {
      if (match.index > from) parts.push({ ...span, text: span.text.slice(from, match.index) });
      parts.push({ ...span, text: span.text.slice(match.index + 1, match.index + match[0].length - 1), italic: true });
      from = match.index + match[0].length;
    }
    if (from < span.text.length) parts.push({ ...span, text: span.text.slice(from) });
    return parts;
  });
}

const cells = (row: string, hasDocuments: boolean) => row.slice(1, -1).split("|").map((cell) => spans(cell.trim(), hasDocuments));

export function messageBlocks(text: string, hasDocuments = false): Block[] {
  const blocks: Block[] = [];
  for (const part of text.replace(/\r\n?/g, "\n").split(/\n\s*\n/)) {
    let prose: string[] = [];
    let items: Span[][] = [];
    let start: number | undefined;
    const flush = () => {
      if (prose.length) blocks.push({ kind: "paragraph", spans: spans(prose.join("\n"), hasDocuments) });
      if (items.length) blocks.push({ kind: "list", items, ...(start !== undefined ? { start } : {}) });
      prose = [];
      items = [];
    };
    const lines = part.split("\n").map((raw) => raw.trim()).filter(Boolean);
    for (let n = 0; n < lines.length; n++) {
      const line = lines[n];
      const bullet = BULLET.exec(line);
      if (TABLE_ROW.test(line) && TABLE_SEPARATOR.test(lines[n + 1] || "")) {
        flush();
        const rows: Span[][][] = [];
        for (n += 2; n < lines.length && TABLE_ROW.test(lines[n]); n++) rows.push(cells(lines[n], hasDocuments));
        n--;
        blocks.push({ kind: "table", header: cells(line, hasDocuments), rows });
      } else if (bullet) {
        const isOrdered = bullet[1] !== undefined;
        if (prose.length || items.length && isOrdered !== (start !== undefined)) flush();
        if (!items.length) start = isOrdered ? Number(bullet[1]) : undefined;
        items.push(spans(line.slice(bullet[0].length).trim(), hasDocuments));
      } else {
        if (items.length) flush();
        prose.push(line);
      }
    }
    flush();
  }
  return blocks;
}

// renderCode draws a code value, such as with a button that copies it; a
// plain code element otherwise, or a link for an address.
export type CodeRenderer = (value: string, key: number) => ReactNode;
const plainCode: CodeRenderer = (value, key) => isWebLink(value) ? webLink(value, key) : createElement("code", { key }, value);

// renderPath draws a local file or folder path the text names, such as a
// link that opens it in File Explorer; as an icon alone beside a code value,
// which keeps its own button that copies it. drawn draws the path's words
// where its place has a way of its own, as terminal text gives a long path
// the breaks it may wrap at.
export type PathRenderer = (path: string, key: number, isIcon?: boolean, drawn?: (path: string) => ReactNode) => ReactNode;
export type DocumentRenderer = (label: string, target: string, key: number) => ReactNode;

function documentElements(text: string, document: DocumentRenderer): ReactNode[] {
  const out: ReactNode[] = [];
  let at = 0;
  for (const link of documentLinks(text)) {
    if (link.start < at) continue;
    if (link.start > at) out.push(text.slice(at, link.start));
    out.push(document(spans(link.label).map((span) => span.text).join(""), link.target, out.length));
    at = link.end;
  }
  if (at < text.length) out.push(text.slice(at));
  return out;
}

// A local file or folder path, found by the rules the supervisor opens one by
// (internal/supervisor/card_paths.go): a drive letter, a colon and a
// separator, then everything up to a space or a character no Windows path
// holds, without the punctuation that ends a sentence (a closing bracket only
// when the path opened none it closes); or a whole code value that is such a
// path, spaces and all. A space is what Go's \s is, so both sides cut a path
// in the same place.
const TEXT_PATH = /(?<![\w\\/])[A-Za-z]:[\\/][^ \t\n\f\r<>"|?*:`]*/g;
const CODE_PATH = /^[A-Za-z]:[\\/][^<>"|?*:`\n]*$/;
const PATH_END = ".,;!?)]}'";

// trimPathEnd drops the punctuation that ends a sentence from a path found in
// text, keeping a closing parenthesis the path itself opened.
function trimPathEnd(path: string): string {
  const count = (text: string, mark: string) => text.split(mark).length - 1;
  while (path && PATH_END.includes(path.at(-1) ?? "") && !(path.endsWith(")") && count(path, "(") >= count(path, ")"))) path = path.slice(0, -1);
  return path;
}

// drivePaths finds each local path text names, as the supervisor finds it,
// by where it starts and ends: in a card's words and in a terminal's line
// alike, so the board finds a path one way.
export const drivePaths = (text: string) => [...text.matchAll(TEXT_PATH)].map((match) => {
  const path = trimPathEnd(match[0]);
  return { start: match.index, end: match.index + path.length, path };
});

// Text as it reads, with each local path it names drawn by path, its words
// by drawn where given.
export function pathElements(text: string, path: PathRenderer, drawn?: (path: string) => ReactNode): ReactNode[] {
  const out: ReactNode[] = [];
  let at = 0;
  for (const found of drivePaths(text)) {
    if (found.start > at) out.push(text.slice(at, found.start));
    out.push(path(found.path, out.length, false, drawn));
    at = found.end;
  }
  if (at < text.length) out.push(text.slice(at));
  return out;
}

// Neighbouring bold spans, such as the words around a code value, are one
// bold phrase. With path, each local path is drawn by it; a code value that
// is one keeps its own button, which copies it, with the path's icon beside
// it, since a command can start the way a path does.
function inline(parts: Span[], code: CodeRenderer, path?: PathRenderer, document?: DocumentRenderer): ReactNode[] {
  const out: ReactNode[] = [];
  const content = (at: number) => parts[at].code
    ? path && CODE_PATH.test(parts[at].text) ? createElement(Fragment, { key: at }, code(parts[at].text, 0), path(parts[at].text, 1, true)) : code(parts[at].text, at)
    : parts[at].link ? webLink(parts[at].link, at, parts[at].text === parts[at].link ? undefined : parts[at].text)
    : document ? createElement(Fragment, { key: at }, ...documentElements(parts[at].text, document))
    : path ? createElement(Fragment, { key: at }, ...pathElements(parts[at].text, path)) : parts[at].text;
  const piece = (at: number) => parts[at].italic ? createElement("em", { key: at }, content(at)) : content(at);
  for (let n = 0; n < parts.length; n++) {
    if (!parts[n].bold) { out.push(piece(n)); continue; }
    const start = n;
    const held: ReactNode[] = [];
    for (; n < parts.length && parts[n].bold; n++) held.push(piece(n));
    n--;
    out.push(createElement("strong", { key: start }, ...held));
  }
  return out;
}

export function messageElements(text: string, code: CodeRenderer = plainCode, path?: PathRenderer, document?: DocumentRenderer): ReactNode {
  return createElement(Fragment, null, ...messageBlocks(text, document !== undefined).map((block, n) => block.kind === "paragraph"
    ? createElement("p", { key: n }, ...inline(block.spans, code, path, document))
    : block.kind === "list"
      ? createElement(block.start === undefined ? "ul" : "ol", { key: n, start: block.start }, ...block.items.map((item, i) => createElement("li", { key: i }, ...inline(item, code, path, document))))
      : createElement("div", { key: n, className: "message-table" }, createElement("table", null,
        createElement("thead", null, createElement("tr", null, ...block.header.map((cell, i) => createElement("th", { key: i }, ...inline(cell, code, path, document))))),
        createElement("tbody", null, ...block.rows.map((row, r) => createElement("tr", { key: r }, ...row.map((cell, i) => createElement("td", { key: i }, ...inline(cell, code, path, document))))))))));
}

// A message's lead, its first line, as inline text for a card's heading, and
// the rest that follows it. A message that opens with a table row has no
// lead: all of it is the rest, so its table stays whole.
export function leadAndRest(text: string): { lead: string; rest: string } {
  const lines = text.replace(/\r\n?/g, "\n").trim().split("\n");
  const lead = lines[0].trim();
  return TABLE_ROW.test(lead) ? { lead: "", rest: lines.join("\n") } : { lead, rest: lines.slice(1).join("\n") };
}

// A question's decision and the evidence its asker put after a line that
// reads only "Details:", which a card folds away so he reads the decision
// first. Trailing spaces or tabs on that line, which a PowerShell `n break
// can leave, still count. The stored text stays whole. A text without that
// line, or with nothing before or after it, is all decision, exactly as written.
const DETAILS_LINE = /^Details:[ \t]*$/m;

export function decisionAndDetails(text: string): { decision: string; details: string } {
  const lines = text.replace(/\r\n?/g, "\n");
  const marker = DETAILS_LINE.exec(lines);
  const decision = marker ? lines.slice(0, marker.index).trim() : "";
  const details = marker ? lines.slice(marker.index + marker[0].length).trim() : "";
  return decision && details ? { decision, details } : { decision: text, details: "" };
}

export const inlineElements = (text: string, code: CodeRenderer = plainCode, path?: PathRenderer, document?: DocumentRenderer): ReactNode => createElement(Fragment, null, ...inline(spans(text, document !== undefined), code, path, document));

// Plain text, such as a History row's line, with each address in it a link.
export const linkedText = (text: string): ReactNode => createElement(Fragment, null, ...inline(linkSpans(text, false), plainCode));

// Text exactly as written, such as a code block or a command's output, with
// each address in it a link that reads as written.
export function literalLinks(text: string): ReactNode {
  const out: ReactNode[] = [];
  let at = 0;
  for (const { start, end, address } of webAddresses(text)) {
    if (start > at) out.push(text.slice(at, start));
    out.push(webLink(address, out.length, address));
    at = end;
  }
  if (at < text.length) out.push(text.slice(at));
  return createElement(Fragment, null, ...out);
}

// Where a link's words stand whole in a sentence, from at on: never inside a
// longer word, number or host name, such as PR #12 in PR #123.
function wholeWords(lower: string, label: string, at: number): number {
  for (let index = lower.indexOf(label, at); index >= 0; index = lower.indexOf(label, index + 1)) {
    if (!/[\w.-]/.test(lower[index - 1] ?? "") && !/^(?:[\w-]|\.\w)/.test(lower.slice(index + label.length, index + label.length + 2))) return index;
  }
  return -1;
}

// A sentence the board wrote from a report, with the words it says each of
// the report's addresses by, such as "PR #12", a link to that address, in
// the order the report names them. Words a summary cut stay unlinked.
export function linkedSentence(sentence: string, links: SaidLink[]): ReactNode {
  const out: ReactNode[] = [];
  const lower = sentence.toLowerCase();
  let at = 0;
  for (const link of links) {
    const index = wholeWords(lower, link.label.toLowerCase(), at);
    if (index < 0) continue;
    if (index > at) out.push(sentence.slice(at, index));
    out.push(webLink(link.address, out.length, sentence.slice(index, index + link.label.length)));
    at = index + link.label.length;
  }
  if (at < sentence.length) out.push(sentence.slice(at));
  return createElement(Fragment, null, ...out);
}

// The message on one line, for a list row: marks dropped, each bullet a
// sentence of its own, and a table left out, since its values only read in
// the card. A Markdown link stays as written, so the History row that links
// its addresses reads it by its label and still opens its address.
export function plainMessage(text: string): string {
  const sentence = (item: string) => item.charAt(0).toUpperCase() + item.slice(1) + (/[.!?:]$/.test(item) ? "" : ".");
  const written = (span: Span) => span.link && span.text !== span.link ? "[" + span.text + "](" + span.link + ")" : span.text;
  return messageBlocks(text).flatMap((block) => block.kind === "paragraph"
    ? [block.spans.map(written).join("").replace(/\s*\n\s*/g, " ")]
    : block.kind === "list" ? block.items.map((item) => sentence(item.map(written).join("").trim())) : []).join(" ");
}

// A text too long to show whole on a phone, more than about six lines or 600
// characters, which shows its first lines until it is opened.
const LONG_TEXT_LINES = 6;
const LONG_TEXT_CHARS = 600;

export const isLongText = (text: string): boolean => text.length > LONG_TEXT_CHARS || text.replace(/\r\n?/g, "\n").trim().split("\n").length > LONG_TEXT_LINES;
