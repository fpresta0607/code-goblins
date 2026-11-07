import assert from "node:assert/strict";
import test from "node:test";
import { ownsTaskSession, taskSessionDetails, tasksWithoutSession } from "./lineageTree.ts";
import { parseSnapshot } from "./types.ts";

test("a session with another task's identity cannot own a matching generation", () => {
  const snapshot = parseSnapshot({ healthy: true, tasks: [{ id: "one", session: "codex/shared", generation: "g1", verified: false }], sessions: [{ id: "codex/shared", task_id: "two", generation: "g1", role: "goblin" }] });
  assert.equal(ownsTaskSession(snapshot.sessions[0], snapshot.tasks[0]), false);
  assert.deepEqual(tasksWithoutSession(snapshot.tasks, snapshot.sessions), snapshot.tasks);
});

for (const state of ["busy", "active", "idle", "parked", "unknown", "unavailable", "stale"]) {
  test(`session details preserve ${state} terminal evidence`, () => {
    const task = parseSnapshot({ healthy: true, tasks: [{ id: "native", backend: "native", runtime: { state }, verified: false }] }).tasks[0];
    const expected = ["unknown", "unavailable", "stale"].includes(state) ? "Session details unavailable" : "Native terminal running; session details unavailable";
    assert.equal(taskSessionDetails(task), expected);
  });
}
