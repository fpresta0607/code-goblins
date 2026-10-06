import { test } from "node:test";
import assert from "node:assert/strict";
import { releaseBanner, updateOutcome, updateProgress, updateSucceededRecently } from "./update-progress.ts";
import { parseSnapshot, type Run } from "./types.ts";

const offer = { from: "v0.4.2", to: "v0.5.0", page: "https://github.com/fpresta0607/code-goblins/releases/tag/v0.5.0", published: "2026-10-06T14:02:00Z", notes: ["An update arrives in the Command Center"], signing: "unsigned", publisher: "", sum: "3f9a" };
const run = (changes: Partial<Run>): Run => ({ id: "update-v0.5.0-1", identity: "u".repeat(64), title: "Update Code Goblins from v0.4.2 to v0.5.0", shell: "powershell", admin: false, command: "goblins update --to v0.5.0", cwd: "C:\\home", state: "ready", exit_code: null, output: "", reason: "", created_at: "2026-10-06T14:05:00Z", expires_at: "", ran_at: "", finished_at: "", connection_task: "", connection_generation: "", credential_request: "", credential_names: [], task: "", interactive: false, update: offer, ...changes });

// What goblins update prints, as cmd/cfo/update_release.go prints it.
const printed = {
  downloading: "Code Goblins v0.4.2 runs here; v0.5.0 was published 2026-10-06 (page).\n[1/4] Download Code Goblins v0.5.0\n",
  installing: "[1/4] Download Code Goblins v0.5.0\n[2/4] Check the download\n      cfo.exe matches the release's SHA256SUMS: 3f9a\n[3/4] Install Code Goblins v0.5.0\nStopping the supervisor (pid 21116).\n",
  updated: "[1/4] Download Code Goblins v0.5.0\n[2/4] Check the download\n[3/4] Install Code Goblins v0.5.0\n[4/4] Bring the home up to date\nUpdated: Code Goblins v0.5.0 runs.\n",
  incomplete: "[1/4] Download Code Goblins v0.5.0\n[2/4] Check the download\n[3/4] Install Code Goblins v0.5.0\n[4/4] Bring the home up to date\nUpdated: Code Goblins v0.5.0 runs, but its install did not bring the home's contract, skills and hooks up to date (exit code 1); run goblins install to finish.\n",
  rolledBack: "[1/4] Download Code Goblins v0.5.0\n[2/4] Check the download\n[3/4] Install Code Goblins v0.5.0\ncfo update: the supervisor (pid 30412) did not serve within 1m30s; putting the previous build back\nRolled back: Code Goblins v0.4.2 serves again, and v0.5.0 was not installed; what it printed above says why.\n",
  refused: "[1/4] Download Code Goblins v0.5.0\nFailed: the downloaded cfo.exe does not match the release's SHA256SUMS (it is 11, the release lists 22), so nothing was installed.\n",
};

test("an update's steps follow what it printed: done, the one under way, and those still to come", () => {
  const cases: [string, Run, string, string[], string][] = [
    ["not started", run({}), "", ["todo", "todo", "todo", "todo"], ""],
    ["downloading", run({ state: "running" }), printed.downloading, ["now", "todo", "todo", "todo"], ""],
    ["installing", run({ state: "running" }), printed.installing, ["done", "done", "now", "todo"], ""],
    ["updated", run({ state: "succeeded", exit_code: 0 }), printed.updated, ["done", "done", "done", "done"], "Code Goblins v0.5.0 runs."],
    ["updated with an incomplete home refresh", run({ state: "failed", exit_code: 6 }), printed.incomplete, ["done", "done", "done", "failed"], "Code Goblins v0.5.0 runs, but its install did not bring the home's contract, skills and hooks up to date (exit code 1); run goblins install to finish."],
    ["rolled back", run({ state: "failed", exit_code: 3 }), printed.rolledBack, ["done", "done", "failed", "todo"], "Code Goblins v0.4.2 serves again, and v0.5.0 was not installed; what it printed above says why."],
    ["refused at the check", run({ state: "failed", exit_code: 1 }), printed.refused, ["failed", "todo", "todo", "todo"], "The downloaded cfo.exe does not match the release's SHA256SUMS (it is 11, the release lists 22), so nothing was installed."],
    ["a window closed before it finished", run({ state: "failed", exit_code: null, reason: "its window closed before the command finished" }), printed.installing, ["done", "done", "failed", "todo"], "It stopped before it finished: its window closed before the command finished."],
  ];
  for (const [name, given, output, states, result] of cases) {
    const progress = updateProgress(given, output);
    assert.deepEqual(progress.steps.map((step) => step.state), states, name);
    assert.equal(progress.result, result, name);
  }
  assert.deepEqual(updateProgress(run({}), "").steps.map((step) => step.title), ["Download Code Goblins v0.5.0", "Check each file's SHA-256", "Restart the board on v0.5.0", "Bring the CFO's contract and skills up to date"]);
});

test("an update's outcome reads as the update, not as a command's exit code", () => {
  const cases: [string, Run, string, boolean][] = [
    ["ready", run({}), "Ready", false],
    ["running", run({ state: "running" }), "Updating", false],
    ["updated", run({ state: "succeeded", exit_code: 0 }), "Updated", false],
    ["updated with an incomplete home refresh", run({ state: "failed", exit_code: 6 }), "Updated", true],
    ["rolled back", run({ state: "failed", exit_code: 3 }), "Rolled back", true],
    ["failed", run({ state: "failed", exit_code: 1 }), "Not updated", true],
    ["replaced by a newer release", run({ state: "withdrawn", reason: "v0.5.1 was published since" }), "Replaced", false],
  ];
  for (const [name, given, label, trouble] of cases) {
    const outcome = updateOutcome(given);
    assert.equal(outcome.label, label, name);
    assert.equal(outcome.trouble, trouble, name);
  }
});

test("a board update counts as recent only when its item succeeded in the last ten minutes", () => {
  const now = Date.parse("2026-10-06T14:20:00Z");
  const succeeded = run({ state: "succeeded", exit_code: 0, finished_at: "2026-10-06T14:19:00Z" });
  const snapshot = (runs: Run[]) => parseSnapshot({ instance: "i", healthy: true, tasks: [], runs });
  const cases: [string, Run[], boolean][] = [
    ["no update items", [], false],
    ["a recent success", [succeeded], true],
    ["finished just now", [{ ...succeeded, finished_at: "2026-10-06T14:20:00Z" }], true],
    ["finished exactly ten minutes ago", [{ ...succeeded, finished_at: "2026-10-06T14:10:00Z" }], true],
    ["finished more than ten minutes ago", [{ ...succeeded, finished_at: "2026-10-06T14:09:59.999Z" }], false],
    ["finished in the future", [{ ...succeeded, finished_at: "2026-10-06T14:20:00.001Z" }], false],
    ["no finish time", [{ ...succeeded, finished_at: "" }], false],
    ["an invalid finish time", [{ ...succeeded, finished_at: "invalid" }], false],
    ["a regular run succeeded", [{ ...succeeded, update: null }], false],
    ["an update still runs", [{ ...succeeded, state: "running" }], false],
    ["an update rolled back", [{ ...succeeded, state: "failed", exit_code: 3 }], false],
    ["the home refresh failed", [{ ...succeeded, state: "failed", exit_code: 6 }], false],
    ["a success among other items", [run({}), succeeded], true],
  ];
  for (const [name, runs, want] of cases) assert.equal(updateSucceededRecently(snapshot(runs), now), want, name);
  assert.equal(updateSucceededRecently(null, now), false);
});

test("the banner points to the update while one waits, and to the clone's steps for a board built from source", () => {
  const snapshot = (changes: Record<string, unknown>) => parseSnapshot({ instance: "i", healthy: true, tasks: [], ...changes });
  const available = { installed: "v0.4.2", tag: "v0.5.0", page: offer.page, published: offer.published, source: false };
  const cases: [string, ReturnType<typeof snapshot>, string, ReturnType<typeof releaseBanner>][] = [
    ["nothing published", snapshot({}), "", null],
    ["an update waits", snapshot({ release: available, runs: [run({})] }), "", { kind: "update", tag: "v0.5.0", installed: "v0.4.2", page: offer.page, item: "run:update-v0.5.0-1" }],
    ["an update runs", snapshot({ release: available, runs: [run({ state: "running" })] }), "", { kind: "update", tag: "v0.5.0", installed: "v0.4.2", page: offer.page, item: "run:update-v0.5.0-1" }],
    ["no item for it yet", snapshot({ release: available }), "", null],
    ["hidden until the next version", snapshot({ release: available, runs: [run({})] }), "v0.5.0", null],
    ["hidden for an older version", snapshot({ release: available, runs: [run({})] }), "v0.4.9", { kind: "update", tag: "v0.5.0", installed: "v0.4.2", page: offer.page, item: "run:update-v0.5.0-1" }],
    ["a board built from source", snapshot({ release: { ...available, installed: "main-8c10c45b", source: true } }), "", { kind: "source", tag: "v0.5.0", installed: "main-8c10c45b", page: offer.page, item: "" }],
    ["AFK mode", snapshot({ release: available, runs: [run({})], afk: { state: "on" } }), "", null],
  ];
  for (const [name, given, hidden, want] of cases) assert.deepEqual(releaseBanner(given, hidden), want, name);
});
