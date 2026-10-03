import { test } from "node:test";
import assert from "node:assert/strict";
import { rowFit } from "./panel-fit.ts";

// The CFO's row: a 253 px switch with its words, 98 px with its icons, a
// 44 px Close, two 44 px controls and 8 px gaps.
const cfo = { switchWords: 253, switchIcons: 98, corner: 44, control: 44, gap: 8, count: 2 };
// A goblin's row has Back, with its name, in the corner.
const goblin = { ...cfo, corner: 96 };

test("with room for everything the row shows every control and the switch's words", () => {
  assert.deepEqual(rowFit({ ...cfo, width: 670 }), { shown: 2, hasWords: true });
  assert.deepEqual(rowFit({ ...cfo, width: 417 }), { shown: 2, hasWords: true }, "253 + 16 + 44 + 2 x 52 is exactly 417");
});

test("a control the row cannot hold goes into More, which takes a control's room", () => {
  assert.deepEqual(rowFit({ ...cfo, width: 416 }), { shown: 0, hasWords: true }, "one control and More need the room of two, so both go");
  assert.deepEqual(rowFit({ ...cfo, count: 4, width: 480 }), { shown: 2, hasWords: true }, "of four, two stay and More holds two: 313 + 3 x 52 is 469");
  assert.deepEqual(rowFit({ ...cfo, width: 365 }), { shown: 0, hasWords: true }, "the switch, More and Close: 313 + 52");
});

test("the switch gives up its words only when More alone does not fit beside them", () => {
  assert.deepEqual(rowFit({ ...cfo, width: 364 }), { shown: 2, hasWords: false }, "with its icons the row holds both controls again");
  assert.deepEqual(rowFit({ ...goblin, width: 330 }), { shown: 2, hasWords: false }, "a goblin's panel at its narrowest, 360 px: 98 + 16 + 96 + 104 is 314");
  assert.deepEqual(rowFit({ ...goblin, count: 5, width: 330 }), { shown: 1, hasWords: false }, "of five, one stays beside More");
});

test("a panel with no switch fits its controls beside the corner button", () => {
  const queued = { ...goblin, switchWords: 0, switchIcons: 0, count: 1 };
  assert.deepEqual(rowFit({ ...queued, width: 330 }), { shown: 1, hasWords: true });
  assert.equal(rowFit({ ...queued, width: 150 }).shown, 0, "too narrow for the control, or More, beside Back");
});

test("a row too narrow for anything keeps the switch's icons and the corner button", () => {
  assert.deepEqual(rowFit({ ...goblin, width: 200 }), { shown: 0, hasWords: false });
});
