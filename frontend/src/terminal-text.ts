import type { IconName } from "./Icon.tsx";
import { webAddresses } from "./messageText.ts";

// Terminal text as the board reads it everywhere it shows any (#2524): what a
// command printed in a chat row, what a Command Center command left on its
// terminal, and a block of it a question quotes. A program's colour codes
// colour its text, every other escape goes, a line the program redrew shows
// as its terminal left it, and a line is one line however long, for its
// drawing to wrap on purpose. Where a program coloured nothing, a line that
// starts as an error, a warning or a success reads as one, and a path or a
// web address is told apart from the words around it. A prompt's lines read
// as written: the card that shows one offers its own choices.
export type TerminalColor = "red" | "green" | "yellow" | "blue" | "magenta" | "cyan" | "gray";
export interface TerminalSpan { text: string; color?: TerminalColor; isBold?: true; isDim?: true; isItalic?: true; isUnderlined?: true; kind?: "path" | "link" }
export type TerminalTone = "" | "error" | "warning" | "success";
export interface TerminalLine { spans: TerminalSpan[]; tone: TerminalTone }
// How a command ended, or that it still runs, as its block's last line says
// under its own row's mark.
export interface TerminalResult { tone: "success" | "error" | "warning" | "running"; text: string; icon: IconName }

type Style = Omit<TerminalSpan, "text" | "kind">;
interface Cell { char: string; style: Style }

const ESCAPE = "\x1b";
const BELL = "\x07";
// A move to a column stops at its terminal's right edge. Text keeps no width,
// so here a move stops at 512 columns, however far it asks.
const COLUMNS = 512;
const BLANK: Cell = { char: " ", style: {} };

// The eight colours a terminal names, in its order. Black is gray on the
// board's dark ground, and white is the text's own colour.
const BASE: (TerminalColor | undefined)[] = ["gray", "red", "green", "yellow", "blue", "magenta", "cyan", undefined];
const CUBE = [0, 95, 135, 175, 215, 255];

// hueOf names a colour given in red, green and blue by the nearest of the
// board's own, so text in any colour keeps its meaning and its contrast.
function hueOf(red: number, green: number, blue: number): TerminalColor | undefined {
  const most = Math.max(red, green, blue), least = Math.min(red, green, blue);
  if (most - least < 48) return most >= 200 ? undefined : "gray";
  const spread = most - least;
  const sixth = most === red ? ((green - blue) / spread + 6) % 6 : most === green ? (blue - red) / spread + 2 : (red - green) / spread + 4;
  const hue = sixth * 60;
  return hue < 20 || hue >= 330 ? "red" : hue < 70 ? "yellow" : hue < 160 ? "green" : hue < 200 ? "cyan" : hue < 260 ? "blue" : "magenta";
}

function indexedColor(index: number): TerminalColor | undefined {
  if (index < 16) return BASE[index % 8];
  if (index >= 232) return hueOf(8 + (index - 232) * 10, 8 + (index - 232) * 10, 8 + (index - 232) * 10);
  const at = index - 16;
  return hueOf(CUBE[Math.floor(at / 36)], CUBE[Math.floor(at / 6) % 6], CUBE[at % 6]);
}

// styled is the style a colour code leaves: its colour and weight set or
// cleared. A background colour is skipped, since the board draws none.
function styled(style: Style, parameters: string): Style {
  const codes = parameters.split(/[;:]/).map((code) => Number(code) || 0);
  let next: Style = { ...style };
  for (let at = 0; at < codes.length; at++) {
    const code = codes[at];
    const extended = codes[at + 1] === 5 ? 2 : codes[at + 1] === 2 ? 4 : 0;
    if (code === 0) next = {};
    else if (code === 1) next.isBold = true;
    else if (code === 2) next.isDim = true;
    else if (code === 3) next.isItalic = true;
    else if (code === 4) next.isUnderlined = true;
    else if (code === 22) { delete next.isBold; delete next.isDim; }
    else if (code === 23) delete next.isItalic;
    else if (code === 24) delete next.isUnderlined;
    else if (code >= 30 && code <= 37 || code >= 90 && code <= 97 || code === 39 || code === 38) {
      const color = code === 39 ? undefined : code === 38 ? (extended === 2 ? indexedColor(codes[at + 2] ?? 0) : extended === 4 ? hueOf(codes[at + 2] ?? 0, codes[at + 3] ?? 0, codes[at + 4] ?? 0) : next.color) : BASE[code % 10];
      if (color) next.color = color;
      else delete next.color;
      if (code === 38) at += extended;
    } else if (code === 48) at += extended;
  }
  return next;
}

// cellRows lays text out as a terminal would leave it, row by row: a
// carriage return or a backspace moves back over what the row holds, an
// erase cuts it, and a colour code styles what follows.
function cellRows(text: string): Cell[][] {
  const rows: Cell[][] = [];
  let row: Cell[] = [];
  let column = 0;
  let style: Style = {};
  for (let at = 0; at < text.length;) {
    const char = String.fromCodePoint(text.codePointAt(at) ?? 0);
    if (char === ESCAPE) {
      const kind = text[at + 1];
      let end = at + 2;
      if (kind === "[") {
        while (end < text.length && text[end] >= "0" && text[end] <= "?") end++;
        const parameters = text.slice(at + 2, end);
        while (end < text.length && text[end] >= " " && text[end] <= "/") end++;
        const final = text[end];
        end++;
        if (final === "m") style = styled(style, parameters);
        else if (final === "G") column = Math.min(Math.max(0, (Number(parameters) || 1) - 1), COLUMNS - 1);
        else if (final === "K" && parameters === "2") row = [];
        else if (final === "K" && parameters !== "1") row.length = Math.min(row.length, column);
      } else if (kind === "]") {
        while (end < text.length && text[end] !== BELL && text[end] !== ESCAPE) end++;
        end += text[end] === ESCAPE ? 2 : 1;
      } else if (kind === "(" || kind === ")") end++;
      at = end;
      continue;
    }
    at += char.length;
    if (char === "\n") { rows.push(row); row = []; column = 0; }
    else if (char === "\r") column = 0;
    else if (char === "\b") column = Math.max(0, column - 1);
    else if (char >= " " && char !== "\x7f" || char === "\t") {
      while (row.length < column) row.push(BLANK);
      row[column++] = { char, style };
    }
  }
  if (row.length) rows.push(row);
  while (rows.length && rows[rows.length - 1].every((cell) => cell.char === " ")) rows.pop();
  return rows;
}

const styleKey = (style: Style) => [style.color, style.isBold, style.isDim, style.isItalic, style.isUnderlined].join();

// A path: one that starts at a drive, the folder it is in, its parent, a
// home or the root, or a relative one that ends in a file's extension, each
// with the line and column a tool may add.
const PATH = /(?<![\w.@+~\\/-])(?:(?:[A-Za-z]:[\\/]|\.{1,2}[\\/]|~[\\/])(?:[\w.@+-]+[\\/])*[\w.@+-]*[\w@+-]|[\\/](?:[\w.@+-]+[\\/])+[\w.@+-]*[\w@+-]|(?:[\w.@+-]+[\\/])+[\w.@+-]+\.[A-Za-z]\w{0,7}|[\w@+-][\w.@+-]*\.[A-Za-z]\w{0,7}(?=:\d))(?::\d+(?::\d+)?)?/g;

// named tells the web addresses and the paths in an uncoloured span apart
// from its words, an address first, so no path is read out of one.
function named(span: TerminalSpan): TerminalSpan[] {
  if (span.color) return [span];
  const { text, ...style } = span;
  const out: TerminalSpan[] = [];
  const words = (from: number, to: number) => {
    let at = from;
    for (const match of text.slice(from, to).matchAll(PATH)) {
      const start = from + match.index;
      if (start > at) out.push({ text: text.slice(at, start), ...style });
      out.push({ text: match[0], ...style, kind: "path" });
      at = start + match[0].length;
    }
    if (at < to) out.push({ text: text.slice(at, to), ...style });
  };
  let at = 0;
  for (const { start, end, address } of webAddresses(text)) {
    words(at, start);
    out.push({ text: address, ...style, kind: "link" });
    at = end;
  }
  words(at, text.length);
  return out;
}

// A line's first word says its outcome only where a colon, a space or the
// line's end follows it: in `panic(err)` or `ok, err := run()`, a line of
// source a command printed, the word is a name.
const SAID = "(?=[:!\\s[]|$)";
const ERROR_LINE = new RegExp("^\\s*(?:--- )?(?:error|fatal|panic|fail|failed|failure|exception|traceback|npm err!|npm error|[✗✘✖×]\\uFE0F?)" + SAID, "i");
const WARNING_LINE = new RegExp("^\\s*(?:warning|warn|npm warn|⚠\\uFE0F?)" + SAID, "i");
const SUCCESS_LINE = new RegExp("^\\s*(?:--- )?(?:ok|pass|passed|success|done|[✓✔]\\uFE0F?)" + SAID, "i");

// terminalLines reads terminal text as its lines, each as its spans and what
// it reads as.
export function terminalLines(text: string): TerminalLine[] {
  return cellRows(text).map((row): TerminalLine => {
    const spans: TerminalSpan[] = [];
    let key = "";
    for (const cell of row) {
      const next = styleKey(cell.style);
      if (spans.length && next === key) spans[spans.length - 1].text += cell.char;
      else spans.push({ text: cell.char, ...cell.style });
      key = next;
    }
    const plain = spans.map((span) => span.text).join("");
    const isColoured = spans.some((span) => span.color);
    const tone = isColoured ? "" : ERROR_LINE.test(plain) ? "error" : WARNING_LINE.test(plain) ? "warning" : SUCCESS_LINE.test(plain) ? "success" : "";
    return { spans: spans.flatMap(named), tone };
  });
}

// plainTerminalText is terminal text as Copy takes it: its lines as drawn,
// with no escape among them.
export const plainTerminalText = (lines: TerminalLine[]): string => lines.map((line) => line.spans.map((span) => span.text).join("")).join("\n");

// commandLines is a command as its lines, each with its own indent, without
// the blank lines at its ends.
export function commandLines(command: string): string[] {
  const lines = command.replace(/\r\n?/g, "\n").split("\n");
  while (lines.length && !lines[0].trim()) lines.shift();
  while (lines.length && !lines[lines.length - 1].trim()) lines.pop();
  return lines;
}

// A word this long may not fit a narrow card's line.
const LONG_WORD = /\S{25,}/g;
const TO_SEPARATOR = /[^\\/,;=&?|:_.]*[\\/,;=&?|:_.]+|[^\\/,;=&?|:_.]+$/g;

// softBreaks cuts text into the pieces a line may wrap between besides its
// spaces: after each separator of a long word, such as a path's folders, and
// never inside a word.
export function softBreaks(text: string): string[] {
  const pieces: string[] = [];
  let at = 0;
  for (const word of text.matchAll(LONG_WORD)) {
    const parts = word[0].match(TO_SEPARATOR) ?? [];
    if (parts.length < 2) continue;
    pieces.push(text.slice(at, word.index) + parts[0], ...parts.slice(1, -1));
    at = word.index + word[0].length - parts[parts.length - 1].length;
  }
  pieces.push(text.slice(at));
  return pieces;
}
