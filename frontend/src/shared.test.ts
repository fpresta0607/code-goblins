import test from "node:test";
import assert from "node:assert/strict";
import { shared } from "./shared.ts";

test("an update that changes nothing keeps the whole prior value", () => {
  // Arrange
  const prior = { revision: 1, tasks: [{ id: "a", title: "A" }, { id: "b", title: "B" }], memory: { available: 9 } };
  const next = structuredClone(prior);

  // Act
  const kept = shared(prior, next);

  // Assert
  assert.equal(kept, prior);
});

test("an update keeps each part it left as it was and replaces each it changed", () => {
  // Arrange
  const prior = { revision: 1, tasks: [{ id: "a", title: "A" }, { id: "b", title: "B" }], memory: { available: 9 } };
  const next = { revision: 2, tasks: [{ id: "a", title: "A" }, { id: "b", title: "Bee" }], memory: { available: 8 } };

  // Act
  const kept = shared(prior, next);

  // Assert
  assert.notEqual(kept, prior);
  assert.equal(kept.tasks[0], prior.tasks[0]);
  assert.notEqual(kept.tasks[1], prior.tasks[1]);
  assert.deepEqual(kept, next);
});

test("items with an id are matched by it, so a list that gains one keeps the rest", () => {
  // Arrange
  const prior = { tasks: [{ id: "a", title: "A" }, { id: "b", title: "B" }] };
  const next = { tasks: [{ id: "new", title: "New" }, { id: "a", title: "A" }, { id: "b", title: "B" }] };

  // Act
  const kept = shared(prior, next);

  // Assert
  assert.equal(kept.tasks[1], prior.tasks[0]);
  assert.equal(kept.tasks[2], prior.tasks[1]);
  assert.deepEqual(kept, next);
});

test("a key the update removed or added is a change", () => {
  for (const [prior, next] of [[{ a: 1, b: 2 }, { a: 1 }], [{ a: 1 }, { a: 1, b: undefined }]] as const) {
    // Act
    const kept = shared(prior, next);

    // Assert
    assert.notEqual(kept, prior);
    assert.deepEqual(kept, next);
  }
});
