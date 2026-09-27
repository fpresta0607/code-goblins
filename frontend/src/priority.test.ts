import { test } from "node:test";
import assert from "node:assert/strict";
import { dropIndex, moveTo, orderShown, pendingSettled, rankLabel, stepped } from "./priority.ts";

const ids = ["a", "b", "c", "d"];

test("moving a card puts it at the place it was dropped and keeps the others in order", () => {
  assert.deepEqual(moveTo(ids, "a", 2), ["b", "c", "a", "d"]);
  assert.deepEqual(moveTo(ids, "d", 0), ["d", "a", "b", "c"]);
  assert.deepEqual(moveTo(ids, "b", 1), ids);
  assert.deepEqual(moveTo(ids, "b", 9), ["a", "c", "d", "b"]);
  assert.deepEqual(moveTo(ids, "gone", 0), ids);
});

test("Alt with an arrow moves a card one place, and never past either end", () => {
  assert.deepEqual(stepped(ids, "b", -1), ["b", "a", "c", "d"]);
  assert.deepEqual(stepped(ids, "b", 1), ["a", "c", "b", "d"]);
  assert.equal(stepped(ids, "a", -1), null);
  assert.equal(stepped(ids, "d", 1), null);
  assert.equal(stepped(ids, "gone", 1), null);
});

test("in one column a dragged card lands before the first other card whose middle is below the pointer", () => {
  const boxes = [0, 60, 120].map((top) => ({ left: 0, top, width: 300, height: 50 }));
  assert.equal(dropIndex(boxes, 150, 10, 1), 0);
  assert.equal(dropIndex(boxes, 150, 60, 1), 1);
  assert.equal(dropIndex(boxes, 150, 144, 1), 2);
  assert.equal(dropIndex(boxes, 150, 400, 1), 3);
  assert.equal(dropIndex([], 150, 40, 1), 0);
});

test("in a grid a dragged card lands in reading order: a later row, or its own row right of the pointer", () => {
  // Two rows of three cards, 100 wide and 50 tall, with 10 px gaps.
  const boxes = [0, 60].flatMap((top) => [0, 110, 220].map((left) => ({ left, top, width: 100, height: 50 })));
  assert.equal(dropIndex(boxes, 20, 25, 3), 0);
  assert.equal(dropIndex(boxes, 80, 25, 3), 1);
  assert.equal(dropIndex(boxes, 300, 25, 3), 3);
  assert.equal(dropIndex(boxes, 150, 55, 3), 3, "the gap between rows lands at the start of the next row");
  assert.equal(dropIndex(boxes, 200, 80, 3), 5);
  assert.equal(dropIndex(boxes, 500, 500, 3), 6);
});

test("the order just dropped shows until the supervisor's list agrees with it", () => {
  const tasks = ids.map((id) => ({ id }));
  assert.deepEqual(orderShown(tasks, ["c", "a", "b", "d"]).map((task) => task.id), ["c", "a", "b", "d"]);
  assert.deepEqual(orderShown(tasks, null).map((task) => task.id), ids);
  assert.deepEqual(orderShown([...tasks, { id: "new" }], ["d", "c", "b", "a"]).map((task) => task.id), ["d", "c", "b", "a", "new"]);
});

test("a dropped order settles once the list agrees with it or changed underneath it", () => {
  assert.equal(pendingSettled(["b", "a"], ["b", "a"]), true);
  assert.equal(pendingSettled(["a", "b"], ["b", "a"]), false);
  assert.equal(pendingSettled(["a", "b", "c"], ["b", "a"]), true);
  assert.equal(pendingSettled(["a"], ["b", "a"]), true);
});

test("a card's place is read out as its rank of the list", () => {
  assert.equal(rankLabel(0, 4), "priority 1 of 4");
});
