import { test } from "node:test";
import assert from "node:assert/strict";
import { parseCommands, slashOptions, type HarnessCommand } from "./commands.ts";

const command = (name: string, description = "", source: HarnessCommand["source"] = "builtin"): HarnessCommand => ({ name, description, source });

test("slash options filter as he types the command word: name first, then within a name, then by what it does", () => {
  // Arrange
  const commands = [command("compact", "Free up context"), command("context", "Visualize context usage"), command("cost"), command("review", "Review the current changes"), command("model", "Switch the AI model"), command("skill-doctor", "Show what each skill costs", "skill")];
  const names = (draft: string) => slashOptions(commands, draft).map((each) => each.name);

  // Act and Assert
  assert.deepEqual(names("/"), commands.map((each) => each.name), "a bare slash offers every command");
  assert.deepEqual(names("/co"), ["compact", "context", "cost", "skill-doctor"]);
  assert.deepEqual(names("/cos"), ["cost", "skill-doctor"]);
  assert.deepEqual(names("/CHANGES"), ["review"], "a word only its description holds still finds it");
  assert.deepEqual(names("/compact "), [], "arguments leave the list closed");
  assert.deepEqual(names("hello"), []);
  assert.deepEqual(names("/zzz"), []);
});

test("a harness's command list parses with what each does and where it comes from, and a malformed one is refused", () => {
  assert.deepEqual(parseCommands({ commands: [{ name: "compact", description: "Free up context", source: "builtin" }, { name: "review", source: "skill" }, { name: "", source: "command" }] }),
    [command("compact", "Free up context"), command("review", "", "skill")]);
  assert.deepEqual(parseCommands({}), []);
  for (const value of [{ commands: ["compact"] }, { commands: [{ name: "x", source: "plugin" }] }, { commands: [{ name: 1, source: "builtin" }] }, null]) assert.throws(() => parseCommands(value), JSON.stringify(value));
});
