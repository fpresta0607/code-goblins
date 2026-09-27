import { test } from "node:test";
import assert from "node:assert/strict";
import { bracketedPaste, fittedFontSize, historyText, inputBytes, maxInputBytes, queueInput, scrollAction, typingHeldReason, wheelLines, type PaneCommand } from "./terminalInput.ts";

test("Unicode paste uses UTF-8 bytes including one complete bracketed wrapper", () => {
  const limit = Math.floor((maxInputBytes - 12) / 3);
  const accepted = bracketedPaste("界".repeat(limit));
  assert.ok(inputBytes(accepted) <= maxInputBytes);
  assert.ok(accepted.startsWith("\x1b[200~") && accepted.endsWith("\x1b[201~"));
  assert.throws(() => bracketedPaste("界".repeat(limit + 1)), /nothing was sent/);
  assert.throws(() => bracketedPaste("🙂".repeat(16382)), /nothing was sent/);
  assert.equal(bracketedPaste("one\n\x1b[201~two"), "\x1b[200~one\ntwo\x1b[201~");
});

const typed = (text: string): PaneCommand => ({ type: "terminal.input", text });

test("typing coalesces adjacent text, preserving Unicode, and keeps control keys and pastes apart", () => {
  const queue: PaneCommand[] = [];
  for (const text of ["c", "a", "f", "é", "日本", "🙂", "é"]) queueInput(queue, text);
  queueInput(queue, "\r");
  const paste = bracketedPaste("one\ntwo");
  queueInput(queue, paste);
  queueInput(queue, "after");
  queueInput(queue, "\x1b[A");
  assert.deepEqual(queue, [typed("café日本🙂é"), typed("\r"), typed(paste), typed("after"), typed("\x1b[A")]);
  const bounded: PaneCommand[] = [];
  for (let i = 0; i < 5000; i++) queueInput(bounded, "x");
  assert.equal(bounded.length, 2);
  assert.equal(bounded.map((command) => command.type === "terminal.input" ? command.text : "").join("").length, 5000);
});

test("scrolling up opens the pane's history, and scrolling down at its bottom follows the live screen again", () => {
  const cases: [string, boolean, boolean, number, ReturnType<typeof scrollAction>][] = [
    ["the wheel up on the live screen opens the history", false, true, -3, "open"],
    ["the wheel down on the live screen keeps following the bottom", false, true, 3, "none"],
    ["the wheel up in the history scrolls it", true, false, -3, "history"],
    ["the wheel down above the history's bottom scrolls it", true, false, 3, "history"],
    ["the wheel down at the history's bottom returns to the live screen", true, true, 3, "close"],
    ["the wheel up at the history's bottom scrolls it", true, true, -3, "history"],
  ];
  for (const [name, inHistory, atBottom, lines, action] of cases) assert.equal(scrollAction(inHistory, atBottom, lines), action, name);
});

test("a pane's history is drawn line by line from the first column, with no color left on", () => {
  assert.equal(historyText("one\n\x1b[31mtwo\x1b[0m\r\nthree"), "one\r\n\x1b[31mtwo\x1b[0m\r\nthree\x1b[0m");
  assert.equal(historyText(""), "\x1b[0m");
});

test("the wheel scrolls whole lines, keeping what a touchpad has not yet made a line", () => {
  const row = 18;
  const cases: [string, number, number, number, { lines: number; rest: number }][] = [
    ["a mouse notch in pixels", 0, 100, 0, { lines: 5, rest: 10 }],
    ["a notch the other way", 0, -100, 0, { lines: -5, rest: -10 }],
    ["a touchpad's small moves add up", 10, 10, 0, { lines: 1, rest: 2 }],
    ["less than a line waits", 0, 10, 0, { lines: 0, rest: 10 }],
    ["a wheel in lines", 0, 3, 1, { lines: 3, rest: 0 }],
    ["a wheel in pages scrolls the screen", 0, 1, 2, { lines: 40, rest: 0 }],
  ];
  for (const [name, rest, delta, mode, want] of cases) assert.deepEqual(wheelLines(rest, delta, mode, row, 40), want, name);
});

test("a pane's screen is fitted to the panel whole, by its width or its height, with no floor", () => {
  const cell = { width: 0.6, height: 1.2 };
  const cases: [string, number, number, number, number, number | null][] = [
    ["maximized, bound by height", 1800, 1000, 132, 43, 19],
    ["the side panel, bound by width and below 12 px", 900, 800, 132, 43, 11],
    ["a small pane in a large panel stops at 28 px", 2000, 1200, 80, 24, 28],
    ["a hidden panel keeps the font it has", 0, 800, 132, 43, null],
    ["a pane with no size yet keeps the font it has", 900, 800, 0, 0, null],
  ];
  for (const [name, width, height, cols, rows, size] of cases) assert.equal(fittedFontSize(width, height, cols, rows, cell), size, name);
});

test("the fit follows the cell size the terminal measured, not an assumed one", () => {
  assert.equal(fittedFontSize(900, 800, 132, 43, { width: 0.5, height: 1.2 }), 13.5);
  assert.equal(fittedFontSize(1800, 1000, 132, 43, { width: 0.6, height: 1.5 }), 15.5);
});

test("a refused input explains itself in plain words", () => {
  const cases: [string, string][] = [
    ["pipeline owns this task; use cfo pipeline respond or inspect its delivery evidence", "The review gate owns this goblin's work right now, so typing is paused. Reconnect to watch the screen."],
    ["pipeline custody has not been returned; use cfo pipeline recover", "The review gate owns this goblin's work right now, so typing is paused. Reconnect to watch the screen."],
    ["cannot verify pipeline custody", "cannot verify pipeline custody"],
    ["Native terminal identity changed. Select the current session; input was discarded.", "Native terminal identity changed. Select the current session; input was discarded."],
  ];
  for (const [raw, plain] of cases) assert.equal(typingHeldReason(raw), plain, raw);
});
