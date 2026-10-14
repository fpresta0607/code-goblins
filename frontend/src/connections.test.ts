import test from "node:test";
import assert from "node:assert/strict";
import { connectionTrouble, parseConnections } from "./connections.ts";

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
