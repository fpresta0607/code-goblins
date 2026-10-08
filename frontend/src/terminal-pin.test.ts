import { test } from "node:test";
import assert from "node:assert/strict";
import { lineInput, pinnedLine, type PinCell } from "./terminal-pin.ts";

test("the line he types on is pinned at the bottom only while he reads history that hides it", () => {
  const cases: { name: string; at: { viewportY: number; baseY: number; cursorY: number; rows: number }; pinned: number | null }[] = [
    { name: "at the live end", at: { viewportY: 500, baseY: 500, cursorY: 39, rows: 40 }, pinned: null },
    { name: "scrolled up with the input on the last row", at: { viewportY: 300, baseY: 500, cursorY: 39, rows: 40 }, pinned: 539 },
    { name: "scrolled up one line", at: { viewportY: 499, baseY: 500, cursorY: 39, rows: 40 }, pinned: 539 },
    { name: "scrolled up with the input still in sight above the last row", at: { viewportY: 498, baseY: 500, cursorY: 3, rows: 40 }, pinned: null },
    { name: "scrolled up past the input high on the screen", at: { viewportY: 400, baseY: 500, cursorY: 3, rows: 40 }, pinned: 503 },
    { name: "the input on the row the pin covers", at: { viewportY: 464, baseY: 500, cursorY: 3, rows: 40 }, pinned: 503 },
    { name: "a full-screen program, which keeps no history", at: { viewportY: 0, baseY: 0, cursorY: 39, rows: 40 }, pinned: null },
  ];
  for (const { name, at, pinned } of cases) assert.equal(pinnedLine(at), pinned, name);
});

interface Look {
  width?: number;
  bold?: boolean; dim?: boolean; italic?: boolean; underline?: boolean; blink?: boolean; inverse?: boolean; invisible?: boolean; strikethrough?: boolean; overline?: boolean;
  fg?: number; fgRGB?: boolean; bg?: number; bgRGB?: boolean;
}

// A cell as xterm's buffer reads it: a color is a palette index unless it is
// marked true color, and none is the default color.
function cell(chars: string, look: Look = {}): PinCell {
  const flag = (isSet: boolean | undefined) => isSet ? 1 : 0;
  return {
    getChars: () => chars,
    getWidth: () => look.width ?? 1,
    isBold: () => flag(look.bold), isDim: () => flag(look.dim), isItalic: () => flag(look.italic), isUnderline: () => flag(look.underline), isBlink: () => flag(look.blink),
    isInverse: () => flag(look.inverse), isInvisible: () => flag(look.invisible), isStrikethrough: () => flag(look.strikethrough), isOverline: () => flag(look.overline),
    isFgRGB: () => look.fg !== undefined && !!look.fgRGB, isFgPalette: () => look.fg !== undefined && !look.fgRGB, getFgColor: () => look.fg ?? 0,
    isBgRGB: () => look.bg !== undefined && !!look.bgRGB, isBgPalette: () => look.bg !== undefined && !look.bgRGB, getBgColor: () => look.bg ?? 0,
  };
}
const text = (chars: string, look: Look = {}) => [...chars].map((each) => cell(each, look));
const line = (cells: PinCell[], cols: number) => ({ getCell: (x: number) => x < cols ? cells[x] ?? cell("") : undefined });
const START = "\x1b[H\x1b[2K";

test("the pinned line is drawn as the terminal drew it, with the cursor where he types", () => {
  const cases: { name: string; cells: PinCell[]; cursorX: number; isCursorShown: boolean; input: string }[] = [
    { name: "a plain prompt, its blank end left out", cells: text("Your fly.io email:   "), cursorX: 19, isCursorShown: true, input: START + "\x1b[0mYour fly.io email:\x1b[0m\x1b[1;20H\x1b[?25h" },
    { name: "the first 16 colors as SGR 30 to 37 and 90 to 97", cells: [...text("ok", { bold: true, fg: 2 }), ...text(" "), ...text("!", { fg: 9, bg: 12 })], cursorX: 4, isCursorShown: true, input: START + "\x1b[0;1;32mok\x1b[0m \x1b[0;91;104m!\x1b[0m\x1b[1;5H\x1b[?25h" },
    { name: "256 colors and true color", cells: [...text("a", { fg: 208 }), ...text("b", { bg: 0x112233, bgRGB: true }), ...text("c", { fg: 0xff8000, fgRGB: true })], cursorX: 3, isCursorShown: true, input: START + "\x1b[0;38;5;208ma\x1b[0;48;2;17;34;51mb\x1b[0;38;2;255;128;0mc\x1b[0m\x1b[1;4H\x1b[?25h" },
    { name: "every style", cells: text("s", { dim: true, italic: true, underline: true, blink: true, inverse: true, invisible: true, strikethrough: true, overline: true }), cursorX: 1, isCursorShown: true, input: START + "\x1b[0;2;3;4;5;7;8;9;53ms\x1b[0m\x1b[1;2H\x1b[?25h" },
    { name: "a wide character once", cells: [cell("界", { width: 2 }), cell("", { width: 0 }), ...text("x")], cursorX: 3, isCursorShown: true, input: START + "\x1b[0m界x\x1b[0m\x1b[1;4H\x1b[?25h" },
    { name: "a colored blank end kept", cells: [...text(">"), ...text("  ", { bg: 4 })], cursorX: 1, isCursorShown: true, input: START + "\x1b[0m>\x1b[0;44m  \x1b[0m\x1b[1;2H\x1b[?25h" },
    { name: "a cursor the program hid", cells: text("> "), cursorX: 2, isCursorShown: false, input: START + "\x1b[0m>\x1b[0m\x1b[1;3H\x1b[?25l" },
    { name: "an empty line", cells: [], cursorX: 0, isCursorShown: true, input: START + "\x1b[0m\x1b[1;1H\x1b[?25h" },
  ];
  for (const { name, cells, cursorX, isCursorShown, input } of cases) assert.equal(lineInput(line(cells, 30), 30, cursorX, isCursorShown), input, name);
});
