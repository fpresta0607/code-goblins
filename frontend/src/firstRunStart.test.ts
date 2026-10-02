import { test } from "node:test";
import assert from "node:assert/strict";
import { showsFirstRun, startState } from "./firstRunStart.ts";
import type { Setup, SetupAgent } from "./types.ts";

const claude: SetupAgent = { id: "claude", name: "Claude Code", installed: true, signed_in: true, reason: "" };
const others: SetupAgent[] = [
  { id: "codex", name: "Codex", installed: true, signed_in: true, reason: "Goblins can wake only a Claude Code CFO today" },
  { id: "pi", name: "Pi", installed: false, signed_in: false, reason: "Goblins can wake only a Claude Code CFO today" },
];
const setup = (changes: Partial<Setup> = {}): Setup => ({ home: "C:\\CodeGoblins", agent: "", projects_root: "C:\\dev", checkouts: ["alpha", "beta"], problem: "", agents: [claude, ...others], cfo_runs: false, ...changes });

test("Start uses the picked agent, or the one the quick start remembered, or the recommended one, and needs no project", () => {
  const noCheckout = "No git checkout is in this folder; pick the folder that holds your projects.";
  const cases: [string, Setup, string, boolean, { agent: string; blocked: string }][] = [
    ["nothing chosen anywhere", setup(), "", false, { agent: "claude", blocked: "" }],
    ["the agent the quick start remembered", setup({ agent: "codex" }), "", false, { agent: "codex", blocked: "Goblins can wake only a Claude Code CFO today" }],
    ["a tab picked over the remembered agent", setup({ agent: "codex" }), "claude", false, { agent: "claude", blocked: "" }],
    ["a pick that is no agent of this machine", setup({ agent: "pi" }), "gemini", false, { agent: "pi", blocked: "Goblins can wake only a Claude Code CFO today" }],
    ["a remembered name that is no agent", setup({ agent: "kimi" }), "", false, { agent: "claude", blocked: "" }],
    ["two projects and none picked, since none is asked for", setup(), "", false, { agent: "claude", blocked: "" }],
    ["no projects folder at all", setup({ projects_root: "", checkouts: [] }), "", false, { agent: "claude", blocked: "" }],
    ["a recorded folder without projects that he never looked at", setup({ checkouts: [], problem: noCheckout }), "", false, { agent: "claude", blocked: "" }],
    ["a folder he looked at that has no projects", setup({ checkouts: [], problem: noCheckout }), "", true, { agent: "claude", blocked: noCheckout }],
    ["a Claude Code it cannot start", setup({ agents: [{ ...claude, installed: false, reason: "Install Claude Code to start the CFO" }, ...others] }), "", false, { agent: "claude", blocked: "Install Claude Code to start the CFO" }],
    ["a machine whose page lists no Claude Code", setup({ agents: others }), "", false, { agent: "claude", blocked: "This board cannot start Claude Code." }],
  ];
  for (const [name, given, picked, lookedAtFolder, want] of cases) assert.deepEqual(startState(given, picked, lookedAtFolder), want, name);
});

test("the board's root shows the first-run page whenever no CFO runs, unless he just started one or chose the board", () => {
  const cases: [string, Parameters<typeof showsFirstRun>[0], boolean][] = [
    ["no CFO", { cfoRuns: false, choice: "" }, true],
    ["a CFO runs", { cfoRuns: true, choice: "" }, false],
    ["just started, before the board sees it", { cfoRuns: false, choice: "started" }, false],
    ["the board without a CFO, by his choice", { cfoRuns: false, choice: "board" }, false],
    ["the board's Start the CFO, even after a start the board never saw run", { cfoRuns: false, choice: "" }, true],
  ];
  for (const [name, given, want] of cases) assert.equal(showsFirstRun(given), want, name);
});
