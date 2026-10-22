import { test } from "node:test";
import assert from "node:assert/strict";
import { mirroredEdit } from "./dictated-input.ts";

test("a stream of growing partial results sends the final words once", () => {
  // Arrange
  const partials = ["", "Why", "Why is", "Why is every", "Why is everything", "Why is everything double printing"];

  // Act
  const keys = partials.slice(1).map((partial, index) => mirroredEdit(partials[index], partial, 0)).join("");

  // Assert
  assert.equal(keys, "Why is everything double printing");
});

for (const [name, before, after, floor, expected] of [
  ["text added at the end is typed", "ab", "abc", 0, "c"],
  ["a changed word is erased back to the change", "every thing", "everything", 0, "\x7f".repeat(6) + "thing"],
  ["text taken away is erased", "abc", "a", 0, "\x7f\x7f"],
  ["no change sends nothing", "same", "same", 0, ""],
  ["an empty box typed into sends the text", "", "hi", 0, "hi"],
  ["text before a typed key is never erased", "Why is", "Why's", 6, ""],
  ["a change after a typed key is typed", "Why is", "Why is now", 6, " now"],
  ["an emoji is erased with one key", "a🙂", "a", 0, "\x7f"],
  ["an emoji changed for another is never split", "a🙂", "a😀", 0, "\x7f😀"],
] as const) {
  test(name, () => {
    // Act
    const keys = mirroredEdit(before, after, floor);

    // Assert
    assert.equal(keys, expected);
  });
}
