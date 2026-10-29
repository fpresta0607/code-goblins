import { test } from "node:test";
import assert from "node:assert/strict";
import { rowFit } from "./panel-fit.ts";

const cfo = { corner: 44, control: 44, gap: 8, count: 2 };
const goblin = { ...cfo, corner: 96 };

test("with room for everything the row shows every control", () => {
  for (const width of [670, 148]) assert.deepEqual(rowFit({ ...cfo, width }), { shown: 2 }, "44 + 2 x 52 is exactly 148");
});

test("a control the row cannot hold goes into More, which takes a control's room", () => {
  assert.deepEqual(rowFit({ ...cfo, width: 147 }), { shown: 0 }, "one control and More need the room of two, so both go");
  assert.deepEqual(rowFit({ ...cfo, count: 4, width: 200 }), { shown: 2 }, "two controls, More and Close occupy 44 + 3 x 52");
  assert.deepEqual(rowFit({ ...cfo, width: 96 }), { shown: 0 }, "More and Close occupy 44 + 52");
});

test("a wider corner leaves less room for controls", () => {
  assert.deepEqual(rowFit({ ...goblin, width: 200 }), { shown: 2 });
  assert.deepEqual(rowFit({ ...goblin, width: 199 }), { shown: 0 });
  assert.deepEqual(rowFit({ ...goblin, count: 5, width: 252 }), { shown: 2 });
  assert.deepEqual(rowFit({ ...goblin, count: 1, width: 148 }), { shown: 1 });
});

test("a panel with no controls needs only its corner button", () => {
  for (const width of [96, 330]) assert.deepEqual(rowFit({ ...goblin, count: 0, width }), { shown: 0 });
});

test("a row too narrow for any control keeps its corner and puts controls in More", () => {
  for (const width of [0, 95, 147]) assert.deepEqual(rowFit({ ...goblin, width }), { shown: 0 });
});

test("a row too narrow even for More keeps a lone control, which takes More's room", () => {
  for (const width of [0, 95, 147]) assert.deepEqual(rowFit({ ...goblin, count: 1, width }), { shown: 1 });
});
