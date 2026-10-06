// The board as the Overlord saw it on 2026-10-05, in his screenshots of a
// goblin's Task panel and of the Tasks list: one task for each state the
// panel shows, with the words their goblins and the supervisor really wrote.
const GB = 2 ** 30;
const since = "2026-10-05T14:40:00Z";
const at = "2026-10-05T16:30:06Z";
const kept = ["worktree C:\\dev\\code-goblins\\.worktrees\\gb-cg-fleet-auto-resume", "task session and branch"];

const live = (id: string, title: string, fields: Record<string, unknown>) => ({
  id, title, project: "code-goblins", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", backend: "native",
  generation: "s-" + id, since, verified: false, dependencies: [], ...fields,
});

export const WORKING = live("cg-goblins-quickstart", "An OpenClaw-style quick start in the goblins command; Claude Code", {
  phase: "working", report: "working", pr: "https://github.com/fpresta0607/code-goblins/pull/334",
  activity: "working: 334's red run was not a broken merge: every red job except the aggregate was cancelled after 15 minutes without ever getting a runner; it is now on main 04188dad with build and vet passing and pushed once. 289 and 300 are next, waiting on their local Go runs.",
});

export const WAITING = live("cg-board-theme", "SIQstack colors and a clean browser tab for the board; Claude Code", {
  phase: "waiting", report: "waiting", waiting_on: "overlord",
  activity: "waiting on overlord: the three mockups are on the Scrawl page; reply build or say what to change",
});

export const PAUSED = live("cg-subscription-dials", "Memory and subscription dials in one header; Claude Code", {
  phase: "paused", at, reason: "Paused by the Overlord; resumes on Resume", activity: "Paused by the Overlord; resumes on Resume",
  lifecycle: { phase: "paused", action: "pause", at, kept: ["worktree C:\\dev\\code-goblins\\.worktrees\\gb-cg-subscription-dials", "task session and branch"], stopped: ["Claude Code pid 18004"], problems: ["Stopping-point deadline reached or request failed; no new handoff was saved"], handoff_saved: false, validation_restarts: false, pause: { reason: "overlord", at } },
});

// The goblin of his screenshot: the CFO asked it to pause, the pause ran out
// of time, and the goblin's own last report still says working.
export const PAUSE_FAILED = live("cg-fleet-auto-resume", "Paused goblins resume by themselves when the reason for the pause clears; Claude Code", {
  phase: "working", report: "working", pr: "https://github.com/fpresta0607/code-goblins/pull/326",
  activity: "working: PRs 324 and 326 are green 10 of 10 on main 13f9e0be (main has since moved to ffa7c0c5); the board-display handoff with five mockup prompts is at state/tasktmp/cg-fleet-auto-resume/board-display-handoff.md, so this task is ready to pause until 2026-10-09 22:27 UTC.",
  lifecycle: { phase: "failed", action: "pause", at, kept, stopped: [], problems: ["Stopping-point deadline reached or request failed; no new handoff was saved", "context deadline exceeded"], handoff_saved: false, validation_restarts: false },
});

export const FAILED = live("pd-ci-minutes", "PrecisionDocs-AI uses far fewer GitHub Actions minutes; Claude Code", {
  phase: "failed", report: "failed", project: "PrecisionDocs-AI",
  activity: "failed: go test ./internal/billing failed at 9f3c2a1e: TestMeteredUsage timed out after 10m0s; log at C:\\Users\\fpres\\AppData\\Local\\cfo\\verify\\reports\\report.log",
});

const queued = (id: string, title: string, fields: Record<string, unknown> = {}) => ({
  id, title, project: "code-goblins", phase: "queued", brief: true, verified: false, generation: "", queue_revision: "q-" + id, dependencies: [], since: "2026-10-01T15:00:00Z", ...fields,
});

export const QUEUED = [
  queued("cg-quick-tour", "A very quick tour for new users: how Code Goblins works and how to use the board, on first open; Claude Code", { dependencies: ["cg-goblins-quickstart"], reason: "default first-open layout must land and the generated mockup must be approved", since: "2026-10-03T10:00:00Z" }),
  queued("cg-board-update", "Updates arrive as their own special Overlord command in the Command Center, with one Update button (checksum-verified, safe swap and rollback, goblins untouched, the desktop window too); Claude Code", { since: "2026-10-02T11:00:00Z" }),
  queued("cg-hardening-followups", "The already-pushed skip design for tests-kept and the other open hardening items", { dependencies: ["priority"], reason: "cg-hardening retired 2026-09-28 03:11Z with a handoff", since: "2026-09-28T08:00:00Z" }),
];

export const TASKS = [WORKING, WAITING, PAUSED, PAUSE_FAILED, FAILED, ...QUEUED];

export const REVIEW = {
  id: "waiting-cg-board-theme-3584", identity: "a".repeat(64), task: "cg-board-theme", title: "Waiting on you: the three mockups are on the Scrawl page; reply build or say what to change",
  state: "open", created_at: since, updated_at: since,
};

export const MEMORY = { total: 32 * GB, commit_limit: 48 * GB, floor: 4 * GB, next: 5 * GB, available: 4.0 * GB, commit_available: 9 * GB, paged_pool: 4.7 * GB, nonpaged_pool: 0.4 * GB };

export function snapshot(revision = 1) {
  return { instance: "panel-states", revision, healthy: true, cfo_runs: true, attention: [], memory: MEMORY, tasks: TASKS, reviews: [REVIEW] };
}

export const events = (revision = 1) => `event: snapshot\ndata: ${JSON.stringify(snapshot(revision))}\n\n`;
