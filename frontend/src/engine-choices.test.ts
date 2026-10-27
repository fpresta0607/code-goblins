import assert from "node:assert/strict";
import { test } from "node:test";
import { codexChoice, effortsInOrder, effortTone, engineCommand, settingCommand } from "./engine-choices.ts";
import type { HarnessPanel } from "./terminalStream.ts";

const panel = (...texts: string[]): HarnessPanel => ({ rows: [], hints: [], choices: texts.map((text, at) => ({ text, number: at + 1, isFocused: at === 0 })) });

test("engineCommand sets Claude Code's and pi's model and effort at once, and leaves Codex to its picker", () => {
  // Arrange
  const cases: [string, "model" | "effort", string, string | null][] = [
    ["claude", "model", "claude-sonnet-5-5", "/model claude-sonnet-5-5"],
    ["claude", "effort", "xhigh", "/effort xhigh"],
    ["pi", "model", "openrouter/z-ai/glm-5.3-flash", "/model openrouter/z-ai/glm-5.3-flash"],
    ["pi", "effort", "high", "/thinking high"],
    ["codex", "model", "gpt-6-astra", null],
    ["codex", "effort", "high", null],
  ];

  for (const [harness, setting, value, want] of cases) {
    // Act
    const got = engineCommand(harness, setting, value);

    // Assert
    assert.equal(got, want, harness + " " + setting);
  }
});

test("settingCommand reads the settings the box has a control for, and nothing else", () => {
  // Arrange
  const cases: [string, string, ReturnType<typeof settingCommand>][] = [
    ["claude", "/effort", { setting: "effort", value: "" }],
    ["claude", "/effort  high ", { setting: "effort", value: "high" }],
    ["claude", "/model sonnet", { setting: "model", value: "sonnet" }],
    ["codex", "/model", { setting: "model", value: "" }],
    ["pi", "/thinking low", { setting: "effort", value: "low" }],
    ["claude", "/thinking", null],
    ["pi", "/effort", null],
    ["claude", "/models", null],
    ["claude", "please /model", null],
    ["claude", "", null],
  ];

  for (const [harness, text, want] of cases) {
    // Act
    const got = settingCommand(harness, text);

    // Assert
    assert.deepEqual(got, want, harness + " " + JSON.stringify(text));
  }
});

test("codexChoice finds the model by slug or name and the effort by its word, never a longer name", () => {
  // Arrange
  const models = panel("gpt-6-sol-mini  Small and fast", "gpt-6-sol (current)  Frontier", "GPT-6.1 Terra  Balanced");
  const efforts = panel("Low", "Medium (default)", "High", "Extra high");

  // Act and Assert
  assert.equal(codexChoice(models, "model", "gpt-6-sol"), 1);
  assert.equal(codexChoice(models, "model", "gpt-6.1-terra", "GPT-6.1 Terra"), 2);
  assert.equal(codexChoice(models, "model", "gpt-7"), -1);
  assert.equal(codexChoice(efforts, "effort", "medium"), 1);
  assert.equal(codexChoice(efforts, "effort", "xhigh"), 3);
  assert.equal(codexChoice(efforts, "effort", "minimal"), -1);
  assert.equal(codexChoice(models, "effort", "high"), -1);
});

test("effort levels sort from the least to the most and take their tones", () => {
  // Arrange
  const levels = ["max", "low", "xhigh", "medium", "high", "minimal", "off"];

  // Act
  const ordered = effortsInOrder(levels);

  // Assert
  assert.deepEqual(ordered, ["off", "minimal", "low", "medium", "high", "xhigh", "max"]);
  assert.deepEqual(ordered.map(effortTone), ["dull", "dull", "light", "green", "teal", "purple", "max"]);
  assert.equal(effortTone("auto"), "plain");
});
