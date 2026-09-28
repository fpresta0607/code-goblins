import { test } from "node:test";
import assert from "node:assert/strict";
import { freeGigabytes, meterScale, meterState, nextChip, refusalStands, startBlock, startOutcome } from "./start.ts";
import { parseSnapshot, type Memory, type Snapshot, type Task } from "./types.ts";

const GB = 2 ** 30;
const memory = (available: number): Memory => ({ available: available * GB, total: 32 * GB, floor: 4 * GB, next: 5 * GB });
const task = (changes: Partial<Task> = {}) => parseSnapshot({ healthy: true, tasks: [{ id: "next-task", phase: "queued", brief: true, starting: false, start_error: "", verified: false, ...changes }] }).tasks[0];

test("the meter says when the CFO starts the next task, and never that one starts by itself", () => {
  assert.deepEqual(meterState(memory(5.2)), { tone: "ready", text: "Enough memory: the CFO starts the next task." });
  assert.deepEqual(meterState(memory(4.1)), { tone: "waiting", text: "The CFO starts the next task at 5 GB free." });
  assert.deepEqual(meterState(memory(3.9)), { tone: "under", text: "Under the 4 GB floor: nothing starts until memory frees." });
});

test("the memory bar spans twice the start mark, so the floor and start marks sit apart and a full bar means the next task starts", () => {
  assert.deepEqual(meterScale(memory(4.5)), { fill: 45, floor: 40, next: 50 });
  assert.deepEqual(meterScale(memory(20)), { fill: 100, floor: 40, next: 50 }, "memory well past the mark fills the bar");
  assert.deepEqual(meterScale(memory(0)), { fill: 0, floor: 40, next: 50 });
  assert.deepEqual(meterScale({ ...memory(2), total: 5 * GB }), { fill: 40, floor: 80, next: 100 }, "a machine with less memory than that spans its own");
});

test("Start is offered while a queued task can start, and otherwise says why not", () => {
  assert.equal(startBlock(task(), memory(5), false), "");
  assert.equal(startBlock(task(), memory(4.9), false), "Needs 5 GB free to keep the 4 GB floor");
  assert.equal(startBlock(task(), null, false), "", "a board that cannot read memory leaves the check to the supervisor");
  assert.equal(startBlock(task({ brief: false }), memory(5), false), "");
  assert.equal(startBlock(task(), memory(3.9), false), "Needs 5 GB free to keep the 4 GB floor");
  assert.equal(startBlock(task(), memory(5), true), "Another task is starting");
  assert.equal(startBlock(task({ starting: true }), memory(5), true), "Starting", "its own start in flight");
});

test("free memory rounds down, so just under the floor or start mark never reads as ready", () => {
  assert.equal(freeGigabytes(3.97 * GB), "3.9");
  assert.equal(freeGigabytes(4.99 * GB), "4.9");
  assert.equal(freeGigabytes(5.26 * GB), "5.2");
});

test("the next eligible task waits for 5 GB when memory is short", () => {
  assert.equal(nextChip(memory(5)), "Next up");
  assert.equal(nextChip(memory(4.2)), "Next, at 5 GB free");
  assert.equal(nextChip(null), "Next up");
});

test("an accepted Start opens its goblin once its session is up, stops on a failure of this start, and otherwise waits", () => {
  // Arrange
  const snapshot = (revision: number, changes: Partial<Task>) => ({ revision, tasks: [task({ generation: "", ...changes })] }) as Snapshot;
  const accepted = { id: "next-task", revision: 12 };
  const cases: [string, Snapshot, "open" | "failed" | "wait"][] = [
    ["its session is up", snapshot(13, { phase: "running", generation: "gen-1" }), "open"],
    ["this start failed", snapshot(13, { start_error: "GITHUB_TOKEN is red" }), "failed"],
    ["this start failed, in the snapshot the start answered", snapshot(12, { start_error: "GITHUB_TOKEN is red" }), "failed"],
    ["a retry, where the older snapshot still shows the last start's failure", snapshot(11, { start_error: "GITHUB_TOKEN is red" }), "wait"],
    ["it is still starting", snapshot(13, { starting: true }), "wait"],
    ["the snapshot does not list it", { revision: 13, tasks: [] as Task[] } as Snapshot, "wait"],
  ];

  for (const [name, shown, want] of cases) {
    // Act
    const outcome = startOutcome(accepted, shown);

    // Assert
    assert.equal(outcome, want, name);
  }
});

test("a passing refusal lapses once a newer snapshot shows Start no longer blocked, and a standing one stays", () => {
  // Arrange
  const passing = { reason: "next-task is starting; start another once it is up", revision: 12, passing: true };
  const standing = { reason: "The brief for next-task names no project", revision: 12, passing: false };
  const cases: [string, typeof passing, number, string, boolean][] = [
    ["a passing refusal, in the snapshot it arrived at", passing, 12, "", true],
    ["a passing refusal, in an older snapshot", passing, 11, "", true],
    ["a passing refusal, in a newer snapshot that still blocks it", passing, 13, "Another task is starting", true],
    ["a passing refusal, in a newer snapshot in which it can start", passing, 13, "", false],
    ["a standing refusal, in a newer snapshot in which it can start", standing, 13, "", true],
    ["a standing refusal, many snapshots later", standing, 40, "", true],
  ];

  for (const [name, refusal, revision, blocked, want] of cases) {
    // Act
    const stands = refusalStands(refusal, { revision, tasks: [task()] } as Snapshot, blocked);

    // Assert
    assert.equal(stands, want, name);
  }
});
