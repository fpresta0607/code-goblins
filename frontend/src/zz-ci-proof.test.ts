import test from "node:test";
import assert from "node:assert/strict";

// Throwaway, removed before merge: its failure must turn the test check red.
test("deliberate failure: proof that the frontend job turns the test check red", () => {
  assert.fail("deliberate failure");
});
