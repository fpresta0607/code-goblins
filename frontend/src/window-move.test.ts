import { test } from "node:test";
import assert from "node:assert/strict";
import { windowMayMove } from "./window-move.ts";

// The supervisor moves the desktop window onto the program an update
// installed only when the page in it asks, and the page asks only when moving
// the window takes nothing from the Overlord: it is in the window, connected,
// holds nothing unsent and no answer in progress, and is in the tray. A
// window he has open is never moved, however long it goes untouched: the
// move ends that window and opens another.
test("the window asks to move only from the tray, so a window he has open stays put", () => {
  const idle = { inWindow: true, connected: true, answering: false, hidden: true };
  const cases: [string, Partial<typeof idle>, boolean][] = [
    ["in the tray", {}, true],
    ["shown, however long untouched", { hidden: false }, false],
    ["a browser tab, which no move is for", { inWindow: false }, false],
    ["the board not connected", { connected: false }, false],
    ["an answer in progress or text unsent", { answering: true }, false],
  ];
  for (const [name, change, want] of cases) assert.equal(windowMayMove({ ...idle, ...change }), want, name);
});
