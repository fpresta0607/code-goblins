import assert from "node:assert/strict";
import test from "node:test";
import { localChecksLook } from "./local-checks.ts";
import type { LocalChecks } from "./types.ts";

const run = (overrides: Partial<LocalChecks>): LocalChecks => ({ commit: "3d7072f8aa", level: "affected", required_level: "affected", status: "passed", duration_seconds: 1080, queue_seconds: 300, failed: [], at: "2026-09-30T11:00:00Z", ...overrides });

test("a passed run says its level, how long it took and how long it waited", () => {
  assert.deepEqual(localChecksLook(run({}), "cg-wakes"), { text: "Local tests passed", tip: "affected level, 18m, 5m of it waiting for its turn", tone: "passed", url: "/api/tasks/cg-wakes/checks" });
});

test("a failed run names what failed first", () => {
  const look = localChecksLook(run({ status: "failed", failed: ["internal/b", "go vet"], queue_seconds: 0, duration_seconds: 42 }), "cg-wakes");

  assert.deepEqual(look, { text: "Local tests failed", tip: "Failed: internal/b, go vet; affected level, 42s", tone: "failed", url: "/api/tasks/cg-wakes/checks" });
});

test("the log's address carries the task's ID as one path segment", () => {
  assert.equal(localChecksLook(run({}), "a/b?c").url, "/api/tasks/a%2Fb%3Fc/checks");
});
