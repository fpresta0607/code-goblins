import { test } from "node:test";
import assert from "node:assert/strict";
import { arrive, asksPermission, boardAlerts, notifies, SEEN_LIMIT, unseen, type AlertTarget, type BoardAlert } from "./alertRules.ts";
import type { Question, Review, Run, Snapshot, Task } from "./types.ts";

const task = (id: string, phase: string, extra: Partial<Task> = {}): Task => ({ id, title: "Goblin " + id, phase, pr: "", reason: "", activity: "", generation: id + "-1", ...extra }) as Task;
const question = (id: string, extra: Partial<Question> = {}): Question => ({ id, text: "Which option?", status: "pending", task: "a", created_at: "2026-09-27T01:00:00Z", ...extra }) as Question;
const review = (id: string): Review => ({ id, title: "Review the plan", state: "open", task: "a", created_at: "2026-09-27T01:00:00Z" }) as Review;
const run = (id: string, state = "ready"): Run => ({ id, title: "Restart the board", state, created_at: "2026-09-27T01:00:00Z" }) as Run;
const snapshot = (parts: { tasks?: Task[]; questions?: Question[]; reviews?: Review[]; runs?: Run[] }): Snapshot => ({ tasks: parts.tasks || [task("a", "working")], questions: parts.questions || [], reviews: parts.reviews || [], runs: parts.runs || [], attention: [] as string[] }) as Snapshot;
// The title of the goblin whose finished alert the Overlord showed on
// 2026-10-01: its name tab, its text and its Open button each said it.
const LONG_TITLE = "Answers given in a review page's editor are never lost (a disconnect is not the end), no duplicate Command Center question, revisions update the editor, the Command Center never blocks typing, and waiting cards render cleanly with a copy button on every value";

test("what needs the Overlord or finished alerts once, and opens its item", () => {
  const before = snapshot({});
  const cases: [string, Snapshot, { key: string; tone: string; target: unknown }[]][] = [
    ["a new question", snapshot({ questions: [question("q1")] }), [{ key: "item:a:Goblin a asks: Which option?", tone: "needs", target: { kind: "command", key: "question:q1" } }]],
    ["a new review card", snapshot({ reviews: [review("r1")] }), [{ key: "item:a:Goblin a wants your review: Review the plan", tone: "needs", target: { kind: "command", key: "review:r1" } }]],
    ["a new run card", snapshot({ runs: [run("c1")] }), [{ key: "item:cfo:A command waits for you to run it: Restart the board", tone: "needs", target: { kind: "command", key: "run:c1" } }]],
    ["a goblin blocked", snapshot({ tasks: [task("a", "blocked", { reason: "Needs a key" })] }), [{ key: "task:a:a-1:blocked:Needs a key", tone: "needs", target: { kind: "command", key: "" } }]],
    ["a goblin failed", snapshot({ tasks: [task("a", "failed")] }), [{ key: "task:a:a-1:failed:", tone: "failed", target: { kind: "task", id: "a" } }]],
    ["a goblin done with its pull request", snapshot({ tasks: [task("a", "done", { pr: "https://github.com/o/r/pull/7" })] }), [{ key: "task:a:a-1:done:https://github.com/o/r/pull/7", tone: "done", target: { kind: "task", id: "a" } }]],
    ["a goblin reporting it failed", snapshot({ tasks: [task("a", "review", { report: "failed", activity: "The build broke" })] }), [{ key: "task:a:a-1:failed:The build broke", tone: "failed", target: { kind: "task", id: "a" } }]],
    ["a goblin reporting done with its pull request", snapshot({ tasks: [task("a", "review", { report: "done", pr: "https://github.com/o/r/pull/7" })] }), [{ key: "task:a:a-1:done:https://github.com/o/r/pull/7", tone: "done", target: { kind: "task", id: "a" } }]],
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
  assert.deepEqual(boardAlerts(done, withPR).map((alert) => alert.key), ["task:a:a-1:done:https://github.com/o/r/pull/7"]);
  const blocked = snapshot({ tasks: [task("a", "blocked")] });
  const restartedBlocked = snapshot({ tasks: [task("a", "blocked", { generation: "a-2" })] });
  assert.deepEqual(boardAlerts(blocked, restartedBlocked).map((alert) => alert.key), ["task:a:a-2:blocked:"]);
});

test("a blocked goblin needs the Overlord, so it opens its newest item waiting in the Command Center, or else the first waiting or the inbox", () => {
  const older = question("q1", { created_at: "2026-09-27T01:00:00Z" });
  const newer = { ...review("r1"), created_at: "2026-09-27T02:00:00Z" };
  const another = question("q2", { task: "b", created_at: "2026-09-27T03:00:00Z" });
  const answered = question("q3", { status: "answered", created_at: "2026-09-27T04:00:00Z" });
  const cases: [string, Parameters<typeof snapshot>[0], AlertTarget][] = [
    ["its newest item of several", { questions: [older, another], reviews: [newer] }, { kind: "command", key: "review:r1" }],
    ["only another goblin's item", { questions: [another] }, { kind: "command", key: "" }],
    ["only an item already answered", { questions: [answered] }, { kind: "command", key: "" }],
  ];
  for (const [name, waiting, target] of cases) {
    const [alert] = boardAlerts(snapshot(waiting), snapshot({ ...waiting, tasks: [task("a", "blocked")] }));
    assert.deepEqual({ tone: alert.tone, action: alert.action, target: alert.target }, { tone: "needs", action: "Open Command Center", target }, name);
  }
});

test("a goblin's own failure says what it reported", () => {
  const [alert] = boardAlerts(snapshot({}), snapshot({ tasks: [task("a", "review", { report: "failed", activity: "The build broke", reason: "Manual task mode" })] }));
  assert.equal(alert.text, "Goblin a failed: The build broke");
});

test("a goblin waiting on the Overlord says so once, and a review item asks for review", () => {
  // The supervisor titles a wait as the Command Center's card heads it, with
  // the page it names, so the alert says what the goblin waits for, once.
  const page = "http://sermon.tail.ts.net:4387/session/0f5b";
  const waiting = { ...review("waiting-a-3"), title: "Waiting on you: Sign in to GitHub (page " + page + ")", lavish: page };
  const [wait, plan] = boardAlerts(snapshot({}), snapshot({ reviews: [waiting, review("plan")] }));
  assert.equal(wait.text, "Goblin a is waiting on you: Sign in to GitHub");
  assert.equal(plan.text, "Goblin a wants your review: Review the plan");
});

test("a goblin's own question alerts once, as its question, whichever reaches the board first", () => {
  // As the supervisor serves it: the goblin's blocked notify sets its task
  // blocked, Waiting on the CFO, before or as its question reaches the
  // Command Center, and both say the same thing.
  const asked = "May I finish the remaining heavy steps now, or stay paused?";
  const working = snapshot({ tasks: [task("a", "working", { report: "working" })] });
  const blocked = task("a", "blocked", { report: "blocked", reason: "Waiting on the CFO: " + asked, activity: asked + " options: Finish it now (Recommended) | Stay paused" });
  const pending = question("notify-a-7", { text: asked });
  const orders: [string, Snapshot[]][] = [
    ["both in one snapshot", [working, snapshot({ tasks: [blocked], questions: [pending] })]],
    ["the task blocked first", [working, snapshot({ tasks: [blocked] }), snapshot({ tasks: [blocked], questions: [pending] })]],
    ["the question first", [working, snapshot({ questions: [pending] }), snapshot({ tasks: [blocked], questions: [pending] })]],
  ];
  for (const [name, snapshots] of orders) {
    const alerts = snapshots.slice(1).flatMap((next, i) => boardAlerts(snapshots[i], next));
    assert.deepEqual(alerts.map((alert) => alert.text), ["Goblin a asks: " + asked], name);
  }
});

test("an event alerts once however often a snapshot or a reconnect brings it back, and a goblin's next news alerts", () => {
  const pr = "https://github.com/o/r/pull/7";
  const working = snapshot({ tasks: [task("a", "working")] });
  const done = snapshot({ tasks: [task("a", "review", { report: "done", pr })] });
  const restarting = snapshot({ tasks: [] });
  const wait = (id: string, state = "open") => ({ ...review(id), title: "Waiting on you: Look at the card", state });
  const waiting = snapshot({ reviews: [wait("waiting-a-3")] });
  const refiled = snapshot({ reviews: [wait("waiting-a-3", "withdrawn"), wait("waiting-a-5")] });
  const nextPR = snapshot({ tasks: [task("a", "review", { report: "done", pr: "https://github.com/o/r/pull/8" })] });
  let seen: string[] = [];
  const shown = (from: Snapshot, to: Snapshot) => {
    const sighting = unseen(boardAlerts(from, to), seen);
    seen = sighting.seen;
    return sighting.fresh.map((alert) => alert.text);
  };
  assert.deepEqual(shown(working, done), ["Goblin a finished: r #7 is ready."]);
  assert.deepEqual(shown(done, restarting), [], "the supervisor restarting");
  assert.deepEqual(shown(restarting, done), [], "the same news in the restarted supervisor's snapshot");
  assert.deepEqual(shown(done, working), []);
  assert.deepEqual(shown(working, done), [], "the same news after the goblin flickered back to work");
  assert.deepEqual(shown(done, waiting), ["Goblin a is waiting on you: Look at the card"]);
  assert.deepEqual(shown(waiting, refiled), [], "the same wait filed again");
  assert.deepEqual(shown(refiled, nextPR), ["Goblin a finished: r #8 is ready."], "its next pull request is news");
});

test("a browser remembers the newest alerts it showed, a bounded few, and one snapshot shows each once", () => {
  const alert = (key: string) => ({ key }) as BoardAlert;
  const many = Array.from({ length: SEEN_LIMIT + 5 }, (_, i) => alert("k" + i));
  const { fresh, seen } = unseen([...many, alert("k0")], ["k-old"]);
  assert.equal(fresh.length, SEEN_LIMIT + 5);
  assert.equal(seen.length, SEEN_LIMIT);
  assert.equal(seen.at(-1), "k" + (SEEN_LIMIT + 4));
  assert.deepEqual(unseen([alert("k-old"), alert("k3")], ["k-old"]).fresh.map((one) => one.key), ["k3"]);
});

test("the Completed column's history alerts nothing, while a live goblin done with its pull request alerts once", () => {
  const pr = "https://github.com/o/r/pull/7";
  const working = snapshot({ tasks: [task("a", "working")] });
  const done = snapshot({ tasks: [task("a", "review", { report: "done", pr })] });
  const finished = task("finished:a", "done", { archived: true, pr, title: "a" });
  const merged = task("merged:" + pr, "done", { archived: true, merged: true, pr, title: "feat/a" });
  assert.deepEqual(boardAlerts(working, done).map((alert) => alert.key), ["task:a:a-1:done:" + pr]);
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
  assert.equal(alert.text, "Goblin a failed: spawn refused: no harness");
});

test("each alert names who speaks by its goblin's title, as its card does, says one plain line and offers the one thing to do", () => {
  const before = snapshot({});
  const cfoQuestion = question("q2", { task: "", text: "Merge the release now?\n\n- details" });
  const cases: [string, Snapshot, { speaker: string; text: string; action: string }][] = [
    ["a goblin's question", snapshot({ questions: [question("q1")] }), { speaker: "Goblin a", text: "Goblin a asks: Which option?", action: "Open Command Center" }],
    ["a question from a goblin no longer on the board", snapshot({ questions: [question("q1", { task: "gone" })] }), { speaker: "gone", text: "gone asks: Which option?", action: "Open Command Center" }],
    ["the CFO's question", snapshot({ questions: [cfoQuestion] }), { speaker: "CFO", text: "The CFO asks: Merge the release now?", action: "Open Command Center" }],
    ["a command to run", snapshot({ runs: [run("c1")] }), { speaker: "CFO", text: "A command waits for you to run it: Restart the board", action: "Open Command Center" }],
    ["a blocked goblin", snapshot({ tasks: [task("a", "blocked", { reason: "Needs a key" })] }), { speaker: "Goblin a", text: "Goblin a is blocked: Needs a key", action: "Open Command Center" }],
    ["a failed goblin", snapshot({ tasks: [task("a", "failed")] }), { speaker: "Goblin a", text: "Goblin a failed: it needs a decision to go on.", action: "Open" }],
    ["a finished goblin", snapshot({ tasks: [task("a", "done", { pr: "https://github.com/o/r/pull/7" })] }), { speaker: "Goblin a", text: "Goblin a finished: r #7 is ready.", action: "Open" }],
    ["a goblin without a title", snapshot({ tasks: [task("a", "failed", { title: "" })] }), { speaker: "a", text: "a failed: it needs a decision to go on.", action: "Open" }],
    ["a goblin whose title is a whole sentence", snapshot({ tasks: [task("a", "done", { title: LONG_TITLE, pr: "https://github.com/fpresta0607/code-goblins/pull/225" })] }),
      { speaker: LONG_TITLE, text: "Answers given in a review page's editor are never lost (a d… finished: code-goblins #225 is ready.", action: "Open" }],
  ];
  for (const [name, next, want] of cases) {
    const [alert] = boardAlerts(before, next);
    assert.deepEqual({ speaker: alert.speaker, text: alert.text, action: alert.action }, want, name);
  }
});

test("fresh alerts stack below the ones on screen, at most four, the oldest leaving first", () => {
  const alert = (key: string) => ({ key }) as BoardAlert;
  const first = arrive([], [alert("task:a"), alert("item:q1")]);
  assert.deepEqual(first.map((toast) => toast.key), ["task:a", "item:q1"]);
  const full = arrive(first, ["r1", "r2", "r3"].map((key) => alert("item:" + key)));
  assert.deepEqual(full.map((toast) => toast.key), ["item:q1", "item:r1", "item:r2", "item:r3"]);
});

test("an alert names who asks and what, in a few words", () => {
  const [alert] = boardAlerts(snapshot({}), snapshot({ questions: [question("q1", { text: "Ship **now**? " + "x".repeat(300) + "\n\nThe details." })] }));
  assert.equal(alert.speaker, "Goblin a");
  assert.ok(alert.text.startsWith("Goblin a asks: Ship now?") && alert.text.length <= 160 && !alert.text.includes("details"), alert.text);
  const [cfo] = boardAlerts(snapshot({}), snapshot({ questions: [question("q2", { task: "" })] }));
  assert.equal(cfo.speaker, "CFO");
  assert.equal(cfo.text, "The CFO asks: Which option?");
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
