import { test } from "node:test";
import assert from "node:assert/strict";
import { cardPlace, type Side } from "./quick-tour.ts";

// The desktop window at 1707 by 960 CSS pixels, with the CFO's panel beside
// the board as the first open arranges it, and a 460 by 190 card.
const VIEW = { width: 1707, height: 960 };
const CARD = { width: 460, height: 190 };
const PANEL = { left: 915, top: 90, width: 777, height: 855 };
const BOARD = { left: 10, top: 90, width: 893, height: 855 };
const COMMAND = { left: 1430, top: 14, width: 44, height: 44 };

test("the card sits on its step's side of the part when it fits there", () => {
  const cases: [string, typeof PANEL, Side, { left: number; top: number }][] = [
    ["left of the CFO's panel, centered on it", PANEL, "left", { left: 915 - 16 - 460, top: 90 + 855 / 2 - 190 / 2 }],
    ["right of the board, centered on it", BOARD, "right", { left: 10 + 893 + 16, top: 90 + 855 / 2 - 190 / 2 }],
    ["under the Command Center, centered on it", COMMAND, "under", { left: 1430 + 22 - 230, top: 14 + 44 + 16 }],
  ];
  for (const [name, part, side, want] of cases) assert.deepEqual(cardPlace(part, CARD, VIEW, side), want, name);
});

test("a side with no room gives way to the opposite side, then to the first other side with room", () => {
  const cases: [string, typeof PANEL, Side, { left: number; top: number }][] = [
    ["a panel too near the left edge takes its right", { left: 100, top: 90, width: 400, height: 600 }, "left", { left: 516, top: 90 + 300 - 95 }],
    ["a board too near the right edge takes its left", { left: 800, top: 90, width: 890, height: 600 }, "right", { left: 800 - 16 - 460, top: 90 + 300 - 95 }],
    ["a part at the window's foot takes over it", { left: 1200, top: 800, width: 44, height: 44 }, "under", { left: 1222 - 230, top: 800 - 16 - 190 }],
    ["a part with neither room beside nor over it takes under", { left: 10, top: 40, width: 1680, height: 100 }, "left", { left: 10 + 840 - 230, top: 40 + 100 + 16 }],
  ];
  for (const [name, part, side, want] of cases) assert.deepEqual(cardPlace(part, CARD, VIEW, side), want, name);
});

test("a card with no side free, or no part on screen, sits centered at the window's foot", () => {
  const phone = { width: 390, height: 844 }, card = { width: 358, height: 240 };
  const foot = { left: 16, top: 844 - 16 - 240 };
  assert.deepEqual(cardPlace({ left: 0, top: 0, width: 390, height: 844 }, card, phone, "left"), foot, "a part that fills the window");
  assert.deepEqual(cardPlace(null, card, phone, "right"), foot, "no part");
  assert.deepEqual(cardPlace(null, CARD, VIEW, "under"), { left: (1707 - 460) / 2, top: 960 - 16 - 190 }, "no part in the desktop window");
});

test("the card is held inside the window on every side", () => {
  // A part at the window's top left corner, whose centered card would cross
  // the top edge.
  const place = cardPlace({ left: 0, top: 0, width: 300, height: 60 }, CARD, VIEW, "right");
  assert.deepEqual(place, { left: 316, top: 16 });
  // A tall part whose card, centered on it, would cross the foot.
  assert.deepEqual(cardPlace({ left: 600, top: 700, width: 400, height: 1000 }, CARD, VIEW, "left"), { left: 124, top: 960 - 16 - 190 });
});

