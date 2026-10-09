import { test } from "node:test";
import assert from "node:assert/strict";
import { goblinName, listItem, pausedWithParent, pauseStatus, plainText, reportBody, summary, taskName, taskSummary, withoutHarness, teardownSentence, type Summary } from "./task-words.ts";
import { parseSnapshot, type CIDuration, type PauseCondition, type Task } from "./types.ts";

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

test("a named goblin is called by its name and title, and its task keeps its own words", () => {
  const named = task({ id: "cg-x", title: "Show goblin names; Claude Code", goblin_name: "Jerry", goblin_title: "Code Designer" });
  const unnamed = task({ id: "cg-y", title: "Refund totals; Codex" });
  const untitled = task({ id: "cg-z", title: "" });
  assert.equal(goblinName(named), "Jerry - Code Designer");
  assert.equal(taskName(named), "Show goblin names");
  assert.equal(goblinName(unnamed), "Refund totals");
  assert.equal(goblinName(untitled), "cg-z");
  assert.equal(taskName(untitled), "cg-z");
});

test("a paused card names the goblin it waits on or resumes with by its goblin name", () => {
  const otis = task({ id: "b", title: "B work", goblin_name: "Otis", goblin_title: "Bug Hunter" });
  const dependency: PauseCondition = { reason: "dependency", until: "task:b", at: "2026-10-05T16:30:06Z" };
  const overlord: PauseCondition = { reason: "overlord", until: "", at: "2026-10-05T16:30:06Z" };
  assert.equal(pauseStatus(dependency, [otis], []), "Waiting on Otis - Bug Hunter");
  assert.equal(pauseStatus(overlord, [otis], [], otis), "Resumes with Otis - Bug Hunter");
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

test("a queued task adds no line about what it waits for, whatever it waits on", () => {
  // The Overlord, 2026-10-05: "waits for a task doesnt make sens dont ened
  // any addiioantional text".
  const blocker = task({ id: "cg-goblins-quickstart", title: "An OpenClaw-style quick start; Claude Code" });
  const queued = (waits: Record<string, unknown>[], reason = "") => task({ id: "q", title: "q", phase: "queued", generation: "", waits, reason });
  for (const item of [queued([{ kind: "task", target: "cg-goblins-quickstart" }], "default first-open layout must land"), queued([{ kind: "memory", target: "memory 12 GB", bytes: 12 * 2 ** 30 }], "next start at 12 GB free; Claude Code"), queued([{ kind: "task", target: "priority", problem: "No task is named \"priority\"" }]), queued([])]) {
    assert.deepEqual(taskSummary(item, [blocker, item]), { sentence: "", details: [], isFailure: false }, item.waits.map((wait) => wait.target).join(",") || "nothing");
  }
});

test("the panel says each state once: a sentence of its own, and the raw words behind Details", () => {
  const raw = "working: PRs 324 and 326 are green on main 13f9e0be; ready to pause";
  const cases: [string, Task, Summary][] = [
    ["working, tidied, with its report behind Details", task({ activity: raw }), { sentence: "PRs 324 and 326 are green on main. Ready to pause.", details: [raw], isFailure: false }],
    ["working, already plain", task({ activity: "working: Writing the tests." }), { sentence: "Writing the tests.", details: [], isFailure: false }],
    ["waiting, without its state word", task({ phase: "waiting", waiting_on: "ci", activity: "waiting on ci: PR 326's checks" }), { sentence: "PR 326's checks.", details: [], isFailure: false }],
    ["done, whose pull request is its badge", task({ phase: "done", activity: "done: PR https://github.com/o/r/pull/326" }), { sentence: "", details: [], isFailure: false }],
    ["failed by its own report", task({ phase: "failed", report: "failed", activity: "failed: go test timed out at 9f3c2a1e" }),
      { sentence: "", details: ["Go test timed out.", "failed: go test timed out at 9f3c2a1e"], isFailure: true }],
    ["paused, whose fresh note could not be saved", task({ phase: "paused", reason: "Paused by the Overlord; resumes on Resume", activity: "Paused by the Overlord; resumes on Resume", last_report: raw,
      lifecycle: lifecycle({ handoff_saved: false, problems: ["Stopping-point deadline reached or request failed; no new handoff was saved"], pause: { reason: "overlord", at: "2026-10-05T16:30:06Z" } }) }),
      { sentence: "", details: ["PRs 324 and 326 are green on main. Ready to pause. It stays paused until you resume it."], isFailure: false, isPlain: true }],
    ["paused for memory before its first report", task({ phase: "paused", lifecycle: lifecycle({ pause: { reason: "memory", at: "2026-10-05T16:30:06Z" } }) }),
      { sentence: "", details: ["It resumes by itself once 5 GB of memory is free."], isFailure: false, isPlain: true }],
    ["paused before pauses had reasons", task({ phase: "paused", lifecycle: lifecycle({}) }), { sentence: "", details: ["It stays paused until you resume it."], isFailure: false, isPlain: true }],
    ["paused while Windows still closes its programs", task({ phase: "paused", teardown: ["uv.exe pid 20880"], lifecycle: lifecycle({ pause: { reason: "memory", at: "2026-10-05T16:30:06Z" } }) }),
      { sentence: "", details: ["It resumes by itself once 5 GB of memory is free. Windows is still closing uv.exe."], isFailure: false, isPlain: true }],
    ["a pause that ran out of time", task({ activity: raw, lifecycle: lifecycle({ phase: "failed", handoff_saved: false, problems: ["Stopping-point deadline reached or request failed; no new handoff was saved", "context deadline exceeded"] }) }),
      { sentence: "", details: ["The pause did not finish, so the goblin is not paused. Its work is kept. Try Pause again.", "Stopping-point deadline reached or request failed; no new handoff was saved", "context deadline exceeded"], isFailure: false }],
    ["a resume that failed", task({ phase: "paused", lifecycle: lifecycle({ phase: "failed", action: "resume", problems: ["host did not start"] }) }),
      { sentence: "", details: ["The goblin did not start again. Its work is kept. Try Resume again.", "host did not start"], isFailure: true }],
    ["a stop that failed", task({ lifecycle: lifecycle({ phase: "failed", action: "stop", problems: ["access denied"] }) }),
      { sentence: "", details: ["The stop did not finish. Its work is kept. Try Stop again.", "access denied"], isFailure: false }],
    ["a stop under way", task({ phase: "stopping", activity: "Requested by the operator", lifecycle: lifecycle({ phase: "stopping", action: "stop" }) }), { sentence: "", details: [], isFailure: false }],
    ["still closing programs", task({ teardown: ["uv.exe pid 20880"] }), { sentence: "Windows is still closing uv.exe.", details: ["uv.exe pid 20880"], isFailure: false }],
  ];
  for (const [name, item, said] of cases) assert.deepEqual(taskSummary(item, [item]), said, name);
});

test("Working, a pause that did not finish and a failure have no line under them; what it would say is the first thing behind Details", () => {
  // The Overlord, 2026-10-05: "dont need text under working", "dont need
  // text under pause fialed", and 2026-10-08: "everything error wise goes to
  // cfo". The other statuses keep their line.
  const raw = "working: PRs 324 and 326 are green on main 13f9e0be; ready to pause";
  const failed = task({ activity: raw, lifecycle: lifecycle({ phase: "failed", problems: ["context deadline exceeded"] }) });
  const cases: [string, Task, string, { sentence: string; details: string[]; isFailure: boolean }][] = [
    ["working", task({ activity: raw }), "Working", { sentence: "", details: ["PRs 324 and 326 are green on main. Ready to pause.", raw], isFailure: false }],
    ["working, already plain", task({ activity: "working: Writing the tests." }), "Working", { sentence: "", details: ["Writing the tests."], isFailure: false }],
    ["working with nothing to say", task({ activity: "" }), "Working", { sentence: "", details: [], isFailure: false }],
    ["a pause that did not finish", failed, "Working", { sentence: "", details: ["The pause did not finish, so the goblin is not paused. Its work is kept. Try Pause again.", "context deadline exceeded"], isFailure: false }],
    ["a question to him keeps its line", task({ activity: raw }), "Waiting on the CFO", { sentence: "PRs 324 and 326 are green on main. Ready to pause.", details: [raw], isFailure: false }],
    ["a failure", task({ phase: "failed", report: "failed", activity: "failed: go test timed out" }), "Failed", { sentence: "", details: ["Go test timed out."], isFailure: true }],
  ];
  for (const [name, item, status, said] of cases) assert.deepEqual(taskSummary(item, [item], status), said, name);
});

test("a paused goblin's panel adds no line under its status, and its Details says what it did last and what it waits for", () => {
  // The Overlord, 2026-10-09, on Bernie's panel: "in paused goblin panels
  // the highlight line, it's not needed to be presented". Later that day, on
  // a panel whose Details read what its pause could not do: "every time I
  // look at the details it is the same text ... that line that says it
  // resumes by itself, that should be part of the details. It should just be
  // a well written, proper case, human readable little description about
  // what that agent did or is doing."
  // Arrange: a goblin whose pause missed its handoff and its teardown, whose
  // latest status line is the pause's own, and who last reported its work.
  const blocker = task({ id: "cg-board-kill", title: "Pause, Resume and Stop on the board; Claude Code" });
  const did = "The red tests are written at paused-panel.spec.ts. The fixes come next.";
  const paused = (pause: Record<string, unknown>) => task({ phase: "paused",
    last_report: "working: the red tests are written at frontend/tests/paused-panel.spec.ts; the fixes come next",
    activity: "lifecycle-paused: memory; worktree C:\\dev\\code-goblins\\worktrees\\a; task session and branch", reason: "Paused for memory; resumes at 5 GB free",
    lifecycle: lifecycle({ handoff_saved: false, pause: { at: "2026-10-05T16:30:06Z", until: "", ...pause },
      problems: ["Stopping-point deadline reached or request failed; no new handoff was saved", "its terminal ended, but the rest of its stop did not finish: context deadline exceeded"] }) });
  const cases: [string, Record<string, unknown>, RegExp][] = [
    ["memory", { reason: "memory" }, /^It resumes by itself once 5 GB of memory is free\.$/],
    ["allowance", { reason: "allowance", until: "2026-10-09T22:27:00Z" }, /^It resumes by itself when the allowance resets, Oct \d+, \d+:27 [AP]M\.$/],
    ["the Overlord", { reason: "overlord" }, /^It stays paused until you resume it\.$/],
    ["a question", { reason: "question", until: "q-7" }, /^It resumes by itself when you answer its question\.$/],
    ["a task", { reason: "dependency", until: "task:cg-board-kill" }, /^It resumes by itself when Pause, Resume and Stop on the board finishes\.$/],
    ["a task gone from the board", { reason: "dependency", until: "task:cg-old" }, /^It resumes by itself when the task it waits on finishes\.$/],
    ["a pull request", { reason: "dependency", until: "pr:https://github.com/o/r/pull/12" }, /^It resumes by itself when PR #12 merges\.$/],
    ["a time", { reason: "dependency", until: "date:2026-10-10T09:00:00Z" }, /^It resumes by itself on Oct \d+, \d+:00 [AP]M\.$/],
    ["CI", { reason: "ci", until: "pr:https://github.com/o/r/pull/12@" + "a".repeat(40) }, /^It resumes by itself when its CI run finishes\.$/],
    ["a deploy", { reason: "deploy", until: "run:https://github.com/o/r/actions/runs/9@" + "a".repeat(40) }, /^It resumes by itself when its deploy finishes\.$/],
  ];

  for (const [name, pause, waits] of cases) {
    // Act
    const said = taskSummary(paused(pause), [blocker]);

    // Assert: one description, written for a person, of what the goblin did
    // and then what it waits for, with nothing of the pause's own record.
    assert.equal(said.sentence, "", name);
    assert.equal(said.isPlain, true, name);
    assert.equal(said.details.length, 1, name);
    assert.ok(said.details[0].startsWith(did + " "), name + ": " + said.details[0]);
    assert.match(said.details[0].slice(did.length + 1), waits, name);
    assert.doesNotMatch(said.details[0], /;|deadline|handoff|did not finish|lifecycle|[A-Za-z]:\\/, name);
  }
});

test("a goblin paused for its own pull request says what it did, and nothing of a pause the board does not show", () => {
  const pr = "https://github.com/o/r/pull/12";
  const paused = (report: string) => task({ phase: "paused", pr, last_report: report, lifecycle: lifecycle({ pause: { reason: "dependency", until: "pr:" + pr, at: "2026-10-05T16:30:06Z" } }) });
  assert.deepEqual(taskSummary(paused("done: PR " + pr), []), { sentence: "", details: [], isFailure: false, isPlain: true });
  assert.deepEqual(taskSummary(paused("working: the review notes are answered; waiting for the merge"), []),
    { sentence: "", details: ["The review notes are answered. Waiting for the merge."], isFailure: false, isPlain: true });
});

test("a paused card says why it waits and what resumes it in a few words, in place of Paused", () => {
  // Arrange
  const blocker = task({ id: "cg-board-kill", title: "Pause, Resume and Stop on the board; Claude Code" });
  const runs = (repository: string, kind: string, minutes: number[]): CIDuration[] => minutes.map((minute) => ({ repository, kind, seconds: minute * 60 }));
  const durations = [...runs("o/r", "ci", [15, 12, 13]), ...runs("o/r", "deploy", [10, 11]), ...runs("o/other", "deploy", [2])];
  const head = "@" + "a".repeat(40);
  const cases: [string, Record<string, unknown> | undefined, CIDuration[], string | RegExp][] = [
    ["memory", { reason: "memory" }, [], "Memory: resumes at 5 GB free"],
    ["allowance", { reason: "allowance", until: "2026-10-09T22:27:00Z" }, [], /^Allowance: resumes Oct \d+, \d+:27 [AP]M$/],
    ["the Overlord", { reason: "overlord" }, [], "Paused by you"],
    ["a question", { reason: "question", until: "q-7" }, [], "Waiting for your answer"],
    ["a task", { reason: "dependency", until: "task:cg-board-kill" }, [], "Waiting on Pause, Resume and Stop on the board"],
    ["a task gone from the board", { reason: "dependency", until: "task:cg-old" }, [], "Waiting on another task"],
    ["a pull request", { reason: "dependency", until: "pr:https://github.com/o/r/pull/12" }, [], "Waiting on PR #12 to merge"],
    ["a date", { reason: "dependency", until: "date:2026-10-10T09:00:00Z" }, [], /^Resumes Oct \d+, \d+:00 [AP]M$/],
    ["CI with the repository's runs", { reason: "ci", until: "pr:https://github.com/o/r/pull/12" + head }, durations, "Waiting on CI, usually 13 min"],
    ["CI with none measured", { reason: "ci", until: "run:https://github.com/o/r/actions/runs/9" + head }, [], "Waiting on CI"],
    ["a deploy with an even count of runs", { reason: "deploy", until: "run:https://github.com/o/r/actions/runs/9" + head }, durations, "Waiting on deploy, usually 11 min"],
    ["a deploy measured only elsewhere", { reason: "deploy", until: "run:https://github.com/o/new/actions/runs/9" + head }, durations, "Waiting on deploy"],
    ["a pause from before pauses had reasons", undefined, [], "Paused"],
  ];

  for (const [name, pause, measured, said] of cases) {
    // Act
    const status = pauseStatus(pause && { until: "", at: "2026-10-05T16:30:06Z", ...pause } as never, [blocker], measured);

    // Assert
    if (typeof said === "string") assert.equal(status, said, name);
    else assert.match(status, said, name);
  }
});

test("a helper paused with its parent says it resumes with its parent, in its status and behind Details", () => {
  const parent = task({ id: "g", title: "Sync the ledger" });
  for (const [withParent, card, details] of [
    [true, "Resumes with Sync the ledger", "It resumes by itself when Sync the ledger runs again."],
    [false, "Paused by you", "It stays paused until you resume it."],
  ] as [boolean, string, string][]) {
    const helper = task({ id: "g-h1", parent: "g", phase: "paused", lifecycle: lifecycle({ pause: { reason: "overlord", until: "", at: "2026-10-05T16:30:06Z" }, with_parent: withParent }) });
    const tasks = [parent, helper];
    assert.equal(pauseStatus(helper.lifecycle?.pause, tasks, [], pausedWithParent(helper, tasks)), card);
    assert.deepEqual(taskSummary(helper, tasks), { sentence: "", details: [details], isFailure: false, isPlain: true });
  }
});
