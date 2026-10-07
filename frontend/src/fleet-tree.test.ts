import { test } from "node:test";
import assert from "node:assert/strict";
import { babyFor, canvasChildren, finished, forHowLong, formatMemory, isDimmed, running, silentChild, stateWord, summarize } from "./fleet-tree.ts";
import { makeRoom, NODE_HEIGHT, NODE_WIDTH, settle, workflowNodes } from "./workflow.ts";
import { parseSnapshot, type FleetTree, type TreeNode } from "./types.ts";

const MINUTE = 60_000;
const now = Date.parse("2026-10-06T21:00:00Z");
const ago = (minutes: number) => new Date(now - minutes * MINUTE).toISOString();

const child = (fields: Partial<TreeNode>): TreeNode => ({
  id: "x", kind: "subagent", group: "", parent: "", label: "", detail: "", state: "working", started: ago(10), last_activity: ago(1),
  finished: "", last_line: "", memory: 0, source_updated_at: ago(1), fetched_at: ago(0), ...fields,
});
const tree = (children: TreeNode[]): FleetTree => ({ task_id: "g", generation: "s1", harness: "claude", memory: 2 ** 30, own_memory: 0, conversation_at: "", children, unread: [], source_updated_at: "", fetched_at: "" });

test("each child is drawn as the baby goblin of its kind, a job as what it does", () => {
  for (const [fields, baby] of [
    [{ kind: "subagent" }, "subagent"], [{ kind: "shell", group: "test" }, "shell"], [{ kind: "monitor" }, "monitor"], [{ kind: "gate" }, "gate"],
    [{ kind: "process", group: "dev-server" }, "server"], [{ kind: "process", group: "test" }, "test"], [{ kind: "process", group: "build" }, "build"],
    [{ kind: "process", group: "browser" }, "browser"], [{ kind: "process", group: "other" }, "other"], [{ kind: "something new" }, "other"],
  ] as [Partial<TreeNode>, string][]) assert.equal(babyFor(child(fields)), baby, JSON.stringify(fields));
});

test("a child reads as working, idle, waiting, done, failed or silent, and idle and finished ones are dimmed", () => {
  for (const [fields, word, dimmed] of [
    [{ state: "working" }, "Working", false], [{ kind: "process", state: "waiting" }, "Idle", true], [{ kind: "gate", state: "waiting" }, "Waiting", false],
    [{ state: "done" }, "Done", true], [{ state: "failed" }, "Failed", true], [{ state: "silent" }, "Silent", false],
  ] as [Partial<TreeNode>, string, boolean][]) {
    assert.equal(stateWord(child(fields)), word);
    assert.equal(isDimmed(child(fields)), dimmed, word);
  }
});

test("for how long: working since it started, silent since it last did anything, finished how long ago", () => {
  assert.equal(forHowLong(child({ state: "working", started: ago(4) }), now), "4m");
  assert.equal(forHowLong(child({ state: "silent", started: ago(60), last_activity: ago(14) }), now), "14m");
  assert.equal(forHowLong(child({ state: "done", finished: ago(12) }), now), "12m ago");
  assert.equal(forHowLong(child({ state: "done", finished: ago(0) }), now), "just now");
  assert.equal(forHowLong(child({ state: "working", started: "0001-01-01T00:00:00Z" }), now), "");
});

test("memory reads in megabytes under a gigabyte, and nothing for none", () => {
  assert.equal(formatMemory(0), "");
  assert.equal(formatMemory(412 * 2 ** 20), "412 MB");
  assert.equal(formatMemory(1.14 * 2 ** 30), "1.1 GB");
  assert.equal(formatMemory(1000), "1 MB");
});

test("a goblin's children at a glance: counts by state, and how many of each kind still run", () => {
  const children = [
    child({ id: "a", kind: "subagent", state: "working" }), child({ id: "b", kind: "subagent", state: "done", finished: ago(12) }),
    child({ id: "c", kind: "process", group: "dev-server", state: "waiting" }), child({ id: "d", kind: "shell", state: "silent", last_activity: ago(30) }),
    child({ id: "e", kind: "shell", state: "silent", last_activity: ago(14) }), child({ id: "f", kind: "subagent", state: "failed", finished: ago(2) }),
  ];
  const summary = summarize(tree(children));
  assert.deepEqual({ ...summary, kinds: summary.kinds }, { working: 1, silent: 2, idle: 1, finished: 2, kinds: [["subagent", 1], ["shell", 2], ["server", 1]] });
  assert.deepEqual(running(tree(children)).map((node) => node.id), ["a", "c", "d", "e"]);
  assert.deepEqual(finished(tree(children)).map((node) => node.id), ["f", "b"], "newest first");
  assert.equal(silentChild(tree(children))?.id, "d", "the one silent longest");
  assert.equal(silentChild(tree([children[0]])), undefined);
});

test("the canvas shows every running child and only the newest finished ones", () => {
  const children = [child({ id: "run", state: "working" }), ...[1, 2, 3, 4, 5].map((minutes) => child({ id: "done" + minutes, state: "done", finished: ago(minutes) }))];
  assert.deepEqual(canvasChildren(tree(children)).map((node) => node.id), ["run", "done1", "done2", "done3"]);
});

test("rows under a goblin whose children show move down to make room, and keep their rows", () => {
  const positions = { cfo: { x: 400, y: 72 }, one: { x: 40, y: 316 }, two: { x: 376, y: 316 }, low: { x: 208, y: 560 } };
  assert.deepEqual(makeRoom(positions, {}), positions, "nothing open moves nothing");
  assert.deepEqual(makeRoom(positions, { one: 58 }), positions, "a count fits in the gap between rows");
  const room = makeRoom(positions, { one: 300 });
  assert.equal(room.cfo.y, 72);
  assert.equal(room.one.y, 316);
  assert.equal(room.two.y, 316, "a row moves as one");
  assert.ok(room.low.y >= 316 + NODE_HEIGHT + 300, "the row under the open goblin starts below its children");
  assert.equal(room.low.x, 208);
});

test("a snapshot carries each goblin's tree", () => {
  const snapshot = parseSnapshot({ healthy: true, instance: "x", tasks: [{ id: "g", title: "g", generation: "s1", phase: "working", verified: false,
    tree: { task_id: "g", generation: "s1", harness: "claude", memory: 3, own_memory: 1, children: [{ id: "subagent:t", kind: "subagent", label: "Map", state: "working", started: ago(4), memory: 0 }], source_updated_at: ago(0), fetched_at: ago(0) } }] });
  const parsed = snapshot.tasks[0].tree!;
  assert.equal(parsed.children[0].label, "Map");
  assert.deepEqual(parsed.unread, []);
  assert.equal(parseSnapshot({ healthy: true, instance: "x", tasks: [{ id: "q", title: "q", phase: "queued", verified: false }] }).tasks[0].tree, undefined);
});

test("a goblin the Overlord moved keeps its open children clear of every card arranged around it", () => {
  const arranged = { "task:a": { x: 0, y: 0 }, "task:b": { x: 0, y: 488 } };
  const placed = { "task:a": { x: 0, y: 300 } };
  const below = { "task:a": 400 };
  const shown = settle(arranged, placed, below);
  const a = shown["task:a"], b = shown["task:b"];
  assert.deepEqual(a, placed["task:a"], "his card stays where he put it");
  const clear = b.x >= a.x + NODE_WIDTH || b.x + NODE_WIDTH <= a.x || b.y >= a.y + NODE_HEIGHT + below["task:a"] || b.y + NODE_HEIGHT <= a.y;
  assert.ok(clear, `the card at ${JSON.stringify(b)} sits among the children under the moved goblin at ${JSON.stringify(a)}`);
});

test("a sub-agent its goblin's tree holds is a baby goblin under it, not a card of its own", () => {
  const sessions = [
    { id: "own", native_id: "own", harness: "claude", role: "goblin", task_id: "g" },
    { id: "a1", native_id: "a1", harness: "claude", role: "subagent", task_id: "g", parent: "own", relation: "delegated" },
  ];
  const goblin = { id: "g", phase: "working", verified: false };
  const held = { ...goblin, tree: { task_id: "g", generation: "s1", harness: "claude", children: [{ id: "subagent:a1", kind: "subagent", state: "working" }] } };
  const cards = (task: object) => workflowNodes(parseSnapshot({ healthy: true, tasks: [task], sessions })).map((node) => node.id);
  assert.ok(cards(held).includes("session:own") && !cards(held).includes("session:a1"), "the tree holds the sub-agent: " + cards(held).join(", "));
  assert.ok(cards(goblin).includes("session:a1"), "with no tree to hold it, the sub-agent keeps its card");
});

test("a helper goblin is drawn as a goblin baby under its parent and named a helper", () => {
  assert.equal(babyFor(child({ kind: "helper" })), "helper");
  assert.deepEqual(summarize(tree([child({ id: "helper:g-h1", kind: "helper", state: "working" })])).kinds, [["helper", 1]]);
});

test("a helper its parent's tree holds is a baby goblin under its parent, not a card of its own", () => {
  const sessions = [
    { id: "own", native_id: "own", harness: "claude", role: "goblin", task_id: "g" },
    { id: "helper", native_id: "helper", harness: "claude", role: "goblin", task_id: "g-h1" },
  ];
  const helper = { id: "g-h1", parent: "g", phase: "working", verified: false };
  const parent = { id: "g", phase: "working", verified: false };
  const holding = { ...parent, tree: { task_id: "g", generation: "s1", harness: "claude", children: [{ id: "helper:g-h1", kind: "helper", state: "working" }] } };
  const cards = (tasks: object[], withSessions = true) => workflowNodes(parseSnapshot({ healthy: true, tasks, sessions: withSessions ? sessions : [] })).map((node) => node.id);
  assert.ok(cards([holding, helper]).includes("session:own") && !cards([holding, helper]).includes("session:helper"), "the tree holds the helper's session: " + cards([holding, helper]).join(", "));
  assert.ok(!cards([holding, helper], false).includes("task:g-h1"), "the tree holds the helper's task: " + cards([holding, helper], false).join(", "));
  assert.ok(cards([parent, helper]).includes("session:helper"), "a helper no tree holds keeps its card");
  assert.equal(parseSnapshot({ healthy: true, tasks: [helper] }).tasks[0].parent, "g");
  assert.equal(parseSnapshot({ healthy: true, tasks: [parent] }).tasks[0].parent, "");
});
