import assert from "node:assert/strict";
import { test } from "node:test";
import { parseSnapshot } from "./types.ts";
import { subscriptionState } from "./subscription-usage.ts";

const NOW = Date.parse("2026-10-03T13:07:00Z");
const reading = (changes: Record<string, unknown> = {}) => parseSnapshot({ healthy: true, subscriptions: [{ provider: "codex", status: "available", percent_remaining: 76, source: "oauth", read_at: "2026-10-03T13:06:00Z", resets_at: "2026-10-09T22:27:42Z", floor_percent: 5, ...changes }] }).subscriptions![0];

test("the weekly percentage preserves zero and full allowance, marks exactly five percent as reserve and ten as near it", () => {
  for (const [remaining, isReserve, isNearReserve] of [[0, true, true], [5, true, true], [5.1, false, true], [10, false, true], [10.1, false, false], [76, false, false], [100, false, false]] as const) {
    const state = subscriptionState(reading({ percent_remaining: remaining }), NOW);
    assert.equal(state.remaining, remaining);
    assert.equal(state.isReserve, isReserve);
    assert.equal(state.isNearReserve, isNearReserve);
    assert.equal(state.text, remaining + "%");
    assert.match(state.label, /OpenAI.*weekly remaining/);
  }
});

// The Overlord, 2026-10-09: "keep using claude until at 0". The reserve the
// dial names is the floor the home keeps for the provider, and a floor of 0
// keeps none, while the ring still warns in the last five points.
test("the reserve is the home's floor for the provider, and a floor of 0 keeps none", () => {
  for (const [floor, remaining, isReserve, isNearReserve, words] of [
    [0, 1, false, true, "No reserve, runs to 0%"],
    [0, 0, false, true, "No reserve, runs to 0%"],
    [0, 5.1, false, false, "No reserve, runs to 0%"],
    [10, 8, true, true, "10% reserve reached"],
    [10, 15, false, true, "10% reserve"],
    [10, 15.1, false, false, "10% reserve"],
    [2.5, 2.5, true, true, "2.5% reserve reached"],
  ] as const) {
    const state = subscriptionState(reading({ floor_percent: floor, percent_remaining: remaining }), NOW);
    assert.equal(state.isReserve, isReserve, `floor ${floor} at ${remaining}%`);
    assert.equal(state.isNearReserve, isNearReserve, `floor ${floor} at ${remaining}%`);
    assert.ok(state.details.endsWith(` ${words}.`), state.details);
  }
  const unread = subscriptionState(reading({ floor_percent: 0, status: "stale" }), NOW);
  assert.ok(unread.details.endsWith(". Reading stale. No reserve, runs to 0%."), unread.details);
});

test("a floor the supervisor could not read leaves the reserve unknown and unmarked", () => {
  for (const floor of [null, undefined, -1, 100, "5", NaN]) {
    const usage = reading({ floor_percent: floor, percent_remaining: 1 });
    assert.equal(usage.floor_percent, null);
    const state = subscriptionState(usage, NOW);
    assert.equal(state.isReserve, false);
    assert.equal(state.isNearReserve, false);
    assert.ok(state.details.endsWith(" Reserve unknown."), state.details);
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
    assert.equal(state.isNearReserve, false);
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

test("explicitly unknown reading times stay unknown without losing a known reset", () => {
  for (const readAt of [null, "not-a-date", "0001-01-01T00:00:00Z"]) {
    const state = subscriptionState(reading({ percent_remaining: 75, read_at: readAt }), NOW);
    assert.equal(state.remaining, null);
    assert.equal(state.text, "?");
    assert.match(state.details, /Reading time unknown/);
    assert.doesNotMatch(state.details, /just now|75%/);
    const reset = new Date("2026-10-09T22:27:42Z").toLocaleString(undefined, { year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit", timeZoneName: "short" });
    assert.ok(state.details.includes(reset));
  }
});
