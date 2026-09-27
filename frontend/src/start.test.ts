import { test } from "node:test";
import assert from "node:assert/strict";
import { freeGigabytes, meterState, nextChip, startBlock } from "./start.ts";
import type { Memory, Task } from "./types.ts";

const GB = 2 ** 30;
const memory = (available: number): Memory => ({ available: available * GB, total: 32 * GB, floor: 3 * GB, next: 4 * GB });
const task = (changes: Partial<Task> = {}) => ({ id: "next-task", phase: "queued", brief: true, starting: false, start_error: "", ...changes }) as Task;

test("the meter says when the CFO starts the next task, and never that one starts by itself", () => {
  assert.deepEqual(meterState(memory(5.2)), { tone: "ready", text: "Enough memory: the CFO starts the next task." });
  assert.deepEqual(meterState(memory(3.1)), { tone: "waiting", text: "The CFO starts the next task at 4 GB free." });
  assert.deepEqual(meterState(memory(2.4)), { tone: "under", text: "Under the 3 GB floor: nothing starts until memory frees." });
});

test("Start is offered while a queued task can start, and otherwise says why not", () => {
  assert.equal(startBlock(task(), memory(5), false), "");
  assert.equal(startBlock(task(), memory(3.5), false), "", "Start now starts a task below the 4 GB mark, down to the floor");
  assert.equal(startBlock(task(), null, false), "", "a board that cannot read memory leaves the check to the supervisor");
  assert.equal(startBlock(task({ brief: false }), memory(5), false), "No brief yet: the CFO writes one before it can start");
  assert.equal(startBlock(task(), memory(2.9), false), "Under the 3 GB memory floor");
  assert.equal(startBlock(task(), memory(5), true), "Another task is starting");
  assert.equal(startBlock(task({ starting: true }), memory(5), true), "Starting", "its own start in flight");
});

test("free memory rounds down, so 2.97 GB never reads as the 3 GB floor", () => {
  assert.equal(freeGigabytes(2.97 * GB), "2.9");
  assert.equal(freeGigabytes(3 * GB), "3.0");
  assert.equal(freeGigabytes(5.26 * GB), "5.2");
});

test("the top queued task is marked next, waiting for 4 GB when memory is short", () => {
  assert.equal(nextChip(memory(5)), "Next up");
  assert.equal(nextChip(memory(3.2)), "Next, at 4 GB free");
  assert.equal(nextChip(null), "Next up");
});
