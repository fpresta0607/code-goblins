import { test } from "node:test";
import assert from "node:assert/strict";
import { showsFirstRun, startState } from "./firstRunStart.ts";
import type { Setup, SetupAgent } from "./types.ts";

const claude: SetupAgent = { id: "claude", name: "Claude Code", recommended: true, note: "the best experience", installed: true, sign_in: "signed_in", reason: "" };
const others: SetupAgent[] = [
  { id: "codex", name: "Codex", recommended: false, note: "woken by a typed line; no digest or guards", installed: true, sign_in: "signed_in", reason: "" },
  { id: "pi", name: "pi", recommended: false, note: "woken by a typed line; no digest or guards", installed: false, sign_in: "unknown", reason: "Install pi to start the CEO" },
];
const setup = (changes: Partial<Setup> = {}): Setup => ({ home: "C:\\CodeGoblins", agent: "", projects_root: "C:\\dev", checkouts: ["alpha", "beta"], problem: "", agents: [claude, ...others], ceo_runs: false, ...changes });

test("Start uses the picked agent, or the one the quick start remembered, or the recommended one, and needs no project", () => {
  const noCheckout = "No git checkout is in this folder; pick the folder that holds your projects.";
  const cases: [string, Setup, string, boolean, { agent: string; blocked: string }][] = [
    ["nothing chosen anywhere", setup(), "", false, { agent: "claude", blocked: "" }],
    ["the agent the quick start remembered, which starts like any other", setup({ agent: "codex" }), "", false, { agent: "codex", blocked: "" }],
    ["a tab picked over the remembered agent", setup({ agent: "codex" }), "claude", false, { agent: "claude", blocked: "" }],
    ["a pick that is no agent of this machine", setup({ agent: "pi" }), "gemini", false, { agent: "pi", blocked: "Install pi to start the CEO" }],
    ["a remembered name that is no agent", setup({ agent: "kimi" }), "", false, { agent: "claude", blocked: "" }],
    ["two projects and none picked, since none is asked for", setup(), "", false, { agent: "claude", blocked: "" }],
    ["no projects folder at all", setup({ projects_root: "", checkouts: [] }), "", false, { agent: "claude", blocked: "" }],
    ["a recorded folder without projects that he never looked at", setup({ checkouts: [], problem: noCheckout }), "", false, { agent: "claude", blocked: "" }],
    ["a folder he looked at that has no projects", setup({ checkouts: [], problem: noCheckout }), "", true, { agent: "claude", blocked: noCheckout }],
    ["a Claude Code it cannot start", setup({ agents: [{ ...claude, installed: false, reason: "Install Claude Code to start the CEO" }, ...others] }), "", false, { agent: "claude", blocked: "Install Claude Code to start the CEO" }],
    ["a page that recommends none and remembers none", setup({ agents: others }), "", false, { agent: "", blocked: "This board offers no agent to start the CEO as." }],
    ["the recommended agent wherever it stands in the list", setup({ agents: [...others, claude] }), "", false, { agent: "claude", blocked: "" }],
  ];
  for (const [name, given, picked, lookedAtFolder, want] of cases) assert.deepEqual(startState(given, picked, lookedAtFolder), want, name);
});

test("the board's root keeps configured homes out of quick start", () => {
  const cases: [string, Parameters<typeof showsFirstRun>[0], boolean][] = [
    ["no CEO", { ceoRuns: false, ceoClosed: false, ceoConfigured: false, choice: "" }, true],
    ["a CEO runs", { ceoRuns: true, ceoClosed: false, ceoConfigured: false, choice: "" }, false],
    ["just started, before the board sees it", { ceoRuns: false, ceoClosed: false, ceoConfigured: false, choice: "started" }, false],
    ["the board without a CEO, by his choice", { ceoRuns: false, ceoClosed: false, ceoConfigured: false, choice: "board" }, false],
    ["the board's Start the CEO, even after a start the board never saw run", { ceoRuns: false, ceoClosed: false, ceoConfigured: false, choice: "" }, true],
    ["a CEO that was closed: the home has had one, so the board stays", { ceoRuns: false, ceoClosed: true, ceoConfigured: false, choice: "" }, false],
    ["a configured home before its CEO registers", { ceoRuns: false, ceoClosed: false, ceoConfigured: true, choice: "" }, false],
    ["a configured home whose CEO start failed", { ceoRuns: false, ceoClosed: false, ceoConfigured: true, choice: "started" }, false],
    ["an active setup before the choice is saved", { ceoRuns: false, ceoClosed: false, ceoConfigured: false, choice: "setup" }, true],
    ["an active setup while the saved choice has not settled", { ceoRuns: false, ceoClosed: false, ceoConfigured: true, choice: "setup" }, true],
    ["an active setup while its launch has not settled", { ceoRuns: true, ceoClosed: false, ceoConfigured: true, choice: "setup" }, true],
  ];
  for (const [name, given, want] of cases) assert.equal(showsFirstRun(given), want, name);
});
