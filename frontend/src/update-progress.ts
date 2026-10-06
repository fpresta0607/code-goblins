import type { IconName } from "./Icon.tsx";
import type { Run, Snapshot } from "./types.ts";

// The four steps goblins update says as it begins each ("[2/4] Check the
// download"), in the card's own words.
const STEP_TITLES = (to: string) => ["Download Code Goblins " + to, "Check each file's SHA-256", "Restart the board on " + to, "Bring the CFO's contract and skills up to date"];
const STEP_LINE = /^\[(\d)\/4\] /;
// The line goblins update ends on: how it went, and why.
const RESULT_LINE = /^(Updated|Rolled back|Failed): (.*)$/;
const UPDATE_RELOAD_WINDOW_MS = 10 * 60 * 1000;

export type StepState = "done" | "now" | "todo" | "failed";
export interface UpdateProgress { steps: { title: string; state: StepState }[]; result: string }

// updateProgress reads an Update item's run from what it printed: each step
// it reached is done, the last one under way while it runs, or failed when it
// ended without updating; result is the line it ended on, or why it stopped
// when it ended without one.
export function updateProgress(run: Run, output: string): UpdateProgress {
  const lines = output.split(/\r?\n/).map((line) => line.trim());
  const reached = Math.max(0, ...lines.map((line) => Number(STEP_LINE.exec(line)?.[1] || 0)));
  const ended = lines.map((line) => RESULT_LINE.exec(line)).filter((match) => !!match).at(-1);
  const updated = run.state === "succeeded";
  const failed = run.state === "failed";
  const steps = STEP_TITLES(run.update?.to || "").map((title, index): { title: string; state: StepState } => {
    const n = index + 1;
    if (updated || n < reached) return { title, state: "done" };
    if (n === reached) return { title, state: failed ? "failed" : "now" };
    return { title, state: "todo" };
  });
  let result = ended ? ended[2] : "";
  if (failed && !ended) result = "It stopped before it finished" + (run.reason ? ": " + run.reason : "") + ".";
  return { steps, result: result && result[0].toUpperCase() + result.slice(1) };
}

// updateOutcome is an Update item's state in the words of an update: ready,
// updating, updated, rolled back, not updated, or replaced by a newer one.
export function updateOutcome(run: Run): { icon: IconName; label: string; trouble: boolean } {
  switch (run.state) {
    case "ready": return { icon: "sparkle", label: "Ready", trouble: false };
    case "running": return { icon: "refresh", label: "Updating", trouble: false };
    case "succeeded": return { icon: "check-double", label: "Updated", trouble: false };
    case "failed": return { icon: "warning", label: run.exit_code === 6 ? "Updated" : run.exit_code === 3 ? "Rolled back" : "Not updated", trouble: true };
    case "withdrawn": return { icon: "close", label: "Replaced", trouble: false };
    case "expired": return { icon: "close", label: "Expired", trouble: false };
    default: return { icon: "clock", label: "Waiting", trouble: false };
  }
}

export function updateSucceededRecently(snapshot: Snapshot | null, now: number): boolean {
  return !!snapshot?.runs.some((run) => {
    const elapsed = now - Date.parse(run.finished_at);
    return !!run.update && run.state === "succeeded" && elapsed >= 0 && elapsed <= UPDATE_RELOAD_WINDOW_MS;
  });
}

export interface ReleaseBanner { kind: "update" | "source"; tag: string; installed: string; page: string; item: string }

// releaseBanner is the slim line under the board's header while a newer
// release waits: one that points to its Update item, or, on a board built
// from a clone, one that says the clone updates it. It says nothing in AFK
// mode, nothing once hidden for this version, and nothing for an update with
// no item to point to.
export function releaseBanner(snapshot: Snapshot, hidden: string): ReleaseBanner | null {
  const release = snapshot.release;
  if (!release || hidden === release.tag || snapshot.afk.state === "on") return null;
  const base = { tag: release.tag, installed: release.installed, page: release.page };
  if (release.source) return { kind: "source", ...base, item: "" };
  const run = (snapshot.runs || []).find((candidate) => candidate.update?.to === release.tag && (candidate.state === "ready" || candidate.state === "running"));
  return run ? { kind: "update", ...base, item: "run:" + run.id } : null;
}
