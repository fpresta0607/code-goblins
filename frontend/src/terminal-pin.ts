import type { IBufferCell } from "@xterm/xterm";

export type PinCell = Pick<IBufferCell, "getChars" | "getWidth" | "isBold" | "isDim" | "isItalic" | "isUnderline" | "isBlink" | "isInverse" | "isInvisible" | "isStrikethrough" | "isOverline" | "isFgRGB" | "isFgPalette" | "getFgColor" | "isBgRGB" | "isBgPalette" | "getBgColor">;

// pinnedLine is the buffer line of the live line he types on, the cursor's
// row, while he reads history that hides it, and null otherwise: at the live
// end, with that line still in sight above the panel's last row, or in a
// full-screen program, such as Claude Code's, which keeps no history in the
// terminal and draws its own input line.
export function pinnedLine({ viewportY, baseY, cursorY, rows }: { viewportY: number; baseY: number; cursorY: number; rows: number }): number | null {
  if (viewportY >= baseY) return null;
  const line = baseY + cursorY;
  return line > viewportY + rows - 2 ? line : null;
}

// lineInput is what draws a line on a one-row terminal as the terminal drew
// it: each run of cells in its colors and styles, the blank end left out,
// and the cursor where it stands, hidden where the program hid it.
export function lineInput(line: { getCell(x: number): PinCell | undefined }, cols: number, cursorX: number, isCursorShown: boolean): string {
  const runs: { style: string; text: string }[] = [];
  for (let x = 0; x < cols; x++) {
    const cell = line.getCell(x);
    if (!cell) break;
    if (cell.getWidth() === 0) continue;
    const style = cellStyle(cell), text = cell.getChars() || " ";
    const last = runs.at(-1);
    if (last?.style === style) last.text += text; else runs.push({ style, text });
  }
  const last = runs.at(-1);
  if (last?.style === "0") {
    last.text = last.text.trimEnd();
    if (!last.text) runs.pop();
  }
  return "\x1b[H\x1b[2K" + runs.map((run) => "\x1b[" + run.style + "m" + run.text).join("") + "\x1b[0m\x1b[1;" + (cursorX + 1) + "H\x1b[?25" + (isCursorShown ? "h" : "l");
}

// cellStyle is the SGR that sets a cell's look from the default one.
function cellStyle(cell: PinCell): string {
  const codes = [0];
  const styles: [number, number][] = [[cell.isBold(), 1], [cell.isDim(), 2], [cell.isItalic(), 3], [cell.isUnderline(), 4], [cell.isBlink(), 5], [cell.isInverse(), 7], [cell.isInvisible(), 8], [cell.isStrikethrough(), 9], [cell.isOverline(), 53]];
  for (const [isSet, code] of styles) if (isSet) codes.push(code);
  codes.push(...color(cell.isFgRGB(), cell.isFgPalette(), cell.getFgColor(), 30));
  codes.push(...color(cell.isBgRGB(), cell.isBgPalette(), cell.getBgColor(), 40));
  return codes.join(";");
}

// color sets a foreground (base 30) or background (base 40) color: the first
// 16 by their own codes, as a program sets them, so bold keeps drawing them
// bright, the rest of the palette and true color by their extended codes.
function color(isRGB: boolean, isPalette: boolean, value: number, base: number): number[] {
  if (isRGB) return [base + 8, 2, value >> 16 & 255, value >> 8 & 255, value & 255];
  if (!isPalette) return [];
  if (value < 8) return [base + value];
  if (value < 16) return [base + 60 + value - 8];
  return [base + 8, 5, value];
}
