import { test } from "node:test";
import assert from "node:assert/strict";
import { callWords } from "./call-words.ts";

test("a call reads with its tool's icon and verb, as it runs and once it has run, for every harness's tools", () => {
  const cases: [string, boolean, ReturnType<typeof callWords>][] = [
    ["Read", false, { icon: "file", verb: "Read" }],
    ["read", true, { icon: "file", verb: "Reading" }],
    ["Edit", false, { icon: "edit", verb: "Edited" }],
    ["apply_patch", true, { icon: "edit", verb: "Editing" }],
    ["Bash", true, { icon: "terminal", verb: "Running" }],
    ["exec_command", false, { icon: "terminal", verb: "Ran" }],
    ["Grep", false, { icon: "search", verb: "Searched" }],
    ["TodoWrite", false, { icon: "task", verb: "Updated the plan" }],
    ["update_plan", false, { icon: "task", verb: "Updated the plan" }],
    ["mcp__docs__fetch", false, { icon: "plug", verb: "Called docs fetch" }],
    ["mcp__docs__fetch", true, { icon: "plug", verb: "Calling docs fetch" }],
    ["SomethingNew", false, { icon: "sparkle", verb: "SomethingNew" }],
  ];
  for (const [tool, isRunning, want] of cases) assert.deepEqual(callWords(tool, isRunning), want, tool + (isRunning ? " running" : ""));
});
