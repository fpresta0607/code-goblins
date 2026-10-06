import type { LocalChecks } from "./types";

// LocalChecksLook is how a task's newest cfo gate test run shows beside its
// pull request: the words, a tip with the detail, a tone, and the page a click
// opens, the end of the run's log.
export interface LocalChecksLook { text: string; tip: string; tone: "failed" | "passed"; url: string }

// duration is a span of seconds as the board's clocks write one: seconds
// under a minute, whole minutes from there.
function duration(seconds: number): string {
  return seconds < 60 ? Math.round(seconds) + "s" : Math.round(seconds / 60) + "m";
}

// localChecksLook says how the newest cfo gate test run of a task's change
// ended, at which level and in how long, and what failed.
export function localChecksLook(checks: LocalChecks, taskId: string): LocalChecksLook {
  const passed = checks.status === "passed";
  let took = checks.level + " level, " + duration(checks.duration_seconds);
  if (checks.queue_seconds > 0) took += ", " + duration(checks.queue_seconds) + " of it waiting for its turn";
  const failed = checks.failed.length > 0 ? "Failed: " + checks.failed.join(", ") + "; " : "";
  return {
    text: passed ? "Local tests passed" : "Local tests failed",
    tip: (passed ? "" : failed) + took,
    tone: passed ? "passed" : "failed",
    url: "/api/tasks/" + encodeURIComponent(taskId) + "/checks",
  };
}
