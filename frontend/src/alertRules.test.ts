import { test } from "node:test";
import assert from "node:assert/strict";
import { arrive, asksPermission, boardAlerts, notifies, type BoardAlert } from "./alertRules.ts";
import type { Question, Review, Run, Snapshot, Task } from "./types.ts";

const task = (id: string, phase: string, extra: Partial<Task> = {}): Task => ({ id, title: "Goblin " + id, phase, pr: "", reason: "", activity: "", generation: id + "-1", ...extra }) as Task;
const question = (id: string, extra: Partial<Question> = {}): Question => ({ id, text: "Which option?", status: "pending", task: "a", created_at: "2026-09-27T01:00:00Z", ...extra }) as Question;
const review = (id: string): Review => ({ id, title: "Review the plan", state: "open", task: "a", created_at: "2026-09-27T01:00:00Z" }) as Review;
const run = (id: string, state = "ready"): Run => ({ id, title: "Restart the board", state, created_at: "2026-09-27T01:00:00Z" }) as Run;
const snapshot = (parts: { tasks?: Task[]; questions?: Question[]; reviews?: Review[]; runs?: Run[] }): Snapshot => ({ tasks: parts.tasks || [task("a", "working")], questions: parts.questions || [], reviews: parts.reviews || [], runs: parts.runs || [], attention: [] as string[] }) as Snapshot;

test("what needs the Overlord or finished alerts once, and opens its item", () => {
  const before = snapshot({});
  const cases: [string, Snapshot, { key: string; tone: string; target: unknown }[]][] = [
    ["a new question", snapshot({ questions: [question("q1")] }), [{ key: "question:q1", tone: "needs", target: { kind: "command", key: "question:q1" } }]],
    ["a new review card", snapshot({ reviews: [review("r1")] }), [{ key: "review:r1", tone: "needs", target: { kind: "command", key: "review:r1" } }]],
    ["a new run card", snapshot({ runs: [run("c1")] }), [{ key: "run:c1", tone: "needs", target: { kind: "command", key: "run:c1" } }]],
    ["a goblin blocked", snapshot({ tasks: [task("a", "blocked", { reason: "Needs a key" })] }), [{ key: "task:a:a-1:blocked", tone: "needs", target: { kind: "task", id: "a" } }]],
    ["a goblin failed", snapshot({ tasks: [task("a", "failed")] }), [{ key: "task:a:a-1:failed", tone: "failed", target: { kind: "task", id: "a" } }]],
    ["a goblin done with its pull request", snapshot({ tasks: [task("a", "done", { pr: "https://github.com/o/r/pull/7" })] }), [{ key: "task:a:a-1:done", tone: "done", target: { kind: "task", id: "a" } }]],
    ["a goblin reporting it failed", snapshot({ tasks: [task("a", "review", { report: "failed", activity: "The build broke" })] }), [{ key: "task:a:a-1:failed", tone: "failed", target: { kind: "task", id: "a" } }]],
    ["a goblin reporting done with its pull request", snapshot({ tasks: [task("a", "review", { report: "done", pr: "https://github.com/o/r/pull/7" })] }), [{ key: "task:a:a-1:done", tone: "done", target: { kind: "task", id: "a" } }]],
  ];
  for (const [name, next, want] of cases) {
    assert.deepEqual(boardAlerts(before, next).map(({ key, tone, target }) => ({ key, tone, target })), want, name);
  }
});

test("routine updates and what was already there alert nothing", () => {
  const working = snapshot({ tasks: [task("a", "working", { activity: "Reading files" })], questions: [question("q1")] });
  const cases: [string, Snapshot | null, Snapshot][] = [
    ["the first snapshot a page sees", null, snapshot({ questions: [question("q1")], tasks: [task("a", "blocked")] })],
    ["new working activity", working, snapshot({ tasks: [task("a", "working", { activity: "Running tests" })], questions: [question("q1")] })],
    ["a goblin entering its review gate", working, snapshot({ tasks: [task("a", "review")], questions: [question("q1")] })],
    ["a goblin waiting on another task", working, snapshot({ tasks: [task("a", "waiting")], questions: [question("q1")] })],
    ["a goblin done without a pull request", working, snapshot({ tasks: [task("a", "done")], questions: [question("q1")] })],
    ["a goblin reporting it is blocked, which raises its own question", working, snapshot({ tasks: [task("a", "review", { report: "blocked" })], questions: [question("q1")] })],
    ["a goblin reporting work", working, snapshot({ tasks: [task("a", "review", { report: "working" })], questions: [question("q1")] })],
    ["a question already waiting", working, working],
    ["a question answered", working, snapshot({ tasks: [task("a", "working")], questions: [question("q1", { status: "answered" })] })],
    ["a goblin still blocked", snapshot({ tasks: [task("a", "blocked")] }), snapshot({ tasks: [task("a", "blocked", { reason: "Still waiting" })] })],
    ["a run card finishing its command", snapshot({ runs: [run("c1", "running")] }), snapshot({ runs: [run("c1", "succeeded")] })],
  ];
  for (const [name, before, next] of cases) assert.deepEqual(boardAlerts(before, next), [], name);
});

test("a done goblin alerts when its pull request arrives, and a restarted goblin alerts again", () => {
  const done = snapshot({ tasks: [task("a", "done")] });
  const withPR = snapshot({ tasks: [task("a", "done", { pr: "https://github.com/o/r/pull/7" })] });
  assert.deepEqual(boardAlerts(done, withPR).map((alert) => alert.key), ["task:a:a-1:done"]);
  const blocked = snapshot({ tasks: [task("a", "blocked")] });
  const restartedBlocked = snapshot({ tasks: [task("a", "blocked", { generation: "a-2" })] });
  assert.deepEqual(boardAlerts(blocked, restartedBlocked).map((alert) => alert.key), ["task:a:a-2:blocked"]);
});

test("a goblin's own failure says what it reported", () => {
  const [alert] = boardAlerts(snapshot({}), snapshot({ tasks: [task("a", "review", { report: "failed", activity: "The build broke", reason: "Manual task mode" })] }));
  assert.equal(alert.text, "The build broke");
});

test("a goblin waiting on the Overlord says so, and a review item asks for review", () => {
  const waiting = { ...review("waiting-a-3"), title: "Sign in to GitHub" };
  const [wait, plan] = boardAlerts(snapshot({}), snapshot({ reviews: [waiting, review("plan")] }));
  assert.equal(wait.title, "Goblin a is waiting on you");
  assert.equal(plan.title, "Goblin a wants your review");
});

test("the Completed column's history alerts nothing, while a live goblin done with its pull request alerts once", () => {
  const pr = "https://github.com/o/r/pull/7";
  const working = snapshot({ tasks: [task("a", "working")] });
  const done = snapshot({ tasks: [task("a", "review", { report: "done", pr })] });
  const finished = task("finished:a", "done", { archived: true, pr, title: "a" });
  const merged = task("merged:" + pr, "done", { archived: true, merged: true, pr, title: "feat/a" });
  assert.deepEqual(boardAlerts(working, done).map((alert) => alert.key), ["task:a:a-1:done"]);
  const cases: [string, Snapshot, Snapshot][] = [
    ["a finished entry appearing", working, snapshot({ tasks: [task("a", "working"), finished] })],
    ["a merged entry appearing", working, snapshot({ tasks: [task("a", "working"), merged] })],
    ["the live goblin leaving for its finished entry", done, snapshot({ tasks: [finished] })],
    ["a merged entry beside the live goblin still in review", done, snapshot({ tasks: [task("a", "review", { report: "done", pr }), merged] })],
    ["history returning after a restart", snapshot({ tasks: [] }), snapshot({ tasks: [finished, merged] })],
  ];
  for (const [name, before, next] of cases) assert.deepEqual(boardAlerts(before, next), [], name);
});

test("a goblin's own failure drops the verb its report line starts with", () => {
  const [alert] = boardAlerts(snapshot({}), snapshot({ tasks: [task("a", "working", { report: "failed", activity: "failed: spawn refused: no harness" })] }));
  assert.equal(alert.text, "spawn refused: no harness");
});

test("each arrival gets its own identity, so a replacement toast starts afresh and outlives the old one's dismissal", () => {
  const alert = (key: string) => ({ key, tone: "failed", title: key, text: "", task: "a", target: { kind: "task", id: "a" } }) as BoardAlert;
  const first = arrive([], [alert("task:a:a-1:failed"), alert("question:q1")]);
  assert.deepEqual(first.map((toast) => toast.alert.key), ["task:a:a-1:failed", "question:q1"]);
  assert.equal(new Set(first.map((toast) => toast.id)).size, 2);
  const replaced = arrive(first, [alert("task:a:a-1:failed")]);
  assert.deepEqual(replaced.map((toast) => toast.alert.key), ["question:q1", "task:a:a-1:failed"]);
  const [old] = first;
  assert.ok(!replaced.some((toast) => toast.id === old.id), "the replacement has a new identity");
  assert.deepEqual(replaced.filter((toast) => toast.id !== old.id), replaced, "dismissing the old arrival keeps the new one");
  const full = arrive(replaced, ["r1", "r2", "r3"].map((key) => alert("review:" + key)));
  assert.deepEqual(full.map((toast) => toast.alert.key), ["task:a:a-1:failed", "review:r1", "review:r2", "review:r3"], "at most four, the oldest leaving first");
});

test("an alert names who asks and what, in a few words", () => {
  const [alert] = boardAlerts(snapshot({}), snapshot({ questions: [question("q1", { text: "Ship **now**?\n\n" + "x".repeat(300) })] }));
  assert.equal(alert.title, "Goblin a asks you");
  assert.ok(alert.text.startsWith("Ship now?") && alert.text.length <= 160, alert.text);
  const [cfo] = boardAlerts(snapshot({}), snapshot({ questions: [question("q2", { task: "" })] }));
  assert.equal(cfo.title, "The CFO asks you");
});

test("a Windows notification is only for a board he is not looking at, once allowed", () => {
  const cases: [string, NotificationPermission | "unsupported", boolean, boolean, boolean][] = [
    ["a hidden tab", "granted", true, false, true],
    ["a window behind another", "granted", false, false, true],
    ["the board in front", "granted", false, true, false],
    ["not allowed", "denied", true, false, false],
    ["not answered yet", "default", true, false, false],
    ["a browser without notifications", "unsupported", true, false, false],
  ];
  for (const [name, permission, hidden, focused, want] of cases) assert.equal(notifies(permission, hidden, focused), want, name);
});

test("the board asks for notifications once, and only while the browser has not been answered", () => {
  assert.equal(asksPermission("default", false), true);
  assert.equal(asksPermission("default", true), false, "asked before");
  assert.equal(asksPermission("granted", false), false);
  assert.equal(asksPermission("denied", false), false);
  assert.equal(asksPermission("unsupported", false), false);
});
