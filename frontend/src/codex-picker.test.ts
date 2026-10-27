import assert from "node:assert/strict";
import { test } from "node:test";
import { CODEX_NOT_OFFERED, driveCodexPicker } from "./codex-picker.ts";
import { offerLink, type TerminalLink } from "./terminal-link.ts";
import type { HarnessPanel, ScreenState } from "./terminalStream.ts";

const MODELS: HarnessPanel = { rows: ["Select Model and Effort"], hints: [], choices: [{ text: "gpt-6-astra (current)", number: 1, isFocused: true }, { text: "gpt-6.1-sol", number: 2, isFocused: false }] };
const EFFORTS: HarnessPanel = { rows: ["Select Reasoning Level"], hints: [], choices: [{ text: "Low", number: 1, isFocused: false }, { text: "Medium (default)", number: 2, isFocused: true }, { text: "High", number: 3, isFocused: false }] };

// terminal is a link the test shows screens through, recording the keys the
// box sent and the Escapes it pressed.
function terminal(key: string) {
  const sent: string[] = [];
  let escapes = 0;
  const link: TerminalLink = {
    phase: "live", isReconnecting: false, isDictating: false, stopDictation: () => {}, feedback: "", isUnread: false, held: "", screen: null,
    send: () => "s1", paste: () => true, escape: () => { escapes++; return true; }, control: (keys) => { sent.push(keys); return true; },
  };
  const show = (panel: HarnessPanel | null) => {
    const screen: ScreenState = { isAsking: !!panel, isOpen: !!panel, isWorking: false, hasStarted: false, submission: "", panel, mode: "" };
    offerLink(key, { ...link, screen });
  };
  return { sent, show, escapes: () => escapes };
}

test("driveCodexPicker picks the model, then the effort, by their digits", () => {
  // Arrange
  const codex = terminal("codex-a");
  const results: string[] = [];
  driveCodexPicker("codex-a", { model: "gpt-6.1-sol", name: "GPT-6.1-Sol", effort: "high" }, 60_000, (failed) => results.push(failed));

  // Act
  codex.show(MODELS);
  codex.show(MODELS);
  codex.show(EFFORTS);
  const beforeClosing = [...results];
  codex.show(null);

  // Assert: it ends once Codex closes its picker, not before.
  assert.deepEqual(codex.sent, ["2", "3"]);
  assert.deepEqual(beforeClosing, []);
  assert.deepEqual(results, [""]);
  offerLink("codex-a", null);
});

test("driveCodexPicker ends after the model when Codex closes its picker without a reasoning list", () => {
  // Arrange
  const codex = terminal("codex-b");
  const results: string[] = [];
  driveCodexPicker("codex-b", { model: "gpt-6-astra", name: "", effort: "medium" }, 60_000, (failed) => results.push(failed));

  // Act
  codex.show(MODELS);
  codex.show(null);

  // Assert
  assert.deepEqual(codex.sent, ["1"]);
  assert.deepEqual(results, [""]);
  offerLink("codex-b", null);
});

test("driveCodexPicker closes a picker that never offers the choice and says so", async () => {
  // Arrange
  const codex = terminal("codex-c");
  const results: string[] = [];
  driveCodexPicker("codex-c", { model: "gpt-7", name: "", effort: "" }, 20, (failed) => results.push(failed));

  // Act
  codex.show(MODELS);
  await new Promise((resolve) => setTimeout(resolve, 60));

  // Assert
  assert.deepEqual(codex.sent, []);
  assert.equal(codex.escapes(), 1);
  assert.deepEqual(results, [CODEX_NOT_OFFERED]);
  offerLink("codex-c", null);
});
