import { test } from "node:test";
import assert from "node:assert/strict";
import { countedTitle, showBadge } from "./arrivals.ts";

test("the tab's title carries how many items wait on the Overlord", () => {
  assert.equal(countedTitle("Code Goblins", 3), "(3) Code Goblins");
  assert.equal(countedTitle("Code Goblins", 0), "Code Goblins");
});

// The app's icon carries the count where the browser badges an installed
// app, loses it at none, and a browser with no badges is left alone.
for (const [name, count, hasBadges, want] of [
  ["a count", 3, true, ["set 3"]],
  ["none", 0, true, ["clear"]],
  ["a browser with no badges", 3, false, []],
] as const) {
  test(`the app's badge for ${name}`, async (t) => {
    // Arrange
    const calls: string[] = [];
    const badges = { setAppBadge: (shown: number) => { calls.push("set " + shown); return Promise.resolve(); }, clearAppBadge: () => { calls.push("clear"); return Promise.resolve(); } };
    const before = Object.getOwnPropertyDescriptor(globalThis, "navigator");
    Object.defineProperty(globalThis, "navigator", { value: hasBadges ? badges : {}, configurable: true });
    t.after(() => {
      if (before) Object.defineProperty(globalThis, "navigator", before);
      else Reflect.deleteProperty(globalThis, "navigator");
    });

    // Act
    const answer = showBadge(count);

    // Assert
    await answer;
    assert.equal(answer === undefined, !hasBadges);
    assert.deepEqual(calls, want);
  });
}
