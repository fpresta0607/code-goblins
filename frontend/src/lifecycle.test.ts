import test from "node:test";
import assert from "node:assert/strict";
import { parseSnapshot } from "./types.ts";
import { nodeStatus, taskColumn } from "./workflow.ts";
import { startBlock } from "./start.ts";

test("paused and stopped tasks have explicit columns and status even without a live session", () => {
  for (const [phase, column, label] of [["paused", "Paused", "Paused"], ["pausing", "Paused", "Pausing"], ["resuming", "Paused", "Resuming"], ["stopped", "Completed", "Stopped"]]) {
    const task = parseSnapshot({ healthy: true, tasks: [{ id: "work", phase, verified: false, archived: phase === "stopped", merged: true }] }).tasks[0];
    assert.equal(taskColumn(task), column);
    assert.equal(nodeStatus({ id: task.id, title: "Work", task, relation: "" }), label);
  }
});

test("Start creates a missing brief but refuses dependency and memory blocks", () => {
  const task = parseSnapshot({ healthy: true, tasks: [{ id: "work", verified: false, phase: "queued", brief: false }] }).tasks[0];
  const memory = { available: 5 * 2 ** 30, total: 32 * 2 ** 30, floor: 4 * 2 ** 30, next: 5 * 2 ** 30 };
  assert.equal(startBlock(task, memory, false), "");
  assert.equal(startBlock({ ...task, reason: "Brief ready at data/work/brief.md; not dispatched yet", brief: true }, memory, false), "");
  assert.match(startBlock({ ...task, reason: "Waiting on the Overlord", dependencies: ["overlord"] }, memory, false), /Overlord/);
  assert.match(startBlock(task, { ...memory, available: memory.next - 1 }, false), /5 GB/);
});

test("snapshot preserves lifecycle results and queued adjustment revision and notes", () => {
  const lifecycle = { phase: "paused", action: "pause", at: "2026-09-28T12:00:00Z", kept: ["branch"], stopped: ["browser"], teardown: ["chrome.exe pid 42"], problems: ["No handoff saved"], handoff_saved: false, validation_restarts: true };
  const task = parseSnapshot({ healthy: true, tasks: [{ id: "work", verified: false, lifecycle, detail: "Task detail", queue_revision: "revision", notes: ["Please adjust this"], action_error: "Could not stop server" }] }).tasks[0];
  assert.deepEqual(task.lifecycle, lifecycle);
  assert.equal(task.queue_revision, "revision");
  assert.deepEqual(task.notes, ["Please adjust this"]);
  assert.equal(task.action_error, "Could not stop server");
});
