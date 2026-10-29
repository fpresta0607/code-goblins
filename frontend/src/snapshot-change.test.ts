import test from "node:test";
import assert from "node:assert/strict";
import { applyChange } from "./snapshot-change.ts";

// Each change as internal/supervisor/snapshot_change_test.go has the
// supervisor send it, from the value the board holds to the next.
const cases: { name: string; held: unknown; change: unknown; next: unknown }[] = [
  { name: "a key that changed deep down", held: { a: 1, b: { c: 2, d: 4 } }, change: { keys: { b: { keys: { c: { value: 3 } } } } }, next: { a: 1, b: { c: 3, d: 4 } } },
  { name: "a key that came", held: { a: 1 }, change: { keys: { b: { value: [1] } } }, next: { a: 1, b: [1] } },
  { name: "keys that went", held: { a: 1, b: 2, c: 3 }, change: { gone: ["b", "c"] }, next: { a: 1 } },
  { name: "an item that changed, by its id", held: [{ id: "x", n: 1 }, { id: "y", n: 1 }], change: { items: { y: { keys: { n: { value: 2 } } } } }, next: [{ id: "x", n: 1 }, { id: "y", n: 2 }] },
  { name: "an item that came, with every id in order", held: [{ id: "x" }], change: { order: ["y", "x"], items: { y: { value: { id: "y", n: 1 } } } }, next: [{ id: "y", n: 1 }, { id: "x" }] },
  { name: "an item that went", held: [{ id: "x" }, { id: "y" }], change: { order: ["y"] }, next: [{ id: "y" }] },
  { name: "items that moved", held: [{ id: "x" }, { id: "y" }], change: { order: ["y", "x"] }, next: [{ id: "y" }, { id: "x" }] },
  { name: "a list sent whole", held: [1, 2], change: { value: [1, 3] }, next: [1, 3] },
  { name: "a value of another kind", held: { a: { b: 1 } }, change: { keys: { a: { value: null } } }, next: { a: null } },
];

for (const each of cases) {
  test(`a change gives the next value: ${each.name}`, () => {
    // Arrange
    const held = structuredClone(each.held);

    // Act
    const next = applyChange(held, each.change);

    // Assert
    assert.deepEqual(next, each.next);
    assert.deepEqual(held, each.held, "the value held is left as it was");
  });
}

test("a change keeps every part it does not name as the very part held", () => {
  // Arrange
  const held = { tasks: [{ id: "a", clock: 1 }, { id: "b", clock: 1 }], sessions: [{ id: "s" }] };

  // Act
  const next = applyChange(held, { keys: { tasks: { items: { b: { keys: { clock: { value: 2 } } } } } } }) as typeof held;

  // Assert
  assert.equal(next.sessions, held.sessions);
  assert.equal(next.tasks[0], held.tasks[0]);
  assert.deepEqual(next.tasks[1], { id: "b", clock: 2 });
});

const misfits: { name: string; held: unknown; change: unknown }[] = [
  { name: "keys for a value that is no object", held: [1], change: { keys: { a: { value: 1 } } } },
  { name: "items for a value that is no list", held: { a: 1 }, change: { order: ["x"] } },
  { name: "an id the board does not hold", held: [{ id: "x" }], change: { order: ["x", "y"] } },
  { name: "a change that is no object", held: { a: 1 }, change: 3 },
];

for (const each of misfits) {
  test(`a change that does not fit what the board holds throws: ${each.name}`, () => {
    assert.throws(() => applyChange(each.held, each.change));
  });
}
