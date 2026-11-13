import { test } from "node:test";
import assert from "node:assert/strict";
import { boardPlace } from "./board-address.ts";

const BOARD = "http://127.0.0.1:4310";

// A board address names one place by one parameter on the board's own root,
// and nothing else is one.
for (const [address, want] of [
  [BOARD + "/?task=cg-touch", { kind: "task", id: "cg-touch" }],
  ["/?task=cg-touch", { kind: "task", id: "cg-touch" }],
  [BOARD + "/?item=question%3Aq1", { kind: "item", id: "question:q1" }],
  [BOARD + "/?item=review:r1#top", { kind: "item", id: "review:r1" }],
  ["https://example.com/?task=cg-touch", null],
  ["http://127.0.0.1:4311/?task=cg-touch", null],
  [BOARD + "/api/tasks?task=cg-touch", null],
  [BOARD + "/?task=", null],
  [BOARD + "/?task=cg-touch&item=question:q1", null],
  [BOARD + "/?task=cg-touch&task=cg-other", null],
  [BOARD + "/?task=cg-touch&token=x", null],
  [BOARD + "/?screen=cg-touch", null],
  [BOARD + "/", null],
  ["javascript:alert(1)", null],
  ["not an address", null],
] as const) {
  test(`the board reads ${address} as ${want ? want.kind + " " + want.id : "no place"}`, () => {
    // Act
    const place = boardPlace(address, BOARD);

    // Assert
    assert.deepEqual(place, want);
  });
}
