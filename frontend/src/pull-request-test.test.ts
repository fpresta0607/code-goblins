import assert from "node:assert/strict";
import test from "node:test";
import { pullRequestTest } from "./pull-request-test.ts";
import { taskSummary } from "./task-words.ts";
import { parseSnapshot, type MergeTrain, type Task } from "./types.ts";
import { nodeStatus, statusPhase, taskColumn } from "./workflow.ts";

// The Overlord, 2026-10-08, of Sid's pull request #512: the train's card read
// "CI tests #512" and "#512 Sid - Memory Keeper Testing" while Sid's panel
// read Paused in yellow, "It resumes by itself when PR #512 merges." and
// "Checks passed". A goblin whose pull request is under test reads one state
// with one link on its card, its panel, its canvas node and its train's card,
// and stays in In progress whether its session runs or was paused until the
// pull request merges.
const REPO = "https://github.com/o/code-goblins";
const PR = REPO + "/pull/512";
const TRAIN_PR = REPO + "/pull/900";
const RUN = REPO + "/actions/runs/77/job/770";
const HEAD = "a".repeat(40);
const lifecycle = (pause: Record<string, unknown>, phase = "paused") => ({ phase, action: "pause", at: "2026-10-08T18:00:00Z", kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false, pause: { at: "2026-10-08T18:00:00Z", ...pause } });
const sid = (fields: Record<string, unknown> = {}) => ({ id: "cg-sid", title: "Memory keeper", goblin_name: "Sid", goblin_title: "Memory Keeper", project: "code-goblins", phase: "idle", report: "done", pr: PR, generation: "cg-sid-1", verified: false, ...fields });
const pausedUntilMerge = (fields: Record<string, unknown> = {}) => sid({ phase: "paused", lifecycle: lifecycle({ reason: "dependency", until: "pr:" + PR }), ...fields });
const pausedForCI = (fields: Record<string, unknown> = {}) => sid({ phase: "paused", lifecycle: lifecycle({ reason: "ci", until: "pr:" + PR + "@" + HEAD }), ...fields });
const checks = (state: string, fields: Record<string, unknown> = {}) => ({ hosted_checks: { head: HEAD, state, checks: 9, failed: [], ...fields } });
const train = (state: string, carState: string, fields: Record<string, unknown> = {}) => ({
  id: "code-goblins-20261008-180000", repository: "o/code-goblins", base: "main", pr: TRAIN_PR, state, runs: 1, started: "2026-10-08T18:10:00Z", finished: state === "testing" ? "" : "2026-10-08T18:40:00Z", note: "",
  cars: [{ number: 512, url: PR, title: "Keep memory", task: "cg-sid", goblin: "Sid", goblin_title: "Memory Keeper", head: HEAD, state: carState, note: "" }], ...fields,
});
const board = (task: Record<string, unknown>, trains: object[] = []): { task: Task; trains: MergeTrain[] } => {
  const snapshot = parseSnapshot({ healthy: true, tasks: [task], merge_trains: trains });
  return { task: snapshot.tasks[0], trains: snapshot.merge_trains ?? [] };
};
// What a goblin reads on each surface: its card's and canvas node's status,
// the colour it is drawn in, its column, the line under its panel's status
// and its pull request's chip.
const surfaces = ({ task, trains }: { task: Task; trains: MergeTrain[] }) => ({
  status: nodeStatus({ id: task.id, title: "", task, relation: "" }, false, [task], trains),
  phase: statusPhase(task, trains),
  column: taskColumn(task),
  line: taskSummary(task, [task]).sentence,
  chip: pullRequestTest(task, trains),
});

test("a goblin whose pull request rides a merge train reads the train's state with the train's link, live or paused until it merges", () => {
  const testing = train("testing", "riding");
  for (const [name, goblin] of [
    ["live, its pull request reported done", sid(checks("passed"))],
    ["paused until its pull request merges", pausedUntilMerge(checks("passed"))],
    ["paused until its CI run on the pull request finishes", pausedForCI(checks("passed"))],
  ] as const) {
    assert.deepEqual(surfaces(board(goblin, [testing])), {
      status: "Testing", phase: "pr-pending", column: "In progress", line: "",
      chip: { text: "Testing", tone: "pending", url: TRAIN_PR, tip: "CI tests #512" },
    }, name);
  }
});

test("the train's outcome is the goblin's: Landed, or what broke, with the train's link", () => {
  for (const [name, given, status, phase] of [
    ["landed", train("landed", "landed"), "Landed", "pr-passed"],
    ["broke CI", train("stopped", "culprit", { note: "#512 breaks CI on main: test" }), "Breaks CI", "pr-failed"],
    ["its half still waits", train("testing", "waiting"), "Waits its turn", "pr-pending"],
  ] as const) {
    const read = surfaces(board(pausedUntilMerge(checks("passed")), [given]));
    assert.equal(read.status, status, name);
    assert.equal(read.phase, phase, name);
    assert.equal(read.column, "In progress", name);
    assert.equal(read.chip?.text, status, name);
    assert.equal(read.chip?.url, TRAIN_PR, name);
  }
});

test("a finished train no longer speaks for a pull request whose head moved since it rode, and a running one comes before a finished one", () => {
  const moved = board(pausedUntilMerge(checks("pending", { head: "b".repeat(40), link: RUN })), [train("stopped", "culprit")]);
  assert.equal(surfaces(moved).status, "Testing");
  assert.equal(surfaces(moved).chip?.url, RUN);

  const older = { ...train("stopped", "returned"), id: "code-goblins-20261008-120000", pr: REPO + "/pull/899" };
  const both = board(pausedUntilMerge(checks("passed")), [older, train("testing", "riding")]);
  assert.equal(surfaces(both).chip?.url, TRAIN_PR);
});

test("a goblin waiting on its own CI reads that CI's state with a link to that run", () => {
  for (const [name, goblin, status, phase, url] of [
    ["running, live", sid(checks("pending", { link: RUN })), "Testing", "pr-pending", RUN],
    ["running, paused until its CI run finishes", pausedForCI(checks("pending", { link: RUN })), "Testing", "pr-pending", RUN],
    ["running, paused until it merges", pausedUntilMerge(checks("pending", { link: RUN })), "Testing", "pr-pending", RUN],
    ["failed", pausedUntilMerge(checks("failed", { failed: ["test"], link: RUN })), "Checks failed", "pr-failed", RUN],
    ["passed, waiting for a train", pausedUntilMerge(checks("passed")), "Checks passed", "pr-passed", PR + "/checks"],
  ] as const) {
    const read = surfaces(board(goblin));
    assert.equal(read.status, status, name);
    assert.equal(read.phase, phase, name);
    assert.equal(read.column, "In progress", name);
    assert.equal(read.line, "", name);
    assert.equal(read.chip?.text, status, name);
    assert.equal(read.chip?.url, url, name);
  }
});

test("a goblin paused until its pull request merges stays in In progress with no Paused and no resumes line, even before its checks are read", () => {
  assert.deepEqual(surfaces(board(pausedUntilMerge())), { status: "Waiting on PR #512 to merge", phase: "pr-pending", column: "In progress", line: "", chip: undefined });
  // Its pause being taken still reads its pull request; its resume says so.
  assert.equal(surfaces(board(pausedUntilMerge({ phase: "pausing" }))).status, "Waiting on PR #512 to merge");
  const resuming = surfaces(board(pausedUntilMerge({ phase: "resuming" })));
  assert.deepEqual([resuming.status, resuming.column], ["Resuming", "In progress"]);
});

test("a real pause still reads as paused, in the Paused section, with what resumes it", () => {
  const otherPR = REPO + "/pull/600";
  for (const [name, pause, line] of [
    ["the memory floor in the middle of work", { reason: "memory", until: "" }, "It resumes by itself once 5 GB of memory is free."],
    ["his own pause", { reason: "overlord", until: "" }, "It stays paused until you resume it."],
    ["a wait on another task", { reason: "dependency", until: "task:cg-other" }, "It resumes by itself when the task it waits on finishes."],
    ["a wait on another goblin's pull request", { reason: "dependency", until: "pr:" + otherPR }, "It resumes by itself when PR #600 merges."],
  ] as const) {
    // Arrange: Sid's own pull request rides a train even while he paused it.
    const paused = board(sid({ phase: "paused", lifecycle: lifecycle(pause), ...checks("passed") }), [train("testing", "riding")]);

    // Act
    const read = surfaces(paused);

    // Assert
    assert.equal(read.column, "Paused", name);
    assert.equal(read.phase, "paused", name);
    assert.equal(read.status, "Paused", name);
    assert.equal(read.line, line, name);
    // The chip still says where the pull request stands.
    assert.equal(read.chip?.text, "Testing", name);
  }
});

test("a goblin at work, blocked or asking after its pull request rode says so, and only its chip reads the train", () => {
  const riding = train("testing", "riding");
  for (const [name, goblin, status] of [
    ["working on its next change", sid({ phase: "working", report: "working" }), "Working"],
    ["blocked", sid({ phase: "blocked", report: "blocked", reason: "Needs a key" }), "Blocked"],
    ["asking the CFO", sid({ phase: "blocked", reason: "Waiting on the CFO: which key?" }), "Waiting on the CFO"],
    ["waiting on another goblin", sid({ phase: "waiting", report: "waiting", waiting_on: "cg-other" }), "Waiting on cg-other"],
  ] as const) {
    const read = surfaces(board(goblin, [riding]));
    assert.equal(read.status, status, name);
    assert.equal(read.chip?.text, "Testing", name);
    assert.equal(read.chip?.url, TRAIN_PR, name);
  }
});

test("a goblin without a pull request, or one whose train carries another, reads its own state", () => {
  const read = surfaces(board(sid({ pr: "", report: "working", phase: "working" }), [train("testing", "riding")]));
  assert.deepEqual([read.status, read.chip], ["Working", undefined]);
  const other = surfaces(board(sid({ pr: REPO + "/pull/513" }), [train("testing", "riding")]));
  assert.equal(other.chip, undefined);
});
