import { test } from "node:test";
import assert from "node:assert/strict";
import { CFO_KEY, MAXIMIZED_KEYS, cfoView, goblinView, idleView, keepLive, maximizedFor, maximizedView, paneTrack, paneWidth, switchKey, switchOrder, switchTarget, type DeckView } from "./terminalOrder.ts";
import type { Session, Task } from "./types.ts";
import { parseSnapshot } from "./types.ts";

test("terminal shortcuts exclude ended sessions but retain a live goblin that delivered a PR", () => {
  const snapshot = parseSnapshot({ healthy: true, tasks: [
    { id: "retired", generation: "g1", archived: true, verified: false },
    { id: "paused", generation: "g1", phase: "paused", verified: false },
    { id: "stopped", generation: "g1", phase: "stopped", verified: false },
    { id: "delivered", generation: "g1", phase: "done", verified: true, report: "done", runtime: { state: "idle" } },
  ] });
  assert.deepEqual(switchOrder(snapshot.tasks).map((entry) => entry.key), [CFO_KEY, "delivered"]);
});

const task = (id: string, changes: Partial<Task> = {}) => ({ id, generation: "g1", archived: false, ...changes }) as Task;
const key = (code: string, changes: Partial<{ ctrlKey: boolean; altKey: boolean; shiftKey: boolean; metaKey: boolean; altGraph: boolean }> = {}) => {
  const { altGraph = false, ...modifiers } = changes;
  return { code, ctrlKey: true, altKey: true, shiftKey: false, metaKey: false, ...modifiers, getModifierState: (name: string) => name === "AltGraph" && altGraph };
};

test("a slot with no terminal shows an empty state that belongs to no backend", () => {
  const cases: [string, DeckView, string][] = [
    ["no CFO runs", cfoView({ cfo_terminal: "", cfo_runs: false }), "No CFO is running."],
    ["a queued task", idleView(task("queued", { generation: "" })), "This task has not started yet."],
    ["a child of a task", idleView(task("alpha"), { id: "child" } as Session), "This child has no separate terminal."],
    ["a child with no task", idleView(undefined, { id: "child" } as Session), "This child has no separate terminal."],
    ["nothing selected", idleView(), "Select a goblin to see its terminal."],
  ];
  for (const [name, view, text] of cases) assert.deepEqual(view, { kind: "empty", text }, name);
});

test("a CFO or goblin still in Herdr keeps Herdr's view, and a native one shows from its host", () => {
  assert.deepEqual(cfoView({ cfo_terminal: "", cfo_runs: true }), { kind: "herdr" });
  assert.deepEqual(goblinView(task("alpha")), { kind: "herdr" });
  assert.deepEqual(cfoView({ cfo_terminal: "cfo", cfo_runs: true }), { kind: "host", query: "cfo=cfo" });
  assert.deepEqual(goblinView(task("alpha", { backend: "native" })), { kind: "host", query: "task=alpha&generation=g1" });
});

test("a resuming or stopping goblin shows its transition instead of connecting to the old generation", () => {
  for (const backend of ["native", "herdr"]) {
    assert.deepEqual(goblinView(task("alpha", { backend, phase: "resuming" })), { kind: "empty", text: "Resuming session..." });
    assert.deepEqual(goblinView(task("alpha", { backend, phase: "stopping" })), { kind: "empty", text: "Stopping session..." });
  }
});

test("the switcher lists the CFO first, then every goblin that has a terminal", () => {
  const order = switchOrder([task("queued", { generation: "" }), task("alpha"), task("history", { archived: true }), task("beta")]);
  assert.deepEqual(order.map((entry) => entry.key), [CFO_KEY, "alpha", "beta"]);
  assert.equal(order[1].task?.id, "alpha");
});

test("Ctrl+Alt with an arrow cycles and with a digit jumps; nothing else is taken from the terminal", () => {
  assert.deepEqual(switchKey(key("ArrowDown")), { step: 1 });
  assert.deepEqual(switchKey(key("ArrowUp")), { step: -1 });
  assert.deepEqual(switchKey(key("Digit1")), { index: 0 });
  assert.deepEqual(switchKey(key("Numpad9")), { index: 8 });
  for (const other of [key("Digit1", { altGraph: true }), key("ArrowDown", { altGraph: true }), key("Digit0"), key("KeyA"), key("ArrowDown", { shiftKey: true }), key("ArrowDown", { altKey: false }), key("Digit2", { ctrlKey: false }), key("Digit2", { metaKey: true })]) assert.equal(switchKey(other), null, JSON.stringify(other));
});

test("cycling wraps around and a jump past the list goes nowhere", () => {
  const order = switchOrder([task("alpha"), task("beta")]);
  assert.equal(switchTarget(order, CFO_KEY, { step: -1 })?.key, "beta");
  assert.equal(switchTarget(order, "beta", { step: 1 })?.key, CFO_KEY);
  assert.equal(switchTarget(order, "alpha", { step: 1 })?.key, "beta");
  assert.equal(switchTarget(order, "gone", { step: 1 })?.key, CFO_KEY);
  assert.equal(switchTarget(order, CFO_KEY, { index: 2 })?.key, "beta");
  assert.equal(switchTarget(order, CFO_KEY, { index: 5 }), undefined);
});

test("every native terminal opened stays live, and Herdr views only within Herdr's stream limit", () => {
  const herdr = (key: string) => key.startsWith("h");
  let open: string[] = [];
  for (const next of ["n1", "h1", "h2", "n2", "h3", "h4", "n1"]) open = keepLive(open, next, herdr);
  assert.deepEqual(open, ["n1", "h4", "h3", "n2", "h2"]);
  assert.deepEqual(keepLive(["a", "b"], "b", herdr), ["b", "a"]);
});

test("the divider keeps both the board and the panel usable", () => {
  assert.equal(paneWidth(800, 1600), 800);
  assert.equal(paneWidth(100, 1600), 360);
  assert.equal(paneWidth(1500, 1600), 1310, "the board keeps 280 px beside the 10 px divider");
  assert.equal(paneWidth(500, 500), 360);
  assert.equal(paneWidth(Number.NaN, 1600), 800);
});

test("a saved panel width is held to the same bounds in any window", () => {
  assert.equal(paneTrack(2000), "clamp(360px, 2000px, calc(100% - 290px))", "a width saved on a wider screen never squeezes the board out");
  assert.equal(paneTrack(800), "clamp(360px, 800px, calc(100% - 290px))");
});

test("a goblin's terminal opens maximized and the task view beside the board, each keeping his last choice", () => {
  assert.equal(maximizedFor("terminal", null), true, "a terminal he never sized opens maximized");
  assert.equal(maximizedFor("task", null), false, "the task view opens beside the board");
  assert.equal(maximizedFor("terminal", "false"), false, "a terminal he restored stays restored");
  assert.equal(maximizedFor("task", "true"), true, "a task view he maximized stays maximized");
  assert.notEqual(MAXIMIZED_KEYS.terminal, MAXIMIZED_KEYS.task, "each view keeps its own choice");
});

test("only the Board opens a terminal maximized; Orchestration keeps its graph beside the panel", () => {
  assert.equal(maximizedView("Board", "terminal"), "terminal", "a goblin's terminal on the Board follows the terminal choice");
  assert.equal(maximizedView("Board", "task"), "task", "the Board's task view follows the task choice");
  assert.equal(maximizedView("Orchestration", "terminal"), "task", "an Orchestration terminal follows the task choice");
  assert.equal(maximizedView("Orchestration", "task"), "task");
  assert.equal(maximizedFor(maximizedView("Orchestration", "terminal"), null), false, "a first click on Orchestration shows the graph");
  assert.equal(maximizedFor(maximizedView("Orchestration", "terminal"), "true"), true, "Orchestration keeps a maximize he chose");
  assert.equal(maximizedFor(maximizedView("Board", "terminal"), null), true, "a goblin's terminal on the Board opens maximized");
});
