import assert from "node:assert/strict";
import test from "node:test";
import { pullRequestTest } from "./pull-request-test.ts";
import { taskStatus } from "./task-status.ts";
import { taskSummary } from "./task-words.ts";
import { parseSnapshot, type MergeTrain, type Task } from "./types.ts";
import { nodeStatus, statusPhase, taskColumn } from "./workflow.ts";

// The Overlord, 2026-10-08, of Sid's pull request #512: the train's card read
// "CI tests #512" and "#512 Sid - Memory Keeper Testing" while Sid's panel
// read Paused in yellow, "It resumes by itself when PR #512 merges." and
// "Checks passed". A goblin whose pull request is under test reads one state
// with one link on its card, its panel, its canvas node and its train's card.
// The Overlord, 2026-10-16: "paused does it means youre on the bench to be
// resumed". A goblin paused until its pull request merges is on the bench:
// it reads Paused, in Paused, and its chip still says where its pull request
// stands.
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
// cardStatus is what its card reads.
const cardStatus = ({ task, trains }: { task: Task; trains: MergeTrain[] }) => taskStatus(task, parseSnapshot({ healthy: true, tasks: [task], merge_trains: trains })).text;
const surfaces = ({ task, trains }: { task: Task; trains: MergeTrain[] }) => ({
  status: nodeStatus({ id: task.id, title: "", task, relation: "" }, false, [task], trains),
  phase: statusPhase(task, trains),
  column: taskColumn(task),
  line: taskSummary(task, [task]).sentence,
  chip: pullRequestTest(task, trains),
});

test("a goblin whose pull request rides a merge train reads the train's state with the train's link, and paused it reads Paused with the train on its chip", () => {
  const testing = train("testing", "riding");
  const chip = { text: "Testing", tone: "pending", url: TRAIN_PR, tip: "CI tests #512" };
  assert.deepEqual(surfaces(board(sid(checks("passed")), [testing])), { status: "Testing", phase: "pr-pending", column: "In progress", line: "", chip });
  for (const [name, goblin, card] of [
    ["paused until its pull request merges", pausedUntilMerge(checks("passed")), "Paused until PR #512 merges"],
    ["paused until its CI run on the pull request finishes", pausedForCI(checks("passed")), "Paused until CI finishes"],
  ] as const) {
    assert.deepEqual(surfaces(board(goblin, [testing])), { status: "Paused", phase: "paused", column: "Paused", line: "", chip }, name);
    assert.equal(cardStatus(board(goblin, [testing])), card, name);
  }
});

test("the train's outcome is the goblin's: Landed, or what broke, with the train's link", () => {
  for (const [name, given, status, phase] of [
    ["landed", train("landed", "landed"), "Landed", "pr-passed"],
    ["broke CI", train("stopped", "culprit", { note: "#512 breaks CI on main: test" }), "Breaks CI", "pr-failed"],
    ["its half still waits", train("testing", "waiting"), "Waits its turn", "pr-pending"],
  ] as const) {
    const read = surfaces(board(sid(checks("passed")), [given]));
    assert.equal(read.status, status, name);
    assert.equal(read.phase, phase, name);
    assert.equal(read.column, "In progress", name);
    assert.equal(read.chip?.text, status, name);
    assert.equal(read.chip?.url, TRAIN_PR, name);
  }
});

test("a finished train no longer speaks for a pull request whose head moved since it rode, and a running one comes before a finished one", () => {
  const moved = board(sid(checks("pending", { head: "b".repeat(40), link: RUN })), [train("stopped", "culprit")]);
  assert.equal(surfaces(moved).status, "Testing");
  assert.equal(surfaces(moved).chip?.url, RUN);

  const older = { ...train("stopped", "returned"), id: "code-goblins-20261008-120000", pr: REPO + "/pull/899" };
  const both = board(sid(checks("passed")), [older, train("testing", "riding")]);
  assert.equal(surfaces(both).chip?.url, TRAIN_PR);
});

test("a goblin waiting on its own CI reads that CI's state with a link to that run", () => {
  for (const [name, goblin, status, phase, url] of [
    ["running, live", sid(checks("pending", { link: RUN })), "Testing", "pr-pending", RUN],
    ["failed", sid(checks("failed", { failed: ["test"], link: RUN })), "Checks failed", "pr-failed", RUN],
    ["passed, waiting for a train", sid(checks("passed")), "Checks passed", "pr-passed", PR + "/checks"],
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

test("a goblin paused until its pull request merges is on the bench: Paused, in Paused, and its panel says what resumes it", () => {
  const paused = board(pausedUntilMerge());
  assert.deepEqual(surfaces(paused), { status: "Paused", phase: "paused", column: "Paused", line: "", chip: undefined });
  assert.equal(cardStatus(paused), "Paused until PR #512 merges");
  assert.match(taskSummary(paused.task, [paused.task]).details.join(" "), /It resumes by itself when PR #512 merges\./);
  // Its pause being taken and its resume under way are on the bench too.
  for (const phase of ["pausing", "resuming"]) assert.equal(surfaces(board(pausedUntilMerge({ phase }))).column, "Paused", phase);
});

// The Overlord, 2026-10-08: "paused is completely stopped terminals for
// memory reasons". Paused holds a goblin stopped for memory, by the floor or
// by the CFO, or by his own Pause, and nothing else.
test("a pause for memory reads as paused, in the Paused section, with no line under its status", () => {
  for (const [name, pause] of [
    ["the memory floor in the middle of work", { reason: "memory", until: "" }],
    ["his own pause", { reason: "overlord", until: "" }],
  ] as const) {
    // Arrange: Sid's own pull request rides a train even while he paused it.
    const paused = board(sid({ phase: "paused", lifecycle: lifecycle(pause), ...checks("passed") }), [train("testing", "riding")]);

    // Act
    const read = surfaces(paused);

    // Assert
    assert.equal(read.column, "Paused", name);
    assert.equal(read.phase, "paused", name);
    assert.equal(read.status, "Paused", name);
    assert.equal(read.line, "", name);
    // The chip still says where the pull request stands.
    assert.equal(read.chip?.text, "Testing", name);
  }
  // A pause with no condition, from before conditions were kept, needs his
  // Resume as his own does.
  assert.equal(taskColumn(board(sid({ phase: "paused", lifecycle: { ...lifecycle({}), pause: undefined } })).task), "Paused");
});

test("every pause is on the bench, in Paused, whatever resumes it", () => {
  for (const [name, pause] of [
    ["a wait on another task", { reason: "dependency", until: "task:cg-other" }],
    ["a wait on another goblin's pull request", { reason: "dependency", until: "pr:" + REPO + "/pull/600" }],
    ["a wait on a date", { reason: "dependency", until: "date:2026-10-09T09:00:00Z" }],
    ["a wait for his answer", { reason: "question", until: "q-1" }],
    ["a wait for the allowance to reset", { reason: "allowance", until: "2026-10-09T09:00:00Z" }],
    ["a wait on a deploy", { reason: "deploy", until: "run:" + REPO + "/actions/runs/9@" + HEAD }],
  ] as const) {
    for (const phase of ["paused", "pausing", "resuming"]) {
      const read = surfaces(board(sid({ phase, lifecycle: lifecycle(pause), ...checks("passed") })));
      assert.equal(read.column, "Paused", name + ", " + phase);
    }
    assert.equal(surfaces(board(sid({ phase: "paused", lifecycle: lifecycle(pause) }))).phase, "paused", name);
  }
});

test("a goblin whose pull request a train fails and retries reads each train on its chip until the retry lands", () => {
  // Arrange: the Overlord's 2026-10-08 trains, main moved under the first
  // three times, and the second landed what the first carried.
  const runs = (count: number, result: string) => Array.from({ length: count }, (_, index) => ({ number: index + 1, riders: [512], base: "b".repeat(40), head: "c".repeat(40), pushed: "2026-10-08T18:10:00Z", result }));
  const first = (state: string, carState: string, history: object[]) => ({ ...train(state, carState, { history, runs: history.length }), id: "code-goblins-20261008-180000", pr: REPO + "/pull/899" });
  const retry = (state: string, carState: string, earlier: object) => ({ ...train(state, carState, { history: runs(1, state === "landed" ? "landed" : "") }), id: "code-goblins-20261008-190000", started: "2026-10-08T19:00:00Z", earlier: [earlier] });
  const failedFirst = first("failed", "returned", runs(3, "moved"));
  for (const [step, trains, status, url, tip] of [
    ["the first train tests it", [first("testing", "riding", runs(1, ""))], "Testing", REPO + "/pull/899", "CI tests #512"],
    ["the first train failed", [failedFirst], "Rides the next train", REPO + "/pull/899", "Train failed, main kept moving"],
    ["the retry tests it", [retry("testing", "riding", failedFirst)], "Testing", TRAIN_PR, "CI tests #512, run 4"],
    ["the retry landed it", [retry("landed", "landed", failedFirst)], "Landed", TRAIN_PR, "Landed on run 4"],
  ] as const) {
    // Act
    const read = surfaces(board(sid(checks("passed")), [...trains]));

    // Assert
    assert.equal(read.column, "In progress", step);
    assert.equal(read.status, status, step);
    assert.deepEqual([read.chip?.text, read.chip?.url, read.chip?.tip], [status, url, tip], step);
  }
});

test("a pull request a train could not merge shows in its goblin's panel with what happens next, in grey", () => {
  // Arrange: Abe's #523 conflicted on the train that landed the rest.
  const conflicted = train("landed", "conflict", { cars: [{ number: 512, url: PR, title: "Keep memory", task: "cg-sid", goblin: "Sid", goblin_title: "Memory Keeper", head: HEAD, state: "conflict", note: "it conflicts in a.go" }] });

  // Act
  const read = surfaces(board(sid({ phase: "working", report: "working", ...checks("passed") }), [conflicted]));

  // Assert
  assert.equal(read.column, "In progress");
  assert.deepEqual(read.chip, { text: "Merging main to fix a conflict", tone: "cancelled", url: TRAIN_PR, tip: "it conflicts in a.go" });
});

test("a pull request left behind by a train another took on still reads that train", () => {
  const left = { ...train("failed", "returned"), id: "code-goblins-20261008-180000", pr: REPO + "/pull/899", cars: [{ number: 512, url: PR, title: "", task: "cg-sid", goblin: "", goblin_title: "", head: HEAD, state: "returned", note: "" }, { number: 513, url: REPO + "/pull/513", title: "", task: "", goblin: "", goblin_title: "", head: "", state: "returned", note: "" }] };
  const tookOn = { ...train("testing", "riding"), cars: [{ number: 513, url: REPO + "/pull/513", title: "", task: "", goblin: "", goblin_title: "", head: "", state: "riding", note: "" }], earlier: [left] };
  const read = surfaces(board(pausedUntilMerge(checks("passed")), [tookOn]));
  assert.deepEqual([read.chip?.text, read.chip?.url], ["Rides the next train", REPO + "/pull/899"]);
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
