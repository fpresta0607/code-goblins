import assert from "node:assert/strict";
import test from "node:test";
import { deploymentLook } from "./deployment.ts";
import type { Deployment } from "./types.ts";

const RUN = "https://github.com/o/r/actions/runs/71";
const deploy = (overrides: Partial<Deployment>): Deployment => ({ commit: "aaa111bbb222", state: "deploying", workflows: ["Deploy"], link: RUN, at: "2026-09-30T12:31:00Z", ...overrides });

test("each state of a deploy reads in plain words, names its workflows and commit, and opens its run", () => {
  for (const [state, text, tone] of [
    ["deploying", "Deploying", "pending"],
    ["deployed", "Deployed", "passed"],
    ["failed", "Deploy failed", "failed"],
    ["cancelled", "Deploy cancelled", "cancelled"],
  ] as const) {
    assert.deepEqual(deploymentLook(deploy({ state, workflows: ["Deploy API", "Deploy worker"] })), { text, tip: "Deploy API, Deploy worker at aaa111b", tone, url: RUN });
  }
});

test("a run's page off GitHub is not opened", () => {
  assert.equal(deploymentLook(deploy({ link: "javascript:alert(1)" })).url, "");
});
