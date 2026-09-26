import { test } from "node:test";
import assert from "node:assert/strict";
import { windowTarget } from "./terminalWindow.ts";
import type { Session, Snapshot, Task } from "./types.ts";

const task = (changes: Partial<Task> = {}) => ({ id: "task-1", generation: "g1", session: "s1", backend: "herdr", ...changes }) as Task;
const owner = { id: "s1", task_id: "task-1", generation: "g1", role: "goblin", parent_id: "" } as unknown as Session;
const snapshot = (changes: Partial<Snapshot> = {}) => ({ cfo_terminal: "", sessions: [owner], ...changes }) as Snapshot;

test("Open in terminal names the terminal shown, as its own view names it", () => {
  const cases: [string, Snapshot, boolean, Task | undefined, object | null][] = [
    ["the CFO in Herdr", snapshot(), true, undefined, {}],
    ["the CFO in a native terminal", snapshot({ cfo_terminal: "cfo" }), true, undefined, { native: "cfo=cfo" }],
    ["a goblin in Herdr, by its owning session", snapshot(), false, task(), { task: "task-1", session: "s1", generation: "g1" }],
    ["a goblin in a native terminal", snapshot(), false, task({ backend: "native" }), { native: "task=task-1&generation=g1" }],
    ["a task not started has no terminal", snapshot(), false, task({ generation: "" }), null],
    ["nothing shown", snapshot(), false, undefined, null],
  ];
  for (const [name, shown, cfo, selected, want] of cases) assert.deepEqual(windowTarget(shown, cfo, selected), want, name);
});
