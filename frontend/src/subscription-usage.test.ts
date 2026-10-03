import assert from "node:assert/strict";
import { test } from "node:test";
import { parseSnapshot } from "./types.ts";
import { subscriptionState } from "./subscription-usage.ts";

const NOW = Date.parse("2026-10-03T13:07:00Z");
const reading = (changes: Record<string, unknown> = {}) => parseSnapshot({ healthy: true, subscriptions: [{ provider: "codex", status: "available", percent_remaining: 76, source: "oauth", read_at: "2026-10-03T13:06:00Z", resets_at: "2026-10-09T22:27:42Z", ...changes }] }).subscriptions![0];

test("the weekly percentage preserves zero and full allowance and marks exactly five percent as reserve", () => {
  for (const [remaining, isReserve] of [[0, true], [5, true], [5.1, false], [76, false], [100, false]] as const) {
    const state = subscriptionState(reading({ percent_remaining: remaining }), NOW);
    assert.equal(state.remaining, remaining);
    assert.equal(state.isReserve, isReserve);
    assert.equal(state.text, remaining + "%");
    assert.match(state.label, /OpenAI.*weekly remaining/);
  }
});

test("missing and invalid percentages remain unknown while the active provider stays present", () => {
  for (const value of [null, undefined, -1, 101, "76", NaN, Infinity]) {
    const usage = reading({ percent_remaining: value });
    assert.equal(usage.provider, "codex");
    const state = subscriptionState(usage, NOW);
    assert.equal(state.remaining, null);
    assert.equal(state.text, "?");
  }
});

test("stale, failed and sign-in-required readings never show a percentage", () => {
  for (const status of ["stale", "unavailable", "auth_required", "provider-secret-error"]) {
    const state = subscriptionState(reading({ status }), NOW);
    assert.equal(state.remaining, null);
    assert.equal(state.isReserve, false);
    assert.doesNotMatch(state.details, /provider-secret-error/);
  }
  assert.match(subscriptionState(reading({ status: "auth_required" }), NOW).details, /sign-in required/);
});

test("cached readings age and expired windows turn unknown even before another snapshot", () => {
  for (const changes of [{ read_at: "2026-10-03T11:00:00Z" }, { resets_at: "2026-10-03T12:00:00Z" }, { read_at: "invalid" }, { read_at: "2026-10-03T14:00:00Z" }]) {
    assert.equal(subscriptionState(reading(changes), NOW).remaining, null);
  }
  const state = subscriptionState(reading(), NOW);
  assert.match(state.details, /1 min ago/);
  const reset = new Date("2026-10-09T22:27:42Z").toLocaleString(undefined, { year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit", timeZoneName: "short" });
  assert.ok(state.details.includes(reset));
  assert.match(state.details, /5% reserve/);
  assert.doesNotMatch(state.details, /quota-axi|OAuth/);
});

test("the snapshot exposes only the supported provider and safe usage fields", () => {
  assert.deepEqual(parseSnapshot({ healthy: true, subscriptions: [] }).subscriptions, []);
  assert.deepEqual(parseSnapshot({ healthy: true, subscriptions: [{ provider: "foreign" }] }).subscriptions, []);
  const usage = reading({ token: "secret", balance: 999, error: "secret", source: "provider-secret" });
  assert.equal(JSON.stringify(usage).includes("secret"), false);
  assert.equal(JSON.stringify(usage).includes("999"), false);
});

test("a non-subscription quota source cannot supply a weekly percentage", () => {
  for (const source of ["api", "", "unavailable"]) {
    assert.equal(subscriptionState(reading({ source }), NOW).remaining, null);
  }
});
