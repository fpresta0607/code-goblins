import { test } from "node:test";
import assert from "node:assert/strict";
import { parseCommands, sendable, slashOptions, type HarnessCommand } from "./commands.ts";

const command = (name: string, description = "", source: HarnessCommand["source"] = "builtin", sigil: HarnessCommand["sigil"] = "/"): HarnessCommand => ({ name, description, source, sigil });

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

test("a command named exactly what he typed comes first, ahead of the session's own longer names", () => {
  // Arrange: the supervisor lists the session's own commands before the built-ins.
  const commands = [command("clear-cache", "", "command"), command("model-routing", "", "skill"), command("clear"), command("model")];
  const names = (draft: string) => slashOptions(commands, draft).map((each) => each.name);

  // Act and Assert
  assert.deepEqual(names("/clear"), ["clear", "clear-cache"]);
  assert.deepEqual(names("/MODEL"), ["model", "model-routing"]);
  assert.deepEqual(names("/cle"), ["clear-cache", "clear"], "a word no command is named keeps the harness's order");
});

test("a harness's command list parses with what each does, where it comes from and its sigil, and a malformed one is refused", () => {
  assert.deepEqual(parseCommands({ commands: [{ name: "compact", description: "Free up context", source: "builtin", sigil: "/" }, { name: "review", source: "skill", sigil: "$" }, { name: "", source: "command", sigil: "/" }] }),
    [command("compact", "Free up context"), command("review", "", "skill", "$")]);
  assert.deepEqual(parseCommands({}), []);
  for (const value of [{ commands: ["compact"] }, { commands: [{ name: "x", source: "plugin", sigil: "/" }] }, { commands: [{ name: 1, source: "builtin", sigil: "/" }] }, { commands: [{ name: "x", source: "skill" }] }, { commands: [{ name: "x", source: "skill", sigil: "#" }] }, null]) assert.throws(() => parseCommands(value), JSON.stringify(value));
});

test("a slash offers Codex's skills beside its commands, and a dollar its skills alone", () => {
  // Arrange
  const commands = [command("alpha-notes", "Keep notes", "skill", "$"), command("kit:find-notes", "Find notes", "skill", "$"), command("model", "A skill named model", "skill", "$"), command("model", "choose what model to use"), command("new")];
  const offered = (draft: string) => slashOptions(commands, draft).map((each) => each.sigil + each.name);

  // Act and Assert
  assert.deepEqual(offered("/"), ["$alpha-notes", "$kit:find-notes", "$model", "/model", "/new"]);
  assert.deepEqual(offered("/model"), ["/model", "$model"], "the command he named comes before a skill of its name");
  assert.deepEqual(offered("/notes"), ["$alpha-notes", "$kit:find-notes"]);
  assert.deepEqual(offered("$"), ["$alpha-notes", "$kit:find-notes", "$model"]);
  assert.deepEqual(offered("$mod"), ["$model"]);
  assert.deepEqual(offered("$alpha-notes now"), [], "arguments leave the list closed");
});

test("a message to Codex that ends in a $ word ends in a space, so Enter sends it rather than completing the word", () => {
  for (const [harness, text, want] of [
    ["codex", "$alpha-notes", "$alpha-notes "],
    ["codex", "Use $kit:find-notes", "Use $kit:find-notes "],
    ["codex", "$alpha-notes tidy the list", "$alpha-notes tidy the list"],
    ["codex", "costs $5", "costs $5 "],
    ["codex", "plain words", "plain words"],
    ["claude", "/kit-one:tidy", "/kit-one:tidy"],
    ["pi", "$alpha-notes", "$alpha-notes"],
  ]) assert.equal(sendable(harness, text), want, harness + ": " + text);
});
