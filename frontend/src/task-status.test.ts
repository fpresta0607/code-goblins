import { test } from "node:test";
import assert from "node:assert/strict";
import { taskStatus } from "./task-status.ts";
import { startBlock } from "./start.ts";
import { withClicks } from "./task-clicks.ts";
import { idleView } from "./terminalOrder.ts";
import { taskSummary } from "./task-words.ts";
import { parseSnapshot, type Snapshot } from "./types.ts";
import { nodeStatus, taskColumn, workflowNodes } from "./workflow.ts";

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

for (const [phase, step, reason, runtime, text] of [
  ["blocked", "rebase", "Pipeline decision required at rebase; use cfo pipeline respond", "busy", "Waiting on the CEO: rebase gate"],
  ["blocked", "rebase", "Pipeline decision required at rebase; use cfo pipeline respond", "parked", "Waiting on the CEO: rebase gate"],
  ["review", "test", "Pipeline evidence is incomplete or belongs to a different HEAD", "busy", "In review gate: tests"],
  ["review", "test", "Pipeline evidence is incomplete or belongs to a different HEAD", "parked", "In review gate: tests"],
  ["blocked", "test", "Waiting on the CFO: Which source should ship?", "busy", "Waiting on the CEO"],
] as const) {
  test(`gate ${step}/${phase} keeps its gate status beside a ${runtime} terminal`, () => {
    const snapshot = board({ phase, gate_step: step, reason, report: "working", runtime: { state: runtime } });
    const task = zane(snapshot);

    const status = taskStatus(task, snapshot);

    assert.deepEqual(status, { text, phase });
  });
}

for (const [name, reason, text] of [
  ["a gate", "Review gate needs approval before continuing", "Waiting on the CEO"],
  ["a dependency", "Wait for the dependency task to land before continuing", "Waiting on the CEO"],
  ["the CEO", "Waiting on the CFO: Which dependency should land first?", "Waiting on the CEO"],
]) {
  test(`a blocked goblin awaiting ${name} reads Waiting on the CFO, who answers it, and keeps its reason and phase`, () => {
    // Arrange
    const snapshot = board({ phase: "blocked", report: "blocked", reason });
    const task = zane(snapshot);

    // Act
    const status = taskStatus(task, snapshot);
    const node = workflowNodes(snapshot).find((node) => node.task?.id === task.id)!;
    const summary = taskSummary(task, snapshot.tasks, status.text);

    // Assert
    assert.deepEqual(status, { text, phase: "blocked" });
    assert.equal(nodeStatus(node, false, snapshot.tasks), text);
    assert.equal(taskColumn(task), "In progress");
    assert.equal(task.phase, "blocked");
    assert.equal(task.reason, reason);
    assert.equal(summary.isFailure, false);
    assert.ok([summary.sentence, ...summary.details].join(" ").includes(reason.replace("Waiting on the CFO: ", "")));
  });
}

test("a queued task reads Queued, with the queued dot, and waits in Tasks", () => {
  // Arrange
  const snapshot = board({ phase: "queued" });

  // Act
  const status = taskStatus(zane(snapshot), snapshot);

  // Assert
  assert.deepEqual(status, { text: "Queued", phase: "queued" });
  assert.equal(taskColumn(zane(snapshot)), "Tasks");
});

// The Overlord, 2026-10-09, of Tasks holding Toby, Nina and Murray as Queued
// while memory was free: a row that waits says what for in a few words, on
// its card, its panel and its Start, never Queued alone, and at the queued
// dot, never yellow.
test("a waiting row says what it waits for in a few words, at the queued dot", () => {
  // Arrange
  const until = "2026-10-10T00:00:00Z";
  const starts = "Starts " + new Date(until).toLocaleDateString(undefined, { month: "short", day: "numeric" });
  const nina = { id: "other-task", title: "Other work", phase: "working", generation: "g1", goblin_name: "Nina", goblin_title: "Bug Hunter", verified: false };
  const cases: [string, Record<string, unknown>[], string][] = [
    ["free memory", [{ kind: "memory", target: "memory 12 GB", bytes: 12 * GB }], "Waits for 12 GB free"],
    ["free memory in tenths", [{ kind: "memory", target: "memory 1.5 GB", bytes: 1.5 * GB }], "Waits for 1.5 GB free"],
    ["a time", [{ kind: "time", target: "until 2026-10-10T00:00Z", until }], starts],
    ["a task, by its goblin's name", [{ kind: "task", target: "other-task" }], "Waits for Nina - Bug Hunter"],
    ["a task the board does not show, by its id", [{ kind: "task", target: "later-task" }], "Waits for later-task"],
    ["a pull request", [{ kind: "pr", target: "https://github.com/o/r/pull/7" }], "Waits for PR #7 to merge"],
    ["a hold", [{ kind: "hold", target: "Wait for the current board task to land" }], "Held: Wait for the current board task to land"],
    ["a wait it cannot read", [{ kind: "task", target: "quiet-night", problem: "No task is named \"quiet-night\"" }], "No task is named \"quiet-night\""],
    ["more than one wait", [{ kind: "memory", target: "memory 12 GB", bytes: 12 * GB }, { kind: "task", target: "other-task" }], "Waits for 12 GB free, and 1 more"],
  ];

  for (const [name, waits, want] of cases) {
    const snapshot = parseSnapshot({ healthy: true, revision: 7, memory: memoryWith(9), tasks: [{ id: "zane", title: "Train history", project: "code-goblins", verified: false, brief: true, phase: "queued", waits }, nina] });

    // Act
    const status = taskStatus(zane(snapshot), snapshot);

    // Assert
    assert.deepEqual(status, { text: want, phase: "queued" }, name);
    assert.notEqual(startBlock(zane(snapshot)), "", name + ": its Start says why it does not start");
  }
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

test("a paused goblin reads what resumes it, the same words on every surface", () => {
  const snapshot = board({ phase: "paused", generation: "s1", lifecycle: { phase: "paused", action: "pause", at: "2026-10-08T11:00:00Z", kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false, pause: { reason: "memory", until: "", at: "2026-10-08T11:00:00Z" } } });
  assert.deepEqual(taskStatus(zane(snapshot), snapshot), { text: "Paused until memory frees", phase: "paused" });
});

// The Overlord, 2026-11-02: "How is a goblin waiting on memory yet it's
// still in progress?" A live goblin whose heavy step waited its turn in the
// machine's line read Waiting on memory in In progress, and one the memory
// reaper stopped read Paused for memory. Every goblin reads one state from a
// small set that says whether its session runs, what it waits for and what
// brings it back, in its column (#1924).
test("every phase and wait of a goblin reads one state, in its column, and only a stopped goblin reads Paused", () => {
  // Arrange
  const TRAIN_PR = "https://github.com/o/code-goblins/pull/900", PR = "https://github.com/o/code-goblins/pull/512";
  const nina = { id: "nina-task", title: "Other work", phase: "working", generation: "n1", goblin_name: "Nina", goblin_title: "Bug Hunter", verified: false };
  const tree = (place: number) => ({ tree: { task_id: "zane", generation: "s1", harness: "claude", memory: 0, own_memory: 0, children: [], line_place: place } });
  const pause = (fields: Record<string, unknown>) => ({ phase: "paused", lifecycle: { phase: "paused", action: "pause", at: "2026-11-02T19:47:00Z", kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false, pause: { until: "", at: "2026-11-02T19:47:00Z", ...fields } } });
  const done = (fields: Record<string, unknown>) => ({ phase: "idle", report: "done", pr: PR, ...fields });
  const checks = (state: string) => ({ hosted_checks: { head: "a".repeat(40), state, checks: 9, failed: state === "failed" ? ["test"] : [] } });
  const riding = { id: "t1", repository: "o/code-goblins", base: "main", pr: TRAIN_PR, state: "testing", runs: 1, started: "2026-11-02T19:00:00Z", finished: "", note: "",
    cars: [{ number: 512, url: PR, title: "", task: "zane", goblin: "", goblin_title: "", head: "a".repeat(40), state: "riding", note: "" }] };
  const asksYou = { reviews: [{ id: "waiting-zane-1", identity: "g", task: "zane", title: "Waiting on you: sign in to the registry", state: "open", created_at: "2026-11-02T19:40:00Z", updated_at: "2026-11-02T19:40:00Z" }] };
  const cases: [string, Record<string, unknown>, Record<string, unknown>, string, string, string][] = [
    ["at work", { phase: "working" }, {}, "Working", "working", "In progress"],
    ["its heavy step in line, place unread", { phase: "waiting", waiting_on: "memory" }, {}, "In line for the machine", "waiting", "In progress"],
    ["its heavy step second in line", { phase: "waiting", waiting_on: "memory", ...tree(2) }, {}, "2nd in line for the machine", "waiting", "In progress"],
    ["idle while its run is next in line", { phase: "idle", ...tree(1) }, {}, "1st in line for the machine", "waiting", "In progress"],
    ["eleventh in line", { phase: "waiting", waiting_on: "memory", ...tree(11) }, {}, "11th in line for the machine", "waiting", "In progress"],
    ["twenty-third in line", { phase: "waiting", waiting_on: "memory", ...tree(23) }, {}, "23rd in line for the machine", "waiting", "In progress"],
    ["its heavy step holding the turn, or none of its runs in line", { phase: "waiting", waiting_on: "memory", ...tree(0) }, {}, "Working", "working", "In progress"],
    ["idle, nothing in line", { phase: "idle" }, {}, "Waiting on the CEO", "idle", "In progress"],
    ["idle, its tree read with nothing in line", { phase: "idle", ...tree(0) }, {}, "Waiting on the CEO", "idle", "In progress"],
    ["asking a question", { phase: "blocked", report: "blocked", reason: "Which key?" }, {}, "Waiting on the CEO", "blocked", "In progress"],
    ["a helper asking its parent", { phase: "blocked", report: "blocked", reason: "Which key?", parent: "nina-task" }, {}, "Waiting on Nina", "blocked", "In progress"],
    ["an idle helper whose parent left the board", { phase: "idle", parent: "gone-task" }, {}, "Waiting on its parent", "idle", "In progress"],
    ["waiting on the Overlord through the CEO", { phase: "waiting", waiting_on: "overlord" }, {}, "Waiting on the CEO", "waiting", "In progress"],
    ["waiting on the Overlord in his Command Center", { phase: "waiting", waiting_on: "overlord" }, asksYou, "Waiting on you", "waiting", "In progress"],
    ["waiting on CI", { phase: "waiting", waiting_on: "ci" }, {}, "Waiting on CI", "waiting", "In progress"],
    ["waiting on its deploy", { phase: "waiting", waiting_on: "deploy" }, {}, "Waiting on its deploy", "waiting", "In progress"],
    ["waiting on a goblin", { phase: "waiting", waiting_on: "nina-task" }, {}, "Waiting on Nina", "waiting", "In progress"],
    ["waiting on a task gone from the board", { phase: "waiting", waiting_on: "later-task" }, {}, "Waiting on later-task", "waiting", "In progress"],
    ["waiting on its gate run", { phase: "waiting", waiting_on: "01M65HF9WKBPKK3F15TEPT3NYZ" }, {}, "Waiting on its gate", "waiting", "In progress"],
    ["in its review gate", { phase: "review", gate_step: "test" }, {}, "In review gate: tests", "review", "In progress"],
    ["done, its pull request on a train", done(checks("passed")), { merge_trains: [riding] }, "Landing on a train", "pr-pending", "In progress"],
    ["done, its checks running", done(checks("pending")), {}, "Landing, waits to merge", "pr-pending", "In progress"],
    ["done, its checks passed", done(checks("passed")), {}, "Landing, waits to merge", "pr-passed", "In progress"],
    ["done, its checks failed", done(checks("failed")), {}, "Checks failed", "pr-failed", "In progress"],
    ["done, its pull request with no checks recorded", done({}), {}, "Landing, waits to merge", "idle", "In progress"],
    ["done, its pull request merged with checks passed", done({ merged: true, ...checks("passed") }), {}, "Merged, verifying", "merged", "In progress"],
    ["delivered, its merge unverified", { phase: "done", report: "done", pr: PR }, {}, "Landing, waits to merge", "done", "In progress"],
    ["delivered, its pull request merged and unverified", { phase: "done", report: "done", pr: PR, merged: true }, {}, "Merged, verifying", "merged", "In progress"],
    ["paused for memory", pause({ reason: "memory" }), {}, "Paused until memory frees", "paused", "Paused"],
    ["paused by the Overlord", pause({ reason: "overlord" }), {}, "Paused until you resume it", "paused", "Paused"],
    ["paused for the PC's restart", pause({ reason: "restart", until: "2026-11-02T08:00:00Z" }), {}, "Paused until after the restart", "paused", "Paused"],
    ["paused until a goblin delivers", pause({ reason: "dependency", until: "task:nina-task" }), {}, "Paused until Nina delivers", "paused", "Paused"],
    ["paused until a pull request merges", pause({ reason: "dependency", until: "pr:" + PR }), {}, "Paused until PR #512 merges", "paused", "Paused"],
  ];

  for (const [name, fields, extra, text, phase, column] of cases) {
    const snapshot = parseSnapshot({ healthy: true, revision: 7, memory: memoryWith(9), ...extra,
      tasks: [{ id: "zane", title: "Train history", project: "code-goblins", generation: "s1", verified: false, ...fields }, nina] });

    // Act
    const status = taskStatus(zane(snapshot), snapshot);

    // Assert
    assert.deepEqual(status, { text, phase }, name);
    assert.equal(taskColumn(zane(snapshot)), column, name);
    assert.equal(status.text.startsWith("Paused"), column === "Paused", name + ": only a stopped goblin reads Paused, and never under In progress");
    assert.doesNotMatch(status.text, /memory$/, name + ": never the word memory alone");
  }
});

test("a task with no terminal yet says its status where its terminal will be", () => {
  const queued = board({ phase: "queued" });
  const starting = board({ phase: "queued", starting: true });
  assert.equal(idleView(zane(queued), undefined, taskStatus(zane(queued), queued).text).text, "Queued");
  assert.equal(idleView(zane(starting), undefined, taskStatus(zane(starting), starting).text).text, "Starting");
});

test("a task that closed its issue reads Done and stays in progress until cleanup retires it", () => {
  // Arrange
  const snapshot = board({ phase: "done", report: "done", reason: "closed https://github.com/o/r/issues/1450 (decision 7: Close it, no PR)", closed_issue: "https://github.com/o/r/issues/1450", runtime: { state: "idle" } });

  // Act
  const status = taskStatus(zane(snapshot), snapshot);

  // Assert
  assert.deepEqual(status, { text: "Done", phase: "done" });
  assert.equal(taskColumn(zane(snapshot)), "In progress");
});

test("a completed helper reports its parent handoff without claiming verification", () => {
  const snapshot = board({ parent: "parent-1", phase: "done", report: "done", reason: "ready for parent-1 to merge", runtime: { state: "idle" } });
  const ordinary = board({ phase: "done", report: "done", runtime: { state: "idle" } });

  const status = taskStatus(zane(snapshot), snapshot);

  assert.deepEqual(status, { text: "Done, awaiting parent", phase: "done" });
  assert.deepEqual(taskStatus(zane(ordinary), ordinary), { text: "Done, verifying", phase: "done" });
  assert.equal(taskColumn(zane(snapshot)), "In progress", "unverified handoff work stays in progress");
  assert.equal(taskColumn(zane(ordinary)), "In progress");
});
