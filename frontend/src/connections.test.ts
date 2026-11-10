import test from "node:test";
import assert from "node:assert/strict";
import { connectionEvidence, connectionTrouble, parseConnections } from "./connections.ts";

const entry = (status: string, detail = "") => parseConnections({ entries: [{ id: "service:repo", name: "repo", kind: "service", status, detail, grant: "revoke" }] }).entries[0];

test("a connector that works, waits or is not carried has no trouble to show", () => {
  for (const status of ["connected", "provided", "withheld", "skipped", "checking"]) assert.equal(connectionTrouble(entry(status, "anything")), "", status);
});

test("a failing connector names its trouble and its reason, for the faint mark's tooltip", () => {
  const cases: [string, string, string][] = [
    ["unauthorized", "Sign-in required.", "Sign in: Sign-in required."],
    ["missing", "", "Missing"],
    ["unreachable", "The host did not answer.", "Unreachable: The host did not answer."],
    ["mystery", "", "Unverified"],
  ];
  for (const [status, detail, want] of cases) assert.equal(connectionTrouble(entry(status, detail)), want, status);
});

test("each entry keeps what its card offers", () => {
  assert.equal(entry("connected").grant, "revoke");
  assert.equal(parseConnections({ entries: [{ id: "x", grant: "anything else" }] }).entries[0].grant, "");
});

test("a successful separate probe leaves session and account identity unknown", () => {
  const result = parseConnections({ generation: "first", runtime: { process_id: 42, started_at: "2026-11-07T00:00:00Z", workspace: "C:/work/first" }, entries: [{ id: "mcp:repo", status: "probe_succeeded", scope: "separate_probe", checked_at: "2026-11-07T01:00:00Z", restart_required: true, restart_reason: "Restart to load it." }] });
  assert.equal(result.generation, "first");
  assert.deepEqual(result.runtime, { processId: 42, startedAt: "2026-11-07T00:00:00Z", workspace: "C:/work/first" });
  const observed = result.entries[0];
  assert.equal(connectionEvidence(observed), "Separate probe succeeded. Live session unknown. Account and target identity unknown.");
  assert.equal(connectionTrouble(observed), "");
  assert.equal(observed.liveStatus, "unknown");
  assert.equal(observed.accountIdentity, "unknown");
  assert.equal(observed.targetIdentity, "unknown");
  assert.equal(observed.checkedAt, "2026-11-07T01:00:00Z");
  assert.equal(observed.isRestartRequired, true);
  assert.equal(observed.restartReason, "Restart to load it.");
});

test("missing provenance is visibly unknown and environment presence is scoped", () => {
  assert.equal(connectionEvidence(entry("connected")), "Observation scope unknown. Live session unknown. Account and target identity unknown.");
  const observed = parseConnections({ entries: [{ status: "provided", scope: "launch_environment" }] }).entries[0];
  assert.equal(connectionEvidence(observed), "Observed process environment. Live session unknown. Account and target identity unknown.");
});
