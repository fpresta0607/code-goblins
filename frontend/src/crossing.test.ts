import { test } from "node:test";
import assert from "node:assert/strict";
import { crossingLine } from "./crossing.ts";
import type { CrossingView } from "./types.ts";

const undo = "goblins cutover --undo 'C:\\home\\state\\backups\\cutover-20261112T050000Z'";
const crossing = (waiting: string[], problem = ""): CrossingView => ({ at: "2026-11-12T05:00:00Z", undo, waiting, problem });

test("a home that made no move says nothing", () => {
  assert.equal(crossingLine(undefined), null);
});

test("before any terminal restarts the notice names each one and when it restarts", () => {
  const line = crossingLine(crossing(["the CEO", "Huey"]));
  assert.ok(line);
  assert.match(line.text, /command is now goblins and its settings begin GOBLINS_/);
  assert.match(line.text, /a terminal you opened before the update needs opening again/);
  assert.match(line.text, /each once it is idle with nothing typed on its prompt line, on its own conversation: the CEO, Huey\./);
  assert.equal(line.undo, undo);
  assert.equal(line.isDone, false);
});

test("once every terminal restarted the notice can be dismissed", () => {
  const line = crossingLine(crossing([]));
  assert.ok(line);
  assert.match(line.text, /Nothing waits to restart under the new names\./);
  assert.equal(line.isDone, true);
});

test("what did not go as it should is said in the notice", () => {
  const line = crossingLine(crossing([], "The GitHub labels were not renamed yet (offline), and the board tries again when it next starts"));
  assert.ok(line);
  assert.match(line.text, /The GitHub labels were not renamed yet \(offline\), and the board tries again when it next starts\. The way back/);
});

test("a record that could not be read is said alone", () => {
  assert.deepEqual(crossingLine({ at: "0001-01-01T00:00:00Z", undo: "", waiting: [], problem: "The record could not be read" }), { text: "The record could not be read", undo: "", isDone: false });
});
