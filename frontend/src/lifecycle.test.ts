import test from "node:test";
import assert from "node:assert/strict";
import { parseSnapshot } from "./types.ts";
import { completedCards, inProgressCards, nodeStatus, taskColumn } from "./workflow.ts";
import { startBlock } from "./start.ts";

test("paused and stopped tasks have explicit columns and status even without a live session", () => {
  for (const [phase, column, label] of [["paused", "Paused", "Paused"], ["pausing", "Paused", "Pausing"], ["resuming", "Paused", "Resuming"], ["stopped", "Completed", "Stopped"]]) {
    const task = parseSnapshot({ healthy: true, tasks: [{ id: "work", phase, verified: false, archived: phase === "stopped", merged: true }] }).tasks[0];
    assert.equal(taskColumn(task), column);
    assert.equal(nodeStatus({ id: task.id, title: "Work", task, relation: "" }), label);
  }
});

test("a start the Overlord made heads In progress and one the supervisor made by itself joins its bottom", () => {
  const tasks = parseSnapshot({ healthy: true, tasks: [
    { id: "his-first", phase: "working", verified: false },
    { id: "automatic", phase: "queued", verified: false, starting: true },
    { id: "his-second", phase: "working", verified: false },
    { id: "his-start", phase: "queued", verified: false, starting: true, overlord_start: true },
  ] }).tasks;
  assert.deepEqual(inProgressCards(tasks).map((task) => task.id), ["his-start", "his-first", "his-second", "automatic"]);
});

// The Overlord, 2026-10-15: "top of completed is always most recently
// completed work".
test("Completed lists the most recently finished work first", () => {
  const tasks = parseSnapshot({ healthy: true, tasks: [
    { id: "landed-live", phase: "done", verified: true, at: "2026-10-15T12:40:00Z", reported_at: "2026-10-15T11:00:00Z" },
    { id: "finished:cleaned-late", phase: "done", verified: false, archived: true, at: "2026-10-15T12:50:00Z", reported_at: "2026-10-15T09:00:00Z" },
    { id: "finished:stopped", phase: "stopped", verified: false, archived: true, at: "2026-10-15T10:00:00Z", reported_at: "2026-10-15T12:55:00Z" },
    { id: "merged:https://github.com/o/r/pull/9", phase: "done", verified: false, archived: true, at: "2026-10-15T12:00:00Z", reported_at: "0001-01-01T00:00:00Z" },
  ] }).tasks;
  assert.deepEqual(completedCards(tasks).map((task) => task.id), ["merged:https://github.com/o/r/pull/9", "landed-live", "finished:stopped", "finished:cleaned-late"]);
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
