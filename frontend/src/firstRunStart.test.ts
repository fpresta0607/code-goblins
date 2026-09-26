import { test } from "node:test";
import assert from "node:assert/strict";
import { showsFirstRun, startState } from "./firstRunStart.ts";
import type { Setup, SetupAgent } from "./types.ts";

const claude: SetupAgent = { id: "claude", name: "Claude Code", installed: true, signed_in: true, reason: "" };
const others: SetupAgent[] = [
  { id: "codex", name: "Codex", installed: true, signed_in: true, reason: "Goblins can wake only a Claude Code CFO today" },
  { id: "pi", name: "Pi", installed: false, signed_in: false, reason: "Goblins can wake only a Claude Code CFO today" },
];
const setup = (changes: Partial<Setup> = {}): Setup => ({ projects_root: "C:\\dev", checkouts: ["alpha", "beta"], problem: "", agents: [claude, ...others], cfo_runs: false, ...changes });

test("Start takes the picked project, or a folder's only one, and says what it still needs", () => {
  const cases: [string, Setup, string, { project: string; blocked: string }][] = [
    ["a picked project", setup(), "beta", { project: "beta", blocked: "" }],
    ["a folder's only project, picked or not", setup({ checkouts: ["alpha"] }), "", { project: "alpha", blocked: "" }],
    ["two projects and no pick", setup(), "", { project: "", blocked: "Pick the project the CFO starts in." }],
    ["a pick the new folder lacks", setup({ checkouts: ["gamma", "delta"] }), "beta", { project: "", blocked: "Pick the project the CFO starts in." }],
    ["no folder yet", setup({ projects_root: "", checkouts: [] }), "", { project: "", blocked: "Enter the folder that holds your projects." }],
    ["a folder without projects", setup({ checkouts: [], problem: "No git checkout is in this folder; pick the folder that holds your projects." }), "", { project: "", blocked: "No git checkout is in this folder; pick the folder that holds your projects." }],
    ["a Claude Code it cannot start", setup({ agents: [{ ...claude, installed: false, reason: "Install Claude Code to start the CFO" }, ...others] }), "alpha", { project: "alpha", blocked: "Install Claude Code to start the CFO" }],
  ];
  for (const [name, given, picked, want] of cases) assert.deepEqual(startState(given, picked), want, name);
});

test("the board's root shows the first-run page whenever no CFO runs, unless he just started one or chose the board", () => {
  const cases: [string, Parameters<typeof showsFirstRun>[0], boolean][] = [
    ["no CFO", { cfoRuns: false, started: false, boardAnyway: false }, true],
    ["a CFO runs", { cfoRuns: true, started: false, boardAnyway: false }, false],
    ["just started, before the board sees it", { cfoRuns: false, started: true, boardAnyway: false }, false],
    ["the board without a CFO, by his choice", { cfoRuns: false, started: false, boardAnyway: true }, false],
  ];
  for (const [name, given, want] of cases) assert.equal(showsFirstRun(given), want, name);
});
