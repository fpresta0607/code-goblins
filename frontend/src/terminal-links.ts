import type { IBufferRange, IDisposable, Terminal } from "@xterm/xterm";
import { openBoardAddress } from "./board-address.ts";
import { isWebLink, webAddresses } from "./messageText.ts";
import { scrawlLink } from "./scrawl-link.ts";

// A row of a terminal's buffer, cell by cell: the characters each cell draws
// and its width, 0 for the second half of a wide character.
export type TerminalRow = { chars: string; width: number }[];

// The most rows one wrapped line is read across, either way from the row
// asked about.
const WRAPPED_ROWS = 64;

// terminalAddresses finds each http or https address in one line that wraps
// across rows, the first of them buffer row firstRow (1-based), by the cells
// it covers, as xterm counts them.
export function terminalAddresses(rows: TerminalRow[], firstRow: number): { address: string; range: IBufferRange }[] {
  let text = "";
  const cells: { x: number; y: number; width: number }[] = [];
  rows.forEach((row, index) => row.forEach((cell, column) => {
    if (cell.width === 0) return;
    const chars = cell.chars || " ";
    for (let unit = 0; unit < chars.length; unit++) cells.push({ x: column + 1, y: firstRow + index, width: cell.width });
    text += chars;
  }));
  return webAddresses(text).map(({ start, end, address }) => {
    const last = cells[end - 1];
    return { address, range: { start: { x: cells[start].x, y: cells[start].y }, end: { x: last.x + last.width - 1, y: last.y } } };
  });
}

// openWebAddress opens an http or https address in a new tab, which the
// desktop window hands to his default browser, so the board is never
// navigated away, and a board address in this page (#2363). Anything else
// opens nothing.
export function openWebAddress(address: string): void {
  if (isWebLink(address) && !openBoardAddress(address)) window.open(scrawlLink(address), "_blank", "noopener,noreferrer");
}

// linkTerminalAddresses makes each http or https address a terminal draws a
// link that a click or a tap opens (the Overlord, 2026-11-11: "any type of
// URL like inside chat messages or UI or like the Galax chats. I can't click
// them"). xterm asks for a row's links only when the pointer is over it.
export function linkTerminalAddresses(terminal: Terminal): IDisposable {
  return terminal.registerLinkProvider({
    provideLinks(lineNumber, callback) {
      const buffer = terminal.buffer.active;
      let first = lineNumber - 1;
      for (let step = 0; step < WRAPPED_ROWS && first > 0 && buffer.getLine(first)?.isWrapped; step++) first--;
      const rows: TerminalRow[] = [];
      for (let y = first; y < lineNumber + WRAPPED_ROWS; y++) {
        const line = buffer.getLine(y);
        if (!line || y > first && !line.isWrapped) break;
        const row: TerminalRow = [];
        for (let x = 0; x < line.length; x++) {
          const cell = line.getCell(x);
          row.push({ chars: cell?.getChars() ?? "", width: cell?.getWidth() ?? 1 });
        }
        rows.push(row);
      }
      const links = terminalAddresses(rows, first + 1)
        .filter(({ range }) => range.start.y <= lineNumber && range.end.y >= lineNumber)
        .map(({ address, range }) => ({ range, text: address, activate: () => openWebAddress(address) }));
      callback(links.length ? links : undefined);
    },
  });
}
