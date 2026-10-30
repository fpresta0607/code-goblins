import assert from "node:assert/strict";
import test from "node:test";
import { parseSnapshot } from "./types.ts";

test("Actions spend preserves measured dollars, repository totals and failed read details", () => {
  const reading = { status: "stale", month: "2026-10", total_usd: 8.5, repositories: [{ name: "example-user/fern", spent_usd: 8.5 }], read_at: "2026-10-15T12:00:00Z", checked_at: "2026-10-15T13:00:00Z", detail: "Missing user scope." };
  assert.deepEqual(parseSnapshot({ healthy: true, actions_spend: reading }).actions_spend, reading);
});

test("an unavailable Actions reading never becomes a guessed zero", () => {
  for (const total of [null, undefined, "8.50", Infinity]) {
    const reading = parseSnapshot({ healthy: true, actions_spend: { status: "unavailable", total_usd: total } }).actions_spend;
    assert.equal(reading?.total_usd, null);
  }
  assert.equal(parseSnapshot({ healthy: true }).actions_spend, undefined);
});

test("Actions spend keeps a measured net credit", () => {
  assert.equal(parseSnapshot({ healthy: true, actions_spend: { status: "available", total_usd: -0.5 } }).actions_spend?.total_usd, -0.5);
});
