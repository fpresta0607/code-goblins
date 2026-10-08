import { test } from "node:test";
import assert from "node:assert/strict";
import { alertItem, announceKey, asksPermission, boardAlerts, notifies, outlived, SEEN_LIMIT, unseen, type BoardAlert, type SeenAlert } from "./alertRules.ts";
import { parseSnapshot, type Question, type Review, type Run, type Snapshot, type Task } from "./types.ts";

const task = (id: string, phase: string, extra: Partial<Task> = {}): Task => ({ id, title: "Goblin " + id, phase, pr: "", reason: "", activity: "", generation: id + "-1", ...extra }) as Task;
const question = (id: string, extra: Partial<Question> = {}): Question => ({ id, text: "Which option?", status: "pending", task: "", created_at: "2026-09-27T01:00:00Z", ...extra }) as Question;
const review = (id: string): Review => ({ id, title: "Review the plan", state: "open", task: "a", created_at: "2026-09-27T01:00:00Z" }) as Review;
const run = (id: string, state = "ready"): Run => ({ id, title: "Restart the board", state, created_at: "2026-09-27T01:00:00Z" }) as Run;
const snapshot = (parts: { tasks?: Task[]; questions?: Question[]; reviews?: Review[]; runs?: Run[] }): Snapshot => ({ tasks: parts.tasks || [task("a", "working")], questions: parts.questions || [], reviews: parts.reviews || [], runs: parts.runs || [], attention: [] as string[] }) as Snapshot;
const MINUTE = 60 * 1000;
// The reason the gate gives every time a step needs the Overlord's decision.
const GATE_BLOCK = "Pipeline decision required at review; use cfo pipeline respond";
const PR = "https://github.com/o/r/pull/7";
const LONG_TITLE = "Answers given in a review page's editor are never lost (a disconnect is not the end), no duplicate Command Center question, revisions update the editor, the Command Center never blocks typing, and waiting cards render cleanly with a copy button on every value";

// The Overlord, 2026-10-07, after a toast about a retired goblin opened its
// task panel: "alerts should only be open command center questions". What a
// goblin is doing, and whether the CFO keeps up, is on its card and the CFO's
// bar, never an alert.
test("a goblin blocked, failed or done raises no alert, and neither does the CFO falling behind", () => {
  const working = snapshot({ tasks: [task("a", "working")] });
  const cases: [string, Snapshot | null, Snapshot][] = [
    ["a goblin blocked", working, snapshot({ tasks: [task("a", "blocked", { reason: "Needs a key" })] })],
    ["a goblin blocked at its gate", working, snapshot({ tasks: [task("a", "blocked", { report: "blocked", activity: "blocked: Which port?", reason: GATE_BLOCK })] })],
    ["a goblin failed", working, snapshot({ tasks: [task("a", "failed")] })],
    ["a goblin reporting it failed", working, snapshot({ tasks: [task("a", "review", { report: "failed", activity: "failed: The build broke" })] })],
    ["a goblin done with its pull request", working, snapshot({ tasks: [task("a", "done", { pr: PR })] })],
    ["a goblin reporting done with its pull request", working, snapshot({ tasks: [task("a", "review", { report: "done", pr: PR })] })],
    ["a goblin restarted and blocked again", snapshot({ tasks: [task("a", "blocked")] }), snapshot({ tasks: [task("a", "blocked", { generation: "a-2" })] })],
    ["the CFO leaving questions unanswered", parseSnapshot({ healthy: true }), parseSnapshot({ healthy: true, cfo_quiet: { since: "2026-10-02T12:10:00Z", count: 3, oldest_age: 660 } })],
    ["the CFO leaving questions unanswered, on the first snapshot", null, parseSnapshot({ healthy: true, cfo_quiet: { since: "2026-10-02T12:10:00Z", count: 3, oldest_age: 660 } })],
  ];
  for (const [name, before, next] of cases) assert.deepEqual(boardAlerts(before, next), [], name);
});

// The toast the Overlord showed on 2026-10-07 came from the CFO retiring a
// goblin whose work was done: its pause missed the handoff deadline and did
// not finish, which an older supervisor read as the goblin failing, and the
// unanswered report then counted as a question left on the CFO.
test("a retired goblin's pause that did not finish raises no alert", () => {
  // Arrange
  const title = "Unused admin route removed; macro-security test can fail (issues 1432, 1273)";
  const said = "Requested by the operator; worktree C:\\dev\\PrecisionDocs-AI\\.worktrees\\gb-pd-small-cleanups; task session and branch";
  const lifecycle = { phase: "failed", action: "pause", at: "2026-10-07T14:49:11Z", kept: [], stopped: [], problems: ["Stopping-point deadline reached or request failed; no new handoff was saved", "context deadline exceeded"], handoff_saved: false, validation_restarts: false };
  const done = task("pd-small-cleanups", "done", { title, report: "done", pr: "https://github.com/fpresta0607/PrecisionDocs-AI/pull/1507" });
  const pausing = { ...done, phase: "pausing", reason: "Requested by the operator" };
  const failed = { ...done, phase: "failed", report: "", reason: "Waiting on the CFO: " + said, activity: said, lifecycle } as Task;
  const reported = { ...failed, report: "failed", activity: "failed: " + said, reason: said } as Task;
  const steps = [snapshot({ tasks: [done] }), snapshot({ tasks: [pausing] }), snapshot({ tasks: [failed] }), snapshot({ tasks: [reported] }),
    { ...snapshot({ tasks: [reported] }), cfo_quiet: { since: "2026-10-07T14:59:11Z", count: 1, oldest_age: 600 } }];

  // Act
  const alerts = steps.slice(1).flatMap((next, i) => boardAlerts(steps[i], next));

  // Assert
  assert.deepEqual(alerts.map((alert) => alert.text), []);
});

test("a new Command Center item that asks him something raises one alert that opens it there", () => {
  const before = snapshot({});
  const cases: [string, Snapshot, { key: string; item: string; text: string }][] = [
    ["a new question", snapshot({ questions: [question("q1")] }), { key: "question:q1@2026-09-27T01:00:00Z", item: "question:q1", text: "The CFO asks: Which option?" }],
    ["a new review card", snapshot({ reviews: [review("r1")] }), { key: "review:r1@2026-09-27T01:00:00Z", item: "review:r1", text: "Goblin a wants your review: Review the plan" }],
    ["a new run card", snapshot({ runs: [run("c1")] }), { key: "run:c1@2026-09-27T01:00:00Z", item: "run:c1", text: "A command waits for you to run it: Restart the board" }],
  ];
  for (const [name, next, want] of cases) {
    assert.deepEqual(boardAlerts(before, next).map(({ key, item, text }) => ({ key, item, text })), [want], name);
  }
});

test("routine updates and what was already there alert nothing", () => {
  const working = snapshot({ tasks: [task("a", "working", { activity: "Reading files" })], questions: [question("q1")] });
  const cases: [string, Snapshot | null, Snapshot][] = [
    ["the first snapshot a page sees", null, snapshot({ questions: [question("q1")], tasks: [task("a", "blocked")] })],
    ["new working activity", working, snapshot({ tasks: [task("a", "working", { activity: "Running tests" })], questions: [question("q1")] })],
    ["a goblin entering its review gate", working, snapshot({ tasks: [task("a", "review")], questions: [question("q1")] })],
    ["a question already waiting", working, working],
    ["a question answered", working, snapshot({ tasks: [task("a", "working")], questions: [question("q1", { status: "answered" })] })],
    ["a run card finishing its command", snapshot({ runs: [run("c1", "running")] }), snapshot({ runs: [run("c1", "succeeded")] })],
  ];
  for (const [name, before, next] of cases) assert.deepEqual(boardAlerts(before, next), [], name);
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

test("a wait that carries a table alerts with its words on one line, without the table, the queue's prefix or its page", () => {
  const page = "http://127.0.0.1:4387/session/f26e";
  const records = { ...review("waiting-a-4"), title: "Waiting on you: Add the DNS records in **Cloudflare**\n| Type | Name |\n| --- | --- |\n| CNAME | `mcp` |\nthen tell me (page " + page + ")", lavish: page };
  const [alert] = boardAlerts(snapshot({}), snapshot({ reviews: [records] }));
  assert.equal(alert.text, "Goblin a is waiting on you: Add the DNS records in Cloudflare then tell me");
});

test("a goblin's question to the CFO alerts nothing, whichever reaches the board first", () => {
  // As the supervisor serves it: the goblin's blocked notify sets its task
  // blocked, Waiting on the CFO, before or as its question reaches the
  // board. The CFO answers it, so neither its question nor its blocked task
  // is news for the Overlord.
  const asked = "May I finish the remaining heavy steps now, or stay paused?";
  const working = snapshot({ tasks: [task("a", "working", { report: "working" })] });
  const pending = question("notify-a-7", { task: "a", text: asked });
  for (const phase of ["blocked", "failed"]) {
    const blocked = task("a", phase, { report: phase, reason: "Waiting on the CFO: " + asked, activity: asked + " options: Finish it now (Recommended) | Stay paused" });
    const orders: [string, Snapshot[]][] = [
      ["both in one snapshot", [working, snapshot({ tasks: [blocked], questions: [pending] })]],
      ["the task blocked first", [working, snapshot({ tasks: [blocked] }), snapshot({ tasks: [blocked], questions: [pending] })]],
      ["the question first", [working, snapshot({ questions: [pending] }), snapshot({ tasks: [blocked], questions: [pending] })]],
    ];
    for (const [name, snapshots] of orders) {
      const alerts = snapshots.slice(1).flatMap((next, i) => boardAlerts(snapshots[i], next));
      assert.deepEqual(alerts.map((alert) => alert.text), [], phase + ": " + name);
    }
  }
});

test("an item alerts once however often a snapshot or a reconnect brings it back, and the same wait filed again is a copy", () => {
  const working = snapshot({ tasks: [task("a", "working")] });
  const restarting = snapshot({ tasks: [] });
  const wait = (id: string, state = "open") => ({ ...review(id), title: "Waiting on you: Look at the card", state });
  const asking = snapshot({ questions: [question("q1")] });
  const waiting = snapshot({ reviews: [wait("waiting-a-3")] });
  const refiled = snapshot({ reviews: [wait("waiting-a-3", "withdrawn"), wait("waiting-a-5")] });
  let seen: readonly SeenAlert[] = [];
  const shown = (from: Snapshot, to: Snapshot) => {
    const sighting = unseen(boardAlerts(from, to), seen, MINUTE);
    seen = sighting.seen;
    return sighting.fresh.map((alert) => alert.text);
  };
  assert.deepEqual(shown(working, asking), ["The CFO asks: Which option?"]);
  assert.deepEqual(shown(asking, restarting), [], "the supervisor restarting");
  assert.deepEqual(shown(restarting, asking), [], "the same question in the restarted supervisor's snapshot");
  assert.deepEqual(shown(working, waiting), ["Goblin a is waiting on you: Look at the card"]);
  assert.deepEqual(shown(waiting, refiled), [], "the same wait filed again");
});

// The supervisor takes an ID again once it has dropped the record that used
// it, so the same ID with another created_at is a new item: it alerts, and it
// is announced under a name of its own, which the supervisor never recorded
// for the earlier one, whose alert has nothing left to open.
test("an ID published again after its item closed alerts and is announced as a new item", () => {
  // Arrange: he saw the first publication's alert, then answered it.
  const first = question("release-train-20261002", { text: "May I merge the release train?" });
  const again = question("release-train-20261002", { text: "May I merge the next release train?", created_at: "2026-10-12T09:00:00Z" });
  const shown = boardAlerts(snapshot({}), snapshot({ questions: [first] }));
  const seen = unseen(shown, [], MINUTE).seen;

  // Act: the supervisor dropped the answered record, and the ID is asked again ten days later.
  const alerts = boardAlerts(snapshot({ questions: [{ ...first, status: "succeeded" }] }), snapshot({ questions: [again] }));
  const sighting = unseen(alerts, seen, 10 * 24 * 60 * MINUTE);

  // Assert
  assert.deepEqual(sighting.fresh.map((alert) => alert.text), ["The CFO asks: May I merge the next release train?"]);
  assert.notEqual(announceKey(sighting.fresh[0]), announceKey(shown[0]), "each publication is announced under its own name");
  assert.equal(outlived(shown[0], snapshot({ questions: [again] })), true, "the earlier publication's alert has nothing left to open");
  assert.equal(outlived(sighting.fresh[0], snapshot({ questions: [again] })), false);
});

test("a question its page's card carried alerts nothing when the page closes and it shows as a card of its own", () => {
  // Arrange
  const page = { ...review("plan-1"), lavish: "http://127.0.0.1:4387/session/a", lavish_page: "C:\\work\\plan.html" };
  const carried = snapshot({ reviews: [page], questions: [question("notify-a-4", { page: "plan-1" })] });
  const alone = snapshot({ reviews: [{ ...page, state: "cleared" }], questions: [question("notify-a-4")] });

  // Act
  const alerts = boardAlerts(carried, alone);

  // Assert
  assert.deepEqual(alerts.map((alert) => alert.key), []);
});

test("an alert leaves with its item only once the snapshot shows the item closed", () => {
  // Arrange
  const asks = boardAlerts(snapshot({}), snapshot({ questions: [question("notify-a-4")] }))[0];
  const cases: [string, Snapshot, boolean][] = [
    ["its question still waits", snapshot({ questions: [question("notify-a-4")] }), false],
    ["its question was answered", snapshot({ questions: [question("notify-a-4", { status: "succeeded" })] }), true],
    ["its answer is on its way", snapshot({ questions: [question("notify-a-4", { status: "running" })] }), true],
    ["the supervisor is restarting and holds no items", snapshot({ tasks: [] }), false],
  ];

  for (const [name, next, want] of cases) {
    // Act
    const gone = outlived(asks, next);

    // Assert
    assert.equal(gone, want, name);
  }
});

test("an alert is announced under its item's key and publishing, whole", () => {
  // Arrange
  const asks = boardAlerts(snapshot({}), snapshot({ questions: [question("notify-a-4")] }))[0];
  const long = boardAlerts(snapshot({}), snapshot({ questions: [question("q".repeat(200))] }))[0];

  // Act
  const names = [announceKey(asks), announceKey(long)];

  // Assert
  assert.equal(names[0], "alert:question:notify-a-4@2026-09-27T01:00:00Z");
  assert.equal(names[1], "alert:question:" + "q".repeat(200) + "@2026-09-27T01:00:00Z");
});

// A Windows notification the desktop window raised by its own look at the
// board carries the alert's key alone, and a click on it opens that item.
test("an alert's key names the Command Center item it opens", () => {
  const alerts = boardAlerts(snapshot({}), snapshot({ questions: [question("q1")], reviews: [review("waiting-a-3")], runs: [run("c1")] }));
  assert.deepEqual(alerts.map((alert) => alertItem(alert.key)), alerts.map((alert) => alert.item));
  assert.deepEqual(alerts.map((alert) => alert.item), ["question:q1", "run:c1", "review:waiting-a-3"], "the CFO's items first, then each goblin's");
  assert.equal(alertItem("question:q1"), "question:q1", "a key without its publishing is the item's own");
});

test("a browser remembers the newest alerts it showed, a bounded few, and one snapshot shows each once", () => {
  const alert = (key: string, says = "says " + key) => ({ key, says }) as BoardAlert;
  const old: SeenAlert = { key: "k-old", says: "says k-old", at: 0 };
  const many = Array.from({ length: SEEN_LIMIT + 5 }, (_, i) => alert("k" + i));
  const { fresh, seen } = unseen([...many, alert("k0"), alert("k-new", "says k1")], [old], MINUTE);
  assert.equal(fresh.length, SEEN_LIMIT + 5);
  assert.equal(seen.length, SEEN_LIMIT);
  assert.deepEqual(seen.at(-1), { key: "k-new", says: "says k1", at: MINUTE });
  assert.deepEqual(unseen([alert("k-old"), alert("k3")], [old], MINUTE).fresh.map((one) => one.key), ["k3"]);
});

test("the same ask within five minutes is one event, and a real new item later alerts again", () => {
  const working = snapshot({ tasks: [task("a", "working")] });
  const restarting = snapshot({ tasks: [] });
  const wait = (id: string) => ({ ...review(id), title: "Waiting on you: Look at the card" });
  const ask = (id: string, details: string) => question(id, { text: "Which option?\n\n" + details });
  const steps: [string, number, Snapshot, Snapshot, string[]][] = [
    ["a wait", 20, working, snapshot({ reviews: [wait("waiting-a-3")] }), ["review:waiting-a-3@2026-09-27T01:00:00Z"]],
    ["the wait filed again under a new id", 22, working, snapshot({ reviews: [wait("waiting-a-5")] }), []],
    ["the wait folded under its new id, back after a restart", 32, restarting, snapshot({ reviews: [wait("waiting-a-5")] }), []],
    ["the wait filed again later", 40, working, snapshot({ reviews: [wait("waiting-a-7")] }), ["review:waiting-a-7@2026-09-27T01:00:00Z"]],
    ["an item already shown, back after a restart", 60, restarting, snapshot({ reviews: [wait("waiting-a-3")] }), []],
    ["a question", 70, working, snapshot({ questions: [ask("notify-a-8", "The port.")] }), ["question:notify-a-8@2026-09-27T01:00:00Z"]],
    ["another question with the same first line", 80, working, snapshot({ questions: [ask("notify-a-9", "The colour.")] }), ["question:notify-a-9@2026-09-27T01:00:00Z"]],
  ];
  let seen: readonly SeenAlert[] = [];
  for (const [name, minute, from, to, want] of steps) {
    const sighting = unseen(boardAlerts(from, to), seen, minute * MINUTE);
    seen = sighting.seen;
    assert.deepEqual(sighting.fresh.map((alert) => alert.key), want, name);
  }
});

test("each alert names who asks by its goblin's title, as its card does, and says one plain line", () => {
  const before = snapshot({});
  const cfoQuestion = question("q2", { task: "", text: "Merge the release now?\n\n- details" });
  const cases: [string, Snapshot, { speaker: string; text: string }][] = [
    ["a goblin's review item", snapshot({ reviews: [review("r1")] }), { speaker: "Goblin a", text: "Goblin a wants your review: Review the plan" }],
    ["a review item from a goblin no longer on the board", snapshot({ tasks: [], reviews: [review("r1")] }), { speaker: "a", text: "a wants your review: Review the plan" }],
    ["a goblin named with its harness", snapshot({ tasks: [task("a", "working", { title: "Fix the gate; Claude Code" })], reviews: [review("r1")] }), { speaker: "Fix the gate", text: "Fix the gate wants your review: Review the plan" }],
    ["a goblin whose title is a whole sentence", snapshot({ tasks: [task("a", "working", { title: LONG_TITLE })], reviews: [review("r1")] }),
      { speaker: LONG_TITLE, text: "Answers given in a review page's editor are never lost (a d… wants your review: Review the plan" }],
    ["the CFO's question", snapshot({ questions: [cfoQuestion] }), { speaker: "CFO", text: "The CFO asks: Merge the release now?" }],
    ["a command to run", snapshot({ runs: [run("c1")] }), { speaker: "CFO", text: "A command waits for you to run it: Restart the board" }],
    ["a goblin's command to run", snapshot({ runs: [{ ...run("c2"), task: "a", title: "Sign in to GitHub" }] }), { speaker: "Goblin a", text: "Goblin a asks you to run a command: Sign in to GitHub" }],
  ];
  for (const [name, next, want] of cases) {
    const [alert] = boardAlerts(before, next);
    assert.deepEqual({ speaker: alert.speaker, text: alert.text }, want, name);
  }
});

test("a question that opens with a table of values alerts with its first words, not the table", () => {
  const [alert] = boardAlerts(snapshot({}), snapshot({ questions: [question("q1", { text: "| Type | Name |\n| --- | --- |\n| CNAME | `mcp` |\nMay I add these records?" })] }));
  assert.equal(alert.text, "The CFO asks: May I add these records?");
});

test("an alert names who asks and what, in a few words", () => {
  const [alert] = boardAlerts(snapshot({}), snapshot({ questions: [question("q1", { text: "Ship **now**? " + "x".repeat(300) + "\n\nThe details." })] }));
  assert.equal(alert.speaker, "CFO");
  assert.ok(alert.text.startsWith("The CFO asks: Ship now?") && alert.text.length <= 160 && !alert.text.includes("details"), alert.text);
  const [short] = boardAlerts(snapshot({}), snapshot({ questions: [question("q2")] }));
  assert.equal(short.text, "The CFO asks: Which option?");
});

// The Overlord, 2026-10-02: system notifications are only for when the app
// is minimized, not on every notification.
test("a Windows notification is only for a board out of sight, once allowed", () => {
  const cases: [string, NotificationPermission | "unsupported", boolean, boolean][] = [
    ["a hidden tab or a minimized window", "granted", true, true],
    ["a board on screen, in front or not", "granted", false, false],
    ["not allowed", "denied", true, false],
    ["not answered yet", "default", true, false],
    ["a browser without notifications", "unsupported", true, false],
  ];
  for (const [name, permission, hidden, want] of cases) assert.equal(notifies(permission, hidden), want, name);
});

test("the board asks for notifications once, and only while the browser has not been answered", () => {
  assert.equal(asksPermission("default", false), true);
  assert.equal(asksPermission("default", true), false, "asked before");
  assert.equal(asksPermission("granted", false), false);
  assert.equal(asksPermission("denied", false), false);
  assert.equal(asksPermission("unsupported", false), false);
});
