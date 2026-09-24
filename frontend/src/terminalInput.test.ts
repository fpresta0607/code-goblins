import { test } from "node:test";
import assert from "node:assert/strict";
import { bracketedPaste, fittedFontSize, inputBytes, maxInputBytes, queueInput, typingHeldReason } from "./terminalInput.ts";

test("Unicode paste uses UTF-8 bytes including one complete bracketed wrapper", () => {
  const limit = Math.floor((maxInputBytes - 12) / 3);
  const accepted = bracketedPaste("界".repeat(limit));
  assert.ok(inputBytes(accepted) <= maxInputBytes);
  assert.ok(accepted.startsWith("\x1b[200~") && accepted.endsWith("\x1b[201~"));
  assert.throws(() => bracketedPaste("界".repeat(limit + 1)), /nothing was sent/);
  assert.throws(() => bracketedPaste("🙂".repeat(16382)), /nothing was sent/);
  assert.equal(bracketedPaste("one\n\x1b[201~two"), "\x1b[200~one\ntwo\x1b[201~");
});

test("typing coalesces adjacent text, preserving Unicode, and keeps control keys and pastes apart", () => {
  const queue: string[] = [];
  for (const text of ["c", "a", "f", "é", "日本", "🙂", "é"]) queueInput(queue, text);
  queueInput(queue, "\r");
  const paste = bracketedPaste("one\ntwo");
  queueInput(queue, paste);
  queueInput(queue, "after");
  queueInput(queue, "\x1b[A");
  assert.deepEqual(queue, ["café日本🙂é", "\r", paste, "after", "\x1b[A"]);
  const bounded: string[] = [];
  for (let i = 0; i < 5000; i++) queueInput(bounded, "x");
  assert.equal(bounded.length, 2);
  assert.equal(bounded.join("").length, 5000);
});

test("a pane's frame is fitted to the panel's width, never cropped and never below 12 px", () => {
  const cases: [number, number, number][] = [[1200, 80, 15], [700, 80, 14.5], [771, 100, 12.5], [700, 120, 12], [350, 120, 12], [0, 80, 12], [900, 0, 15]];
  for (const [width, cols, size] of cases) assert.equal(fittedFontSize(width, cols), size, width + "px for " + cols + " columns");
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
