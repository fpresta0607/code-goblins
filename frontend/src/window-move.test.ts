import { test } from "node:test";
import assert from "node:assert/strict";
import { QUIET_MS, windowMayMove } from "./window-move.ts";

// The supervisor moves the desktop window onto the program an update
// installed only when the page in it asks, and the page asks only when moving
// the window takes nothing from the Overlord: it is in the window, connected,
// holds nothing unsent and no answer in progress, and is in the tray or
// untouched for a while.
test("the window asks to move only when it is idle, so nothing he typed or is answering is lost", () => {
  const idle = { inWindow: true, connected: true, answering: false, hidden: true, quietFor: 0 };
  const cases: [string, Partial<typeof idle>, boolean][] = [
    ["in the tray", {}, true],
    ["shown and untouched for a while", { hidden: false, quietFor: QUIET_MS }, true],
    ["shown and touched a moment ago", { hidden: false, quietFor: QUIET_MS - 1 }, false],
    ["a browser tab, which no move is for", { inWindow: false }, false],
    ["the board not connected", { connected: false }, false],
    ["an answer in progress or text unsent", { answering: true }, false],
  ];
  for (const [name, change, want] of cases) assert.equal(windowMayMove({ ...idle, ...change }), want, name);
});
