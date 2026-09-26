import { test } from "node:test";
import assert from "node:assert/strict";
import { CFO_KEY, keepLive, paneTrack, paneWidth, switchKey, switchOrder, switchTarget } from "./terminalOrder.ts";
import type { Task } from "./types.ts";

const task = (id: string, changes: Partial<Task> = {}) => ({ id, generation: "g1", archived: false, ...changes }) as Task;
const key = (code: string, changes: Partial<{ ctrlKey: boolean; altKey: boolean; shiftKey: boolean; metaKey: boolean; altGraph: boolean }> = {}) => {
  const { altGraph = false, ...modifiers } = changes;
  return { code, ctrlKey: true, altKey: true, shiftKey: false, metaKey: false, ...modifiers, getModifierState: (name: string) => name === "AltGraph" && altGraph };
};

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
