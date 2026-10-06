import assert from "node:assert/strict";
import test from "node:test";
import { hostedChecksLook } from "./hosted-checks.ts";
import type { HostedChecks } from "./types.ts";

const pr = "https://github.com/o/r/pull/209";
const checks = (overrides: Partial<HostedChecks>): HostedChecks => ({ state: "pending", checks: 9, failed: [], link: "", approved: false, ...overrides });

test("each state of a pull request's checks reads in plain words and opens its checks", () => {
  for (const [state, text, tip, tone] of [
    ["pending", "Checks running", "9 checks, still running", "pending"],
    ["passed", "Checks passed", "All 9 checks passed", "passed"],
  ] as const) {
    const look = hostedChecksLook(checks({ state }), pr);
    assert.deepEqual(look, { text, tip, tone, url: pr + "/checks" });
  }
});

test("a failure names the failed checks and opens the first one's own page", () => {
  const look = hostedChecksLook(checks({ state: "failed", failed: ["go (rest)", "frontend"], link: "https://github.com/o/r/actions/runs/5/job/50" }), pr);

  assert.deepEqual(look, { text: "Checks failed", tip: "Failed: go (rest), frontend", tone: "failed", url: "https://github.com/o/r/actions/runs/5/job/50" });
});

test("a cancelled run says so and names what was cancelled", () => {
  const look = hostedChecksLook(checks({ state: "cancelled", failed: ["test"], link: "https://github.com/o/r/actions/runs/5/job/50" }), pr);

  assert.deepEqual(look, { text: "Checks cancelled", tip: "Cancelled: test", tone: "cancelled", url: "https://github.com/o/r/actions/runs/5/job/50" });
});

test("a failed check's page off GitHub opens the pull request's checks instead, and nothing opens without a GitHub pull request", () => {
  const offGitHub = hostedChecksLook(checks({ state: "failed", failed: ["GitGuardian Security Checks"], link: "https://dashboard.gitguardian.com/workspace/1/incidents" }), pr);
  const noPullRequest = hostedChecksLook(checks({ state: "failed", failed: ["test"], link: "javascript:alert(1)" }), "javascript:alert(1)");

  assert.equal(offGitHub.url, pr + "/checks");
  assert.equal(noPullRequest.url, "");
});

test("one check is counted as one", () => {
  assert.equal(hostedChecksLook(checks({ state: "passed", checks: 1 }), pr).tip, "All 1 check passed");
});
