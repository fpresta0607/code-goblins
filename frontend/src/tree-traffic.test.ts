import { test } from "node:test";
import assert from "node:assert/strict";
import { treeTraffic, withDepartures } from "./tree-traffic.ts";
import type { FleetTree, TreeNode } from "./types.ts";

const child = (id: string, fields: Partial<TreeNode> = {}): TreeNode => ({
  id, kind: "subagent", group: "", parent: "", label: id, detail: "", task: "", state: "working", started: "2026-10-12T12:00:00Z",
  last_activity: "2026-10-12T12:01:00Z", finished: "", last_line: "", memory: 0, source_updated_at: "", fetched_at: "", ...fields,
});
const tree = (...children: TreeNode[]): FleetTree => ({ task_id: "g", generation: "g1", harness: "claude", memory: 0, own_memory: 0, conversation_at: "", children, unread: [], source_updated_at: "", fetched_at: "" });

test("the first snapshot moves nothing, since nothing came before it", () => {
  // Arrange
  const trees = { "task:pip": tree(child("a"), child("b")) };

  // Act
  const traffic = treeTraffic(null, trees);

  // Assert
  assert.deepEqual([traffic.born, traffic.left, traffic.reported], [[], [], []]);
  assert.deepEqual([...traffic.seen.get("task:pip")!].map((node) => node.id), ["a", "b"]);
});

test("a baby goblin new to its goblin's branches was born, and one gone or finished left from where it hung", () => {
  // Arrange
  const before = treeTraffic(null, { "task:pip": tree(child("a"), child("b"), child("c")) }).seen;

  // Act
  const traffic = treeTraffic(before, { "task:pip": tree(child("a"), child("b", { state: "done" }), child("d")) });

  // Assert
  assert.deepEqual(traffic.born, [{ goblin: "task:pip", child: "d" }]);
  assert.deepEqual(traffic.left.map((gone) => [gone.goblin, gone.node.id, gone.index]), [["task:pip", "b", 1], ["task:pip", "c", 2]]);
  assert.deepEqual(traffic.reported, []);
});

test("a baby goblin reports when its activity, last line or state changes, and not otherwise", () => {
  for (const [fields, isReport] of [
    [{ last_activity: "2026-10-12T12:02:00Z" }, true], [{ last_line: "ok internal/monitor" }, true], [{ state: "waiting" }, false],
    [{ memory: 4 * 2 ** 20 }, false], [{}, false],
  ] as [Partial<TreeNode>, boolean][]) {
    // Arrange
    const before = treeTraffic(null, { "task:pip": tree(child("a")) }).seen;

    // Act
    const traffic = treeTraffic(before, { "task:pip": tree(child("a", fields)) });

    // Assert
    assert.deepEqual(traffic.reported, isReport ? [{ goblin: "task:pip", child: "a" }] : [], JSON.stringify(fields));
  }
});

test("a goblin whose last baby goblin finished has them all leave", () => {
  // Arrange
  const before = treeTraffic(null, { "task:pip": tree(child("a")) }).seen;

  // Act
  const traffic = treeTraffic(before, {});

  // Assert
  assert.deepEqual(traffic.left.map((gone) => gone.node.id), ["a"]);
});

test("a baby goblin leaving keeps its place among those that stay until it goes", () => {
  // Arrange
  const [a, b, c, d] = ["a", "b", "c", "d"].map((id) => child(id));

  // Act
  const shown = withDepartures([a, c, d], [{ goblin: "task:pip", node: b, index: 1 }]);

  // Assert
  assert.deepEqual(shown.map((node) => node.id), ["a", "b", "c", "d"]);
  assert.deepEqual(withDepartures([], [{ goblin: "task:pip", node: b, index: 3 }]).map((node) => node.id), ["b"]);
});
