import { test } from "node:test";
import assert from "node:assert/strict";
import { listItem, plainText, reportBody, summary, taskSummary, withoutHarness, teardownSentence, waitLine } from "./task-words.ts";
import { parseSnapshot, type Task } from "./types.ts";

const task = (fields: Record<string, unknown>): Task => parseSnapshot({ healthy: true, tasks: [{ id: "a", title: "a", phase: "working", generation: "s1", verified: false, ...fields }] }).tasks[0];
const lifecycle = (fields: Record<string, unknown>) => ({ phase: "paused", action: "pause", at: "2026-10-05T16:30:06Z", kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false, ...fields });

test("a title or a note loses the harness a backlog row names after a semicolon, and nothing else", () => {
  const cases: [string, string][] = [
    ["Paused goblins resume by themselves when the reason for the pause clears; Claude Code", "Paused goblins resume by themselves when the reason for the pause clears"],
    ["Codex goblins start inline; Codex", "Codex goblins start inline"],
    ["A pi goblin; pi", "A pi goblin"],
    ["Fix the Claude Code hook", "Fix the Claude Code hook"],
    ["Start the console; Claude Code reads it later", "Start the console; Claude Code reads it later"],
    ["next start at 5 GB free; Claude Code", "next start at 5 GB free"],
    ["", ""],
  ];
  for (const [title, shown] of cases) assert.equal(withoutHarness(title), shown, title);
});

test("a report loses the state word the status already says", () => {
  const cases: [string, string][] = [
    ["working: PRs 324 and 326 are green", "PRs 324 and 326 are green"],
    ["waiting on overlord: the mockups are on the page", "the mockups are on the page"],
    ["waiting on cg-goblins-quickstart: its layout lands first", "its layout lands first"],
    ["waiting on ci: PR 326's checks", "PR 326's checks"],
    ["blocked: Which port should I use?", "Which port should I use?"],
    ["failed: go test timed out", "go test timed out"],
    ["Waiting on the CFO: Which port?", "Which port?"],
    ["lifecycle-paused: Requested by the operator", "Requested by the operator"],
    ["done: PR https://github.com/o/r/pull/326", ""],
    ["Native activity and runtime evidence agree", "Native activity and runtime evidence agree"],
    ["", ""],
  ];
  for (const [report, body] of cases) assert.equal(reportBody(report), body, report);
});

test("plain text is sentence case with no semicolon chains, hashes, paths or links in its prose", () => {
  const cases: [string, string, string][] = [
    ["a semicolon chain becomes sentences", "the gate passed; the PR is open", "The gate passed. The PR is open."],
    ["a hash goes with the word that points at it", "PRs 324 and 326 are green 10 of 10 on main 13f9e0be (main has since moved to ffa7c0c5); the handoff is ready",
      "PRs 324 and 326 are green 10 of 10 on main (main has since moved). The handoff is ready."],
    ["a hash alone in brackets goes with its brackets", "merged (ce7db967) and installed", "Merged and installed."],
    ["a path becomes the name it ends with", "the board-display handoff is at state/tasktmp/cg-fleet-auto-resume/board-display-handoff.md, so this task can pause",
      "The board-display handoff is at board-display-handoff.md, so this task can pause."],
    ["a Windows path too", "log at C:\\Users\\fpres\\AppData\\Local\\cfo\\verify\\reports\\run-12.log.", "Log at run-12.log."],
    ["a pull request link reads as the pull request", "opened https://github.com/fpresta0607/code-goblins/pull/326 for review", "Opened PR #326 for review."],
    ["a run link reads as a CI run", "see https://github.com/o/r/actions/runs/37360134485/job/1", "See a CI run."],
    ["other links read as their site", "the page at https://lavish.example.ts.net/p/abc is up", "The page at lavish.example.ts.net is up."],
    ["a question keeps its sentence, not its choices", "Should the board wait for Codex? options: Wait (Recommended) | Ask now - PR 331 is merged", "Should the board wait for Codex?"],
    ["a date and a branch are not paths", "opens 10/5/2026 on fix/panel-clarity", "Opens 10/5/2026 on fix/panel-clarity."],
    ["a name that is not a plain word keeps its case", "cg-hardening retired with a handoff", "cg-hardening retired with a handoff."],
    ["finished punctuation is kept", "Which port should I use?", "Which port should I use?"],
    ["a hash inside a file name stays in the name", "wrote pd-ci-9f3c2a1e.log", "Wrote pd-ci-9f3c2a1e.log."],
    ["nothing stays nothing", "  ", ""],
  ];
  for (const [name, raw, plain] of cases) assert.equal(plainText(raw), plain, name);
});

test("a summary keeps whole sentences up to its length, and cuts a runaway first one at a word", () => {
  assert.equal(summary("One. Two. Three.", 9), "One. Two.");
  assert.equal(summary("One. Two. Three.", 100), "One. Two. Three.");
  assert.equal(summary("A first sentence that runs on and on.", 20), "A first sentence…");
  assert.equal(summary("", 20), "");
});

test("a list item is a short label with no path, hash or process id", () => {
  const cases: [string, string][] = [
    ["worktree C:\\dev\\code-goblins\\.worktrees\\gb-cg-fleet-auto-resume", "Worktree"],
    ["task session and branch", "Task session and branch"],
    ["gate commits 1a2b3c4d5e6f; validation restarts on Resume", "Gate commits"],
    ["Claude Code pid 18004", "Claude Code"],
    ["task brief", "Task brief"],
  ];
  for (const [item, label] of cases) assert.equal(listItem(item), label, item);
});

test("Windows teardown names the programs it still closes, never their process ids", () => {
  assert.equal(teardownSentence([]), "");
  assert.equal(teardownSentence(["uv.exe pid 20880"]), "Windows is still closing uv.exe.");
  assert.equal(teardownSentence(["uv.exe pid 1", "node.exe pid 2", "uv.exe pid 3"]), "Windows is still closing uv.exe and node.exe.");
});

test("a queued task's wait is one plain line: short on its card, naming the task it waits on in its panel", () => {
  const blocker = task({ id: "cg-goblins-quickstart", title: "An OpenClaw-style quick start; Claude Code" });
  const queued = (dependencies: string[], reason = "") => task({ id: "q", title: "q", phase: "queued", generation: "", dependencies, reason });
  const cases: [string, Task, string, string][] = [
    ["a task on the board", queued(["cg-goblins-quickstart"], "default first-open layout must land"), "Waits for a task", "Waits for an OpenClaw-style quick start"],
    ["more than one", queued(["cg-goblins-quickstart", "other"]), "Waits for 2 tasks", "Waits for an OpenClaw-style quick start and 1 more"],
    ["memory", queued(["memory"], "next start at 5 GB free; Claude Code"), "Waits for memory", "Waits for memory"],
    ["its turn", queued(["priority"], "cg-hardening retired 2026-09-28 03:11Z with a handoff"), "Waits for its turn", "Waits for its turn"],
    ["a set time", queued(["time"]), "Waits until later", "Waits until later"],
    ["a task no longer on the board", queued(["cg-hardening"]), "Waits for a task", "Waits for a task"],
    ["nothing", queued([]), "", ""],
  ];
  for (const [name, item, card, panel] of cases) {
    assert.equal(waitLine(item, [blocker, item]), card, name + " on its card");
    assert.equal(waitLine(item, [blocker, item], true), panel, name + " in its panel");
  }
});

test("the panel says each state once: a sentence of its own, and the raw words behind Details", () => {
  const raw = "working: PRs 324 and 326 are green on main 13f9e0be; ready to pause";
  const cases: [string, Task, { sentence: string; details: string[]; isFailure: boolean }][] = [
    ["working, tidied, with its report behind Details", task({ activity: raw }), { sentence: "PRs 324 and 326 are green on main. Ready to pause.", details: [raw], isFailure: false }],
    ["working, already plain", task({ activity: "working: Writing the tests." }), { sentence: "Writing the tests.", details: [], isFailure: false }],
    ["waiting, without its state word", task({ phase: "waiting", waiting_on: "ci", activity: "waiting on ci: PR 326's checks" }), { sentence: "PR 326's checks.", details: [], isFailure: false }],
    ["done, whose pull request is its badge", task({ phase: "done", activity: "done: PR https://github.com/o/r/pull/326" }), { sentence: "", details: [], isFailure: false }],
    ["failed by its own report", task({ phase: "failed", report: "failed", activity: "failed: go test timed out at 9f3c2a1e" }),
      { sentence: "Go test timed out.", details: ["failed: go test timed out at 9f3c2a1e"], isFailure: true }],
    ["paused, whose fresh note could not be saved", task({ phase: "paused", reason: "Paused by the Overlord; resumes on Resume", activity: "Paused by the Overlord; resumes on Resume",
      lifecycle: lifecycle({ handoff_saved: false, problems: ["Stopping-point deadline reached or request failed; no new handoff was saved"], pause: { reason: "overlord", at: "2026-10-05T16:30:06Z" } }) }),
      { sentence: "It stays paused until you resume it. The goblin's last saved notes are kept.", details: ["Stopping-point deadline reached or request failed; no new handoff was saved"], isFailure: false }],
    ["paused for memory", task({ phase: "paused", lifecycle: lifecycle({ pause: { reason: "memory", at: "2026-10-05T16:30:06Z" } }) }),
      { sentence: "It resumes by itself once 5 GB of memory is free.", details: [], isFailure: false }],
    ["paused before pauses had reasons", task({ phase: "paused", lifecycle: lifecycle({}) }), { sentence: "It stays paused until you resume it.", details: [], isFailure: false }],
    ["a pause that ran out of time", task({ activity: raw, lifecycle: lifecycle({ phase: "failed", handoff_saved: false, problems: ["Stopping-point deadline reached or request failed; no new handoff was saved", "context deadline exceeded"] }) }),
      { sentence: "The pause did not finish, so the goblin is not paused. Its work is kept. Try Pause again.", details: ["Stopping-point deadline reached or request failed; no new handoff was saved", "context deadline exceeded"], isFailure: true }],
    ["a resume that failed", task({ phase: "paused", lifecycle: lifecycle({ phase: "failed", action: "resume", problems: ["host did not start"] }) }),
      { sentence: "The goblin did not start again. Its work is kept. Try Resume again.", details: ["host did not start"], isFailure: true }],
    ["a stop that failed", task({ lifecycle: lifecycle({ phase: "failed", action: "stop", problems: ["access denied"] }) }),
      { sentence: "The stop did not finish. Its work is kept. Try Stop again.", details: ["access denied"], isFailure: true }],
    ["a stop under way", task({ phase: "stopping", activity: "Requested by the operator", lifecycle: lifecycle({ phase: "stopping", action: "stop" }) }), { sentence: "", details: [], isFailure: false }],
    ["still closing programs", task({ teardown: ["uv.exe pid 20880"] }), { sentence: "Windows is still closing uv.exe.", details: ["uv.exe pid 20880"], isFailure: false }],
  ];
  for (const [name, item, said] of cases) assert.deepEqual(taskSummary(item, [item]), said, name);
});

test("a paused goblin says what resumes it, in words, from its pause's condition", () => {
  const blocker = task({ id: "cg-board-kill", title: "Pause, Resume and Stop on the board; Claude Code" });
  const paused = (pause: Record<string, unknown>) => task({ phase: "paused", lifecycle: lifecycle({ pause: { at: "2026-10-05T16:30:06Z", ...pause } }) });
  const cases: [string, Record<string, unknown>, RegExp][] = [
    ["allowance", { reason: "allowance", until: "2026-10-09T22:27:00Z" }, /^It resumes by itself when the allowance resets, .+\.$/],
    ["a question", { reason: "question", until: "q-7" }, /^It resumes by itself when you answer its question\.$/],
    ["a task", { reason: "dependency", until: "task:cg-board-kill" }, /^It resumes by itself when Pause, Resume and Stop on the board finishes\.$/],
    ["a task gone from the board", { reason: "dependency", until: "task:cg-old" }, /^It resumes by itself when the task it waits on finishes\.$/],
    ["a pull request", { reason: "dependency", until: "pr:https://github.com/o/r/pull/12" }, /^It resumes by itself when PR #12 merges\.$/],
    ["a date", { reason: "dependency", until: "date:2026-10-10T09:00:00Z" }, /^It resumes by itself on .+\.$/],
    ["CI", { reason: "ci", until: "pr:https://github.com/o/r/pull/12@" + "a".repeat(40) }, /^It resumes by itself when its CI run finishes\.$/],
    ["a deploy", { reason: "deploy", until: "run:https://github.com/o/r/actions/runs/9@" + "a".repeat(40) }, /^It resumes by itself when its deploy finishes\.$/],
  ];
  for (const [name, pause, said] of cases) assert.match(taskSummary(paused(pause), [blocker]).sentence, said, name);
});
