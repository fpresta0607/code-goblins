import assert from "node:assert/strict";
import { test } from "node:test";
import { cycleMode, MODE_NOT_SENT } from "./mode-cycle.ts";
import { offerLink, type TerminalLink } from "./terminal-link.ts";
import type { ScreenState } from "./terminalStream.ts";

// terminal is a link whose footer names mode, which the test changes as the
// harness would, recording the keys the box pressed.
function terminal(key: string, mode: string, isTaken = true) {
  const sent: string[] = [];
  const link = (named: string): TerminalLink => ({
    phase: "live", isReconnecting: false, isDictating: false, stopDictation: () => {}, feedback: "", isUnread: false, held: "",
    screen: { isAsking: false, isOpen: false, isWorking: false, hasStarted: true, submission: "", panel: null, mode: named, hasDraft: false, doing: "" } satisfies ScreenState,
    send: () => "s1", paste: () => true, escape: () => true, control: (keys) => { sent.push(keys); return isTaken; },
  });
  const first = link(mode);
  offerLink(key, first);
  return { first, sent, show: (named: string) => offerLink(key, link(named)) };
}

test("cycleMode presses Shift+Tab once per mode until the footer names the one he picked", () => {
  // Arrange
  const claude = terminal("mode-a", "bypass permissions");
  const results: string[] = [];

  // Act
  cycleMode("mode-a", claude.first, "plan mode", "Plan", 60_000, (failed) => results.push(failed));
  claude.show("bypass permissions");
  claude.show("manual mode");
  claude.show("accept edits");
  const beforePlan = [...results];
  claude.show("plan mode");

  // Assert: one press per mode the footer named, none while a press had not
  // landed, and none after.
  assert.deepEqual(claude.sent, ["\x1b[Z", "\x1b[Z", "\x1b[Z"]);
  assert.deepEqual(beforePlan, []);
  assert.deepEqual(results, [""]);
  offerLink("mode-a", null);
});

test("cycleMode stops where it started when the session never offers the mode", () => {
  // Arrange
  const claude = terminal("mode-b", "manual mode");
  const results: string[] = [];

  // Act
  cycleMode("mode-b", claude.first, "auto mode", "Auto", 60_000, (failed) => results.push(failed));
  claude.show("accept edits");
  claude.show("plan mode");
  claude.show("manual mode");

  // Assert
  assert.equal(claude.sent.length, 3);
  assert.deepEqual(results, ["The session does not offer Auto."]);
  offerLink("mode-b", null);
});

test("cycleMode presses nothing for the mode the session is in", () => {
  // Arrange
  const claude = terminal("mode-c", "plan mode");
  const results: string[] = [];

  // Act
  cycleMode("mode-c", claude.first, "plan mode", "Plan", 60_000, (failed) => results.push(failed));

  // Assert
  assert.deepEqual(claude.sent, []);
  assert.deepEqual(results, [""]);
  offerLink("mode-c", null);
});

test("cycleMode says so when the terminal does not take the press", () => {
  // Arrange
  const claude = terminal("mode-d", "manual mode", false);
  const results: string[] = [];

  // Act
  cycleMode("mode-d", claude.first, "plan mode", "Plan", 60_000, (failed) => results.push(failed));

  // Assert
  assert.deepEqual(results, [MODE_NOT_SENT]);
  offerLink("mode-d", null);
});

test("cycleMode gives up after its wait when the footer never changes", async () => {
  // Arrange
  const claude = terminal("mode-e", "manual mode");
  const results: string[] = [];

  // Act
  cycleMode("mode-e", claude.first, "plan mode", "Plan", 10, (failed) => results.push(failed));
  await new Promise((resolve) => setTimeout(resolve, 30));

  // Assert
  assert.deepEqual(claude.sent, ["\x1b[Z"]);
  assert.deepEqual(results, ["The session did not change its mode to Plan."]);
  offerLink("mode-e", null);
});
