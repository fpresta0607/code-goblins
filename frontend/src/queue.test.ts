import { test } from "node:test";
import assert from "node:assert/strict";
import { queuedTasks } from "./workflow.ts";
import type { Snapshot, Task } from "./types.ts";

const task = (id: string, changes: Partial<Task> = {}) => ({ id, phase: "queued", generation: "", archived: false, verified: false, ...changes }) as Task;

test("the CFO's queue is every queued task, in the order the board ranks them", () => {
  const snapshot = { tasks: [task("live", { phase: "working", generation: "s1" }), task("second"), task("history", { archived: true, phase: "done" }), task("first-placed"), task("done", { phase: "done", verified: true, generation: "s2" })] } as Snapshot;
  assert.deepEqual(queuedTasks(snapshot).map((queued) => queued.id), ["second", "first-placed"]);
  assert.deepEqual(queuedTasks({ tasks: [] as Task[] } as Snapshot), []);
});
