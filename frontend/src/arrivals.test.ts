import { test } from "node:test";
import assert from "node:assert/strict";
import { countedTitle } from "./arrivals.ts";

test("the tab's title carries how many items wait on the Overlord", () => {
  assert.equal(countedTitle("Code Goblins", 3), "(3) Code Goblins");
  assert.equal(countedTitle("Code Goblins", 0), "Code Goblins");
});
