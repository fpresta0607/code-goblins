import { test } from "node:test";
import assert from "node:assert/strict";
import { showsFirstRun, startState } from "./firstRunStart.ts";
import type { Setup, SetupAgent } from "./types.ts";

const claude: SetupAgent = { id: "claude", name: "Claude Code", installed: true, signed_in: true, reason: "" };
const codex: SetupAgent = { id: "codex", name: "Codex", installed: true, signed_in: true, reason: "" };
const setup = (changes: Partial<Setup> = {}): Setup => ({ home: "C:\\CodeGoblins", default_agent: "codex", problem: "", agents: [claude, codex], cfo_runs: false, ...changes });

test("Start chooses the remembered or selected agent without a project", () => {
  for (const [picked, expected] of [["", "codex"], ["claude", "claude"], ["unknown", "claude"]]) {
    assert.deepEqual(startState(setup(), picked), { agent: expected, blocked: "" });
  }
});

test("Start refuses missing, signed-out and unverifiable agents", () => {
  for (const changes of [{ installed: false }, { signed_in: false }, { installed: false, signed_in: false }]) {
    const result = startState(setup({ agents: [{ ...claude, ...changes, reason: "Sign-in needed" }], default_agent: "claude" }), "");
    assert.match(result.blocked, /goblins setup/);
  }
  assert.match(startState(setup({ agents: [] }), "").blocked, /goblins setup/);
  assert.equal(startState(setup({ problem: "Unreadable default" }), "").blocked, "Unreadable default");
});

test("the first-run page has no board-without-CFO choice", () => {
  assert.equal(showsFirstRun({ cfoRuns: false, choice: "" }), true);
  assert.equal(showsFirstRun({ cfoRuns: true, choice: "" }), false);
  assert.equal(showsFirstRun({ cfoRuns: false, choice: "started" }), false);
});
