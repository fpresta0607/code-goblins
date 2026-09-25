import { test } from "node:test";
import assert from "node:assert/strict";
import { ackDue, ACK_STEP, closedReason, DEFAULT_FONT_SIZE, fontSizeFor, INPUT_MESSAGE, inputMessages, MAX_FONT_SIZE, MIN_FONT_SIZE, parseSize, reconnects, usableSize } from "./terminalStream.ts";

test("output is acknowledged in steps, and at once when the terminal has caught up", () => {
  const cases: [number, number, number, boolean][] = [
    [100, 0, 5000, false],
    [ACK_STEP, 0, ACK_STEP * 3, true],
    [5000, 0, 5000, true],
    [5000, 5000, 5000, false],
    [ACK_STEP + 10, 10, ACK_STEP * 2, true],
    [ACK_STEP + 9, 10, ACK_STEP * 2, false],
  ];
  for (const [consumed, acknowledged, written, due] of cases) assert.equal(ackDue(consumed, acknowledged, written), due, `${consumed} consumed, ${acknowledged} acknowledged, ${written} written`);
});

test("a long paste travels in ordered pieces the relay accepts, and nothing is lost", () => {
  const bytes = new Uint8Array(INPUT_MESSAGE * 2 + 17).map((_, i) => i % 251);
  const pieces = inputMessages(bytes);
  assert.equal(pieces.length, 3);
  assert.ok(pieces.every((piece) => piece.length <= INPUT_MESSAGE));
  assert.deepEqual(Uint8Array.from(pieces.flatMap((piece) => [...piece])), bytes);
  assert.deepEqual(inputMessages(new TextEncoder().encode("x")), [new TextEncoder().encode("x")]);
  assert.deepEqual(inputMessages(new Uint8Array()), []);
});

test("Ctrl with plus, minus or zero sizes the font within bounds, and other keys leave it", () => {
  assert.equal(fontSizeFor("=", 16), 17);
  assert.equal(fontSizeFor("+", MAX_FONT_SIZE), MAX_FONT_SIZE);
  assert.equal(fontSizeFor("-", 16), 15);
  assert.equal(fontSizeFor("-", MIN_FONT_SIZE), MIN_FONT_SIZE);
  assert.equal(fontSizeFor("0", 22), DEFAULT_FONT_SIZE);
  assert.equal(fontSizeFor("c", 16), null);
  assert.equal(DEFAULT_FONT_SIZE, 16);
});

test("only the relay's size messages are read as sizes", () => {
  assert.deepEqual(parseSize('{"type":"size","cols":100,"rows":30}'), { cols: 100, rows: 30 });
  for (const text of ['{"type":"resize","cols":100,"rows":30}', '{"type":"size","cols":"100","rows":30}', "not json", '{"type":"size","cols":0,"rows":30}']) assert.equal(parseSize(text), null, text);
});

test("a panel measured while hidden never resizes the terminal", () => {
  assert.equal(usableSize(120, 40), true);
  assert.equal(usableSize(19, 40), false);
  assert.equal(usableSize(120, 4), false);
  assert.equal(usableSize(Number.NaN, 40), false);
});

test("a view reconnects on its own only when the terminal is still there to reach", () => {
  const cases: [number, boolean][] = [[1013, true], [1001, true], [1006, true], [1000, false], [1008, false], [1003, false]];
  for (const [code, again] of cases) assert.equal(reconnects(code), again, String(code));
});

test("a closed view says why in plain words", () => {
  assert.equal(closedReason(1000, "The terminal ended with exit code 0."), "The terminal ended with exit code 0.");
  assert.equal(closedReason(1006, ""), "The connection to the board dropped.");
  assert.equal(closedReason(1008, "pipeline owns this task"), "The review gate owns this goblin's work right now, so typing is paused. Reconnect to watch the screen.");
});
