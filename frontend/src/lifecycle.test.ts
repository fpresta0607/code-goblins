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

test("Start creates a missing brief but refuses a dependency block", () => {
  const task = parseSnapshot({ healthy: true, tasks: [{ id: "work", verified: false, phase: "queued", brief: false }] }).tasks[0];
  assert.equal(startBlock(task), "");
  assert.equal(startBlock({ ...task, reason: "Brief ready at data/work/brief.md; not dispatched yet", brief: true }), "");
  assert.equal(startBlock({ ...task, reason: "his call", waits: [{ kind: "task", target: "overlord", until: "", bytes: 0, problem: "No task is named \"overlord\"" }] }), "No task is named \"overlord\"");
});

test("snapshot preserves lifecycle results and queued adjustment revision", () => {
  const lifecycle = { phase: "paused", action: "pause", at: "2026-09-28T12:00:00Z", kept: ["branch"], stopped: ["browser"], problems: ["No handoff saved"], handoff_saved: false, validation_restarts: true };
  const task = parseSnapshot({ healthy: true, tasks: [{ id: "work", verified: false, lifecycle, teardown: ["chrome.exe pid 42"], detail: "Task detail", queue_revision: "revision", action_error: "Could not stop server" }] }).tasks[0];
  assert.deepEqual(task.lifecycle, lifecycle);
  const pause = { reason: "allowance", until: "2026-10-09T22:27:00Z", at: "2026-10-05T16:30:06Z" };
  assert.deepEqual(parseSnapshot({ healthy: true, tasks: [{ id: "work", verified: false, lifecycle: { ...lifecycle, pause } }] }).tasks[0].lifecycle?.pause, pause);
  assert.deepEqual(task.teardown, ["chrome.exe pid 42"]);
  assert.equal(task.queue_revision, "revision");
  assert.equal(task.action_error, "Could not stop server");
});
