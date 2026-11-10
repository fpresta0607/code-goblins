import assert from "node:assert/strict";
import test from "node:test";
import { parseSnapshot } from "./types.ts";

test("Actions spend keeps the month's use, billed figures by repository and failed read details", () => {
  const reading = { status: "stale", month: "2026-10", total: { minutes: 700, used_usd: 12, billed_usd: 8.5 },
    repositories: [{ name: "fern", minutes: 700, used_usd: 12, billed_usd: 8.5 }], read_at: "2026-10-15T12:00:00Z", checked_at: "2026-10-15T13:00:00Z", detail: "Missing user scope." };
  assert.deepEqual(parseSnapshot({ healthy: true, actions_spend: reading }).actions_spend, reading);
});

test("a month GitHub fully covers keeps its use beside a zero bill", () => {
  const total = { minutes: 1200, used_usd: 9.6, billed_usd: 0 };
  assert.deepEqual(parseSnapshot({ healthy: true, actions_spend: { status: "available", total } }).actions_spend?.total, total);
});

test("an unavailable Actions reading never becomes a guessed zero", () => {
  for (const total of [null, undefined, "8.50", { minutes: 1, used_usd: 2 }, { minutes: 1, used_usd: "2", billed_usd: 0 }, { minutes: 1, used_usd: Infinity, billed_usd: 0 }]) {
    const reading = parseSnapshot({ healthy: true, actions_spend: { status: "unavailable", total } }).actions_spend;
    assert.equal(reading?.total, null);
  }
  assert.equal(parseSnapshot({ healthy: true }).actions_spend, undefined);
});

test("Actions spend keeps a measured net credit", () => {
  assert.equal(parseSnapshot({ healthy: true, actions_spend: { status: "available", total: { minutes: 0, used_usd: 0, billed_usd: -0.5 } } }).actions_spend?.total?.billed_usd, -0.5);
});

test("billing failures retain the selected account, credential and bounded recovery state", () => {
  const problem = { kind: "permission", action: "allow", account: "example-user", credential: "gh" };
  const recovery = { run_id: "billing-consent", state: "ready" };
  const reading = parseSnapshot({ healthy: true, actions_spend: { status: "unavailable", problem, recovery } }).actions_spend;
  assert.deepEqual(reading?.problem, problem);
  assert.deepEqual(reading?.recovery, recovery);
  assert.equal(reading?.total, null);
});
