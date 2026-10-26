import { test } from "node:test";
import assert from "node:assert/strict";
import { parseCommands, slashOptions } from "./commands.ts";

test("slash options filter as he types the command word", () => {
  const commands = ["compact", "context", "cost", "review"];
  assert.deepEqual(slashOptions(commands, "/"), commands.slice(0, 4));
  assert.deepEqual(slashOptions(commands, "/co"), ["compact", "context", "cost"]);
  assert.deepEqual(slashOptions(commands, "/cos"), ["cost"]);
  assert.deepEqual(slashOptions(commands, "/compact "), [], "arguments leave the list closed");
  assert.deepEqual(slashOptions(commands, "hello"), []);
});

test("a harness's command list parses, and a missing one is empty", () => {
  assert.deepEqual(parseCommands({ commands: ["compact", "review"] }), ["compact", "review"]);
  assert.deepEqual(parseCommands({}), []);
});
