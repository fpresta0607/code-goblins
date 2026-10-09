import { test } from "node:test";
import assert from "node:assert/strict";
import { taskStatus } from "./task-status.ts";
import { withClicks } from "./task-clicks.ts";
import { idleView } from "./terminalOrder.ts";
import { taskSummary } from "./task-words.ts";
import { parseSnapshot, type Snapshot } from "./types.ts";
import { taskColumn, workflowNodes } from "./workflow.ts";

// The Overlord, 2026-10-08, of a Tasks card reading "Not started" with Start
// while the supervisor had already started Zane, and of Zane's panel with no
// Start: "in queue should say 'Queued' ... when its starting... it displays
// where the status dot is in the board panel as well status dots need to be
// better synched across pane and panels and boards". Every surface reads one
// status for a task, its words and its dot, from the snapshot.
const GB = 2 ** 30;
const memoryWith = (available: number) => ({ next: 5 * GB, floor: 4 * GB, total: 32 * GB, available: available * GB, commit_limit: 48 * GB, commit_available: 40 * GB, paged_pool: 0, nonpaged_pool: 0 });
const board = (task: Record<string, unknown>, available = 9): Snapshot => parseSnapshot({
  healthy: true, revision: 7, memory: memoryWith(available),
  tasks: [{ id: "zane", title: "Train history", project: "code-goblins", verified: false, brief: true, ...task }],
});
const zane = (snapshot: Snapshot) => snapshot.tasks[0];

test("a queued task reads Queued, with the queued dot, and waits in Tasks", () => {
  // Arrange
  const snapshot = board({ phase: "queued" });

  // Act
  const status = taskStatus(zane(snapshot), snapshot);

  // Assert
  assert.deepEqual(status, { text: "Queued", phase: "queued" });
  assert.equal(taskColumn(zane(snapshot)), "Tasks");
});

test("a task the scheduler starts reads Starting at a working dot, leaves Tasks at once and is on the canvas", () => {
  // Arrange: the supervisor's own start, which no click asked for.
  const snapshot = board({ phase: "queued", starting: true });

  // Act
  const status = taskStatus(zane(snapshot), snapshot);

  // Assert
  assert.deepEqual(status, { text: "Starting", phase: "started" });
  assert.equal(taskColumn(zane(snapshot)), "In progress");
  assert.deepEqual(workflowNodes(snapshot).filter((node) => node.task?.id === "zane").map((node) => node.id), ["task:zane"]);
});

test("a Start clicked reads Starting in the frame he clicks, where he clicked it, until the supervisor takes it", () => {
  // Arrange
  const queued = board({ phase: "queued" });

  // Act
  const clicked = withClicks(queued, new Map([["zane", { action: "start", revision: null }]]));

  // Assert
  assert.deepEqual(taskStatus(zane(clicked), clicked), { text: "Starting", phase: "started" });
  assert.equal(taskColumn(zane(clicked)), "Tasks", "a click on its way stays in Tasks, so the card he clicked does not jump");
  const taken = board({ phase: "queued", starting: true });
  assert.equal(taskColumn(zane(taken)), "In progress", "the supervisor took it: it leaves Tasks");
});

test("a Start that waits its turn for memory says when it starts, and stays in Tasks", () => {
  // Arrange
  const snapshot = board({ phase: "queued", starting: true, asked: true }, 4.2);

  // Act
  const status = taskStatus(zane(snapshot), snapshot);

  // Assert
  assert.deepEqual(status, { text: "Starts at 5 GB free", phase: "queued" });
  assert.equal(taskColumn(zane(snapshot)), "Tasks");
});

test("a start that failed reads Start failed at a red dot, never a yellow one, and waits in Tasks for Start", () => {
  // Arrange
  const snapshot = board({ phase: "queued", start_error: "cfo spawn: the brief names no project" });

  // Act
  const status = taskStatus(zane(snapshot), snapshot);

  // Assert
  assert.deepEqual(status, { text: "Start failed", phase: "failed" });
  assert.equal(taskColumn(zane(snapshot)), "Tasks");
});

test("a failed start's panel says nothing under it and keeps why behind Details", () => {
  const snapshot = board({ phase: "queued", start_error: "cfo spawn: the brief names no project" });
  assert.deepEqual(taskSummary(zane(snapshot), snapshot.tasks, "Start failed"), { sentence: "", details: ["cfo spawn: the brief names no project"], isFailure: false });
});

test("a new Start of a task whose last start failed reads Starting", () => {
  const snapshot = board({ phase: "queued", start_error: "cfo spawn: no brief", starting: true, asked: true });
  assert.deepEqual(taskStatus(zane(snapshot), snapshot), { text: "Starting", phase: "started" });
});

test("a started goblin reads Starting until its spawn ends, then what it does", () => {
  const starting = board({ phase: "unknown", generation: "s1", starting: true });
  const working = board({ phase: "working", generation: "s1" });
  assert.deepEqual(taskStatus(zane(starting), starting), { text: "Starting", phase: "started" });
  assert.deepEqual(taskStatus(zane(working), working), { text: "Working", phase: "working" });
});

// On a scratch board, 2026-10-08: a goblin read "unknown" for 9 seconds
// between the end of its spawn and its first native activity.
test("a goblin whose spawn ended reads Starting until the supervisor has evidence of it, and not past its startup", () => {
  const since = "2026-10-08T23:05:22Z";
  const read = (phase: string, seconds: number) => {
    const snapshot = parseSnapshot({ healthy: true, revision: 9, at: new Date(Date.parse(since) + seconds * 1000).toISOString(), tasks: [{ id: "zane", title: "Train history", phase, generation: "s1", since, verified: false }] });
    return taskStatus(zane(snapshot), snapshot);
  };
  assert.deepEqual(read("unknown", 50), { text: "Starting", phase: "started" });
  assert.deepEqual(read("unavailable", 50), { text: "Starting", phase: "started" });
  assert.deepEqual(read("working", 60), { text: "Working", phase: "working" });
  assert.deepEqual(read("unknown", 180), { text: "No evidence yet", phase: "unknown" });
});

test("a paused goblin reads why it waits and what resumes it, the same words on every surface", () => {
  const snapshot = board({ phase: "paused", generation: "s1", lifecycle: { phase: "paused", action: "pause", at: "2026-10-08T11:00:00Z", kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false, pause: { reason: "memory", until: "", at: "2026-10-08T11:00:00Z" } } });
  assert.deepEqual(taskStatus(zane(snapshot), snapshot), { text: "Memory: resumes at 5 GB free", phase: "paused" });
});

test("a task with no terminal yet says its status where its terminal will be", () => {
  const queued = board({ phase: "queued" });
  const starting = board({ phase: "queued", starting: true });
  assert.equal(idleView(zane(queued), undefined, taskStatus(zane(queued), queued).text).text, "Queued");
  assert.equal(idleView(zane(starting), undefined, taskStatus(zane(starting), starting).text).text, "Starting");
});
