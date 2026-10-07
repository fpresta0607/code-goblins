import { test } from "node:test";
import assert from "node:assert/strict";
import { showsFirstRun, startState } from "./firstRunStart.ts";
import type { Setup, SetupAgent } from "./types.ts";

const claude: SetupAgent = { id: "claude", name: "Claude Code", recommended: true, note: "the best experience", installed: true, sign_in: "signed_in", reason: "" };
const others: SetupAgent[] = [
  { id: "codex", name: "Codex", recommended: false, note: "woken by a typed line; no digest or guards", installed: true, sign_in: "signed_in", reason: "" },
  { id: "pi", name: "pi", recommended: false, note: "woken by a typed line; no digest, guards or resume", installed: false, sign_in: "unknown", reason: "Install pi to start the CFO" },
];
const setup = (changes: Partial<Setup> = {}): Setup => ({ home: "C:\\CodeGoblins", agent: "", projects_root: "C:\\dev", checkouts: ["alpha", "beta"], problem: "", agents: [claude, ...others], cfo_runs: false, ...changes });

test("Start uses the picked agent, or the one the quick start remembered, or the recommended one, and needs no project", () => {
  const noCheckout = "No git checkout is in this folder; pick the folder that holds your projects.";
  const cases: [string, Setup, string, boolean, { agent: string; blocked: string }][] = [
    ["nothing chosen anywhere", setup(), "", false, { agent: "claude", blocked: "" }],
    ["the agent the quick start remembered, which starts like any other", setup({ agent: "codex" }), "", false, { agent: "codex", blocked: "" }],
    ["a tab picked over the remembered agent", setup({ agent: "codex" }), "claude", false, { agent: "claude", blocked: "" }],
    ["a pick that is no agent of this machine", setup({ agent: "pi" }), "gemini", false, { agent: "pi", blocked: "Install pi to start the CFO" }],
    ["a remembered name that is no agent", setup({ agent: "kimi" }), "", false, { agent: "claude", blocked: "" }],
    ["two projects and none picked, since none is asked for", setup(), "", false, { agent: "claude", blocked: "" }],
    ["no projects folder at all", setup({ projects_root: "", checkouts: [] }), "", false, { agent: "claude", blocked: "" }],
    ["a recorded folder without projects that he never looked at", setup({ checkouts: [], problem: noCheckout }), "", false, { agent: "claude", blocked: "" }],
    ["a folder he looked at that has no projects", setup({ checkouts: [], problem: noCheckout }), "", true, { agent: "claude", blocked: noCheckout }],
    ["a Claude Code it cannot start", setup({ agents: [{ ...claude, installed: false, reason: "Install Claude Code to start the CFO" }, ...others] }), "", false, { agent: "claude", blocked: "Install Claude Code to start the CFO" }],
    ["a page that recommends none and remembers none", setup({ agents: others }), "", false, { agent: "", blocked: "This board offers no agent to start the CFO as." }],
    ["the recommended agent wherever it stands in the list", setup({ agents: [...others, claude] }), "", false, { agent: "claude", blocked: "" }],
  ];
  for (const [name, given, picked, lookedAtFolder, want] of cases) assert.deepEqual(startState(given, picked, lookedAtFolder), want, name);
});

test("the board's root shows the first-run page only in a home that has had no CFO, unless he just started one or chose the board", () => {
  const cases: [string, Parameters<typeof showsFirstRun>[0], boolean][] = [
    ["no CFO", { cfoRuns: false, cfoClosed: false, choice: "" }, true],
    ["a CFO runs", { cfoRuns: true, cfoClosed: false, choice: "" }, false],
    ["just started, before the board sees it", { cfoRuns: false, cfoClosed: false, choice: "started" }, false],
    ["the board without a CFO, by his choice", { cfoRuns: false, cfoClosed: false, choice: "board" }, false],
    ["the board's Start the CFO, even after a start the board never saw run", { cfoRuns: false, cfoClosed: false, choice: "" }, true],
    ["a CFO that was closed: the home has had one, so the board stays", { cfoRuns: false, cfoClosed: true, choice: "" }, false],
  ];
  for (const [name, given, want] of cases) assert.equal(showsFirstRun(given), want, name);
});
