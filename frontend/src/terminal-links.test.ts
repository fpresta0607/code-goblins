import { test } from "node:test";
import assert from "node:assert/strict";
import { openWebAddress, terminalAddresses, type TerminalRow } from "./terminal-links.ts";

// rowOf draws text one cell per character, a kana or CJK character taking
// two cells as xterm draws it, and pads the row to width with empty cells.
function rowOf(text: string, width = text.length): TerminalRow {
  const row: TerminalRow = [];
  for (const character of text) {
    if (/[\u3040-\u30ff\u4e00-\u9fff]/.test(character)) row.push({ chars: character, width: 2 }, { chars: "", width: 0 });
    else row.push({ chars: character, width: 1 });
  }
  while (row.length < width) row.push({ chars: "", width: 1 });
  return row;
}

for (const [name, rows, firstRow, want] of [
  ["an address on one row", [rowOf("See https://example.com/a now", 40)], 7, [{ address: "https://example.com/a", range: { start: { x: 5, y: 7 }, end: { x: 25, y: 7 } } }]],
  ["an address that wraps onto the next row", [rowOf("PR: https://github.com/o/r/pu"), rowOf("ll/12 merged", 29)], 3, [{ address: "https://github.com/o/r/pull/12", range: { start: { x: 5, y: 3 }, end: { x: 5, y: 4 } } }]],
  ["an address after wide characters", [rowOf("見て https://example.com", 30)], 1, [{ address: "https://example.com", range: { start: { x: 6, y: 1 }, end: { x: 24, y: 1 } } }]],
  ["the full stop that ends a sentence", [rowOf("Open http://127.0.0.1:4387/session/a.", 40)], 1, [{ address: "http://127.0.0.1:4387/session/a", range: { start: { x: 6, y: 1 }, end: { x: 36, y: 1 } } }]],
  ["two addresses", [rowOf("https://a.example and https://b.example", 40)], 1, [{ address: "https://a.example", range: { start: { x: 1, y: 1 }, end: { x: 17, y: 1 } } }, { address: "https://b.example", range: { start: { x: 23, y: 1 }, end: { x: 39, y: 1 } } }]],
  ["other schemes", [rowOf("javascript:alert(1) file:///C:/x ftp://example.com", 60)], 1, []],
] as const) {
  test(`a terminal finds ${name} by the cells it covers`, () => {
    // Arrange, act
    const found = terminalAddresses(rows.map((row) => [...row]), firstRow);

    // Assert
    assert.deepEqual(found, want);
  });
}

for (const [name, address, opened] of [
  ["an https address", "https://github.com/o/r/pull/12", ["https://github.com/o/r/pull/12", "_blank", "noopener,noreferrer"]],
  ["an http address", "http://127.0.0.1:4387/session/a", ["http://127.0.0.1:4387/session/a", "_blank", "noopener,noreferrer"]],
  ["a script", "javascript:alert(1)", null],
  ["a file", "file:///C:/Windows/notepad.exe", null],
] as const) {
  test(`a terminal link to ${name} opens ${opened ? "in a new tab" : "nothing"}`, () => {
    // Arrange
    const calls: unknown[][] = [];
    const before = Object.getOwnPropertyDescriptor(globalThis, "window");
    Object.defineProperty(globalThis, "window", { configurable: true, value: { open: (...args: unknown[]) => calls.push(args) } });
    try {
      // Act
      openWebAddress(address);
    } finally {
      if (before) Object.defineProperty(globalThis, "window", before);
      else Reflect.deleteProperty(globalThis, "window");
    }

    // Assert
    assert.deepEqual(calls, opened ? [opened] : []);
  });
}
