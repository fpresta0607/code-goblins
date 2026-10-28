import { test } from "node:test";
import assert from "node:assert/strict";
import { drawnSpans, messageWindow } from "./message-window.ts";

test("a long conversation draws only the rows within reach of the view, and keeps the height of the rest", () => {
  // Arrange: a thousand rows of 100 px, a 600 px view and 300 px of reach.
  const heights = Array.from({ length: 1000 }, () => 100);

  // Act and Assert
  assert.deepEqual(messageWindow(heights, 0, 600, 300), { start: 0, end: 9, before: 0, after: 99100 }, "the top");
  assert.deepEqual(messageWindow(heights, 50_000, 600, 300), { start: 497, end: 509, before: 49700, after: 49100 }, "the middle");
  assert.deepEqual(messageWindow(heights, Infinity, 600, 300), { start: 991, end: 1000, before: 99100, after: 0 }, "following the newest");
  assert.deepEqual(messageWindow(heights, 200_000, 600, 300), messageWindow(heights, Infinity, 600, 300), "a top past the end reads the last rows");
});

test("a short conversation, an empty one and rows of every height are drawn whole where they reach the view", () => {
  assert.deepEqual(messageWindow([80, 400, 30], 0, 900, 300), { start: 0, end: 3, before: 0, after: 0 });
  assert.deepEqual(messageWindow([], 0, 600, 300), { start: 0, end: 0, before: 0, after: 0 });
  assert.deepEqual(messageWindow([1000, 50, 50, 1000], 1050, 100, 0), { start: 2, end: 4, before: 1050, after: 0 }, "a tall row the view's edge cuts is drawn");
  const window = messageWindow([300, 300, 300, 300, 300], 600, 300, 0);
  assert.equal(window.before + [300, 300, 300, 300, 300].slice(window.start, window.end).reduce((sum, each) => sum + each, 0) + window.after, 1500, "the room before and after and the drawn rows make the whole height");
});

test("a selection keeps the rows it needs drawn: all it spans, or the one a caret is in", () => {
  // Arrange
  const window = { start: 40, end: 50, before: 4000, after: 5000 };
  const cases: [string, number, number, boolean, { start: number; end: number }[]][] = [
    ["no selection", -1, -1, true, [{ start: 40, end: 50 }]],
    ["a selection inside the window", 42, 45, false, [{ start: 40, end: 50 }]],
    ["a selection from far above", 10, 48, false, [{ start: 10, end: 50 }]],
    ["a selection dragged up from below", 70, 41, false, [{ start: 40, end: 71 }]],
    ["a caret above the window", 10, 10, true, [{ start: 10, end: 11 }, { start: 40, end: 50 }]],
    ["a caret below the window", 80, 80, true, [{ start: 40, end: 50 }, { start: 80, end: 81 }]],
    ["a caret inside the window", 44, 44, true, [{ start: 40, end: 50 }]],
  ];

  for (const [name, from, to, isCollapsed, want] of cases) {
    // Act
    const spans = drawnSpans(window, from, to, isCollapsed);

    // Assert
    assert.deepEqual(spans, want, name);
  }
});
