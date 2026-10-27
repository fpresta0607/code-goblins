import { test } from "node:test";
import assert from "node:assert/strict";
import { messageWindow } from "./message-window.ts";

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
