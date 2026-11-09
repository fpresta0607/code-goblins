import assert from "node:assert/strict";
import test from "node:test";
import { taskSessionDetails } from "./lineageTree.ts";
import { parseSnapshot } from "./types.ts";

for (const state of ["busy", "active", "idle", "parked", "unknown", "unavailable", "stale"]) {
  test(`session details preserve ${state} terminal evidence`, () => {
    const task = parseSnapshot({ healthy: true, tasks: [{ id: "native", backend: "native", runtime: { state }, verified: false }] }).tasks[0];
    const expected = ["unknown", "unavailable", "stale"].includes(state) ? "Session details unavailable" : "Native terminal running, session details unavailable";
    assert.equal(taskSessionDetails(task), expected);
  });
}
