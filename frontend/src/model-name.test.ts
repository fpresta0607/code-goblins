import assert from "node:assert/strict";
import test from "node:test";
import { shortModel } from "./model-name.ts";

test("a model is shown by its short name", () => {
  for (const [model, short] of [
    ["claude-opus-5-5", "Opus 5.5"],
    ["claude-opus-5-5[1m]", "Opus 5.5"],
    ["claude-sonnet-4-5-20250929", "Sonnet 4.5"],
    ["claude-fable-5-1", "Fable 5.1"],
    ["claude-haiku-5", "Haiku 5"],
    ["gpt-6", "GPT-6"],
    ["gpt-6-astra", "GPT-6 Astra"],
    ["gpt-6.1-sol", "GPT-6.1 Sol"],
    ["z-ai/glm-5.3-flash", "GLM 5.3 Flash"],
    ["deepseek-v4.1-flash", "Deepseek V4.1 Flash"],
    ["kimi-k2", "Kimi K2"],
    ["  ", ""],
  ] as const) {
    // Act and Assert
    assert.equal(shortModel(model), short, model);
  }
});
