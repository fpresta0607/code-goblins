import { test } from "node:test";
import assert from "node:assert/strict";
import type { KeySpend, SubscriptionUsage } from "./types.ts";
import { formatAge, formatCount, formatUSD, usageReadings } from "./session-usage.ts";

const usage = {
  input_tokens: 1_400_000, output_tokens: 289_000, cache_read_tokens: 208_000_000,
  cost_usd: 3.209, context_tokens: 593_000, context_window: 1_000_000, model: "claude-opus-5-5", effort: "",
};
const week = (provider: "claude" | "codex", percentRemaining: number | null, status: SubscriptionUsage["status"] = "available"): SubscriptionUsage =>
  ({ provider, status, percent_remaining: percentRemaining, read_at: "", resets_at: "", source: "oauth", floor_percent: 5 });
const key = (spent: number | null, limit: number | null, status: KeySpend["status"] = "available"): KeySpend =>
  ({ provider: "openrouter", status, spent_usd: spent, limit_usd: limit, remaining_usd: null, period: "monthly", resets_at: "", read_at: "", byok_usd: null, includes_byok: false, blocking: null, profile: "default", detail: "" });

test("a session's context reads as its share of the model's window, with the tokens behind it", () => {
  // Act
  const readings = usageReadings("claude", usage);

  // Assert
  assert.deepEqual(readings, [{ key: "context", label: "Context", percent: 59.3, detail: "593k of 1.0M tokens" }]);
});

for (const [name, contextTokens, contextWindow] of [["no window", 24_000, 0], ["no tokens yet", 0, 1_000_000]] as const) {
  test(`a context with ${name} draws no reading rather than an empty one`, () => {
    // Act
    const readings = usageReadings("claude", { ...usage, context_tokens: contextTokens, context_window: contextWindow });

    // Assert
    assert.deepEqual(readings, []);
  });
}

test("a context past its window reads full, never past it", () => {
  assert.equal(usageReadings("pi", { ...usage, context_tokens: 1_200_000 })[0].percent, 100);
});

for (const harness of ["claude", "codex"] as const) {
  test(`a ${harness} session reads the week its own subscription used`, () => {
    // Arrange
    const subscriptions = [week("claude", 12), week("codex", 70)];

    // Act
    const readings = usageReadings(harness, undefined, subscriptions);

    // Assert
    assert.deepEqual(readings, [{ key: "week", label: "Week", percent: harness === "claude" ? 88 : 30, detail: harness === "claude" ? "12% left" : "70% left" }]);
  });
}

for (const [name, subscription] of [["unread", week("claude", null)], ["stale", week("claude", 40, "stale")], ["signed out", week("claude", 40, "auth_required")]] as const) {
  test(`a subscription that is ${name} draws no week reading`, () => {
    assert.deepEqual(usageReadings("claude", usage, [subscription]).map((reading) => reading.key), ["context"]);
  });
}

test("a pi session reads its key's spend against the key's limit", () => {
  // Act
  const readings = usageReadings("pi", undefined, [week("claude", 12)], [key(4, 20)]);

  // Assert
  assert.deepEqual(readings, [{ key: "spend", label: "Key spend", percent: 20, detail: "$4.000 of $20.000" }]);
});

for (const [name, spend] of [["no limit", key(4, null)], ["unread spend", key(null, 20)], ["an unavailable key", key(4, 20, "unavailable")]] as const) {
  test(`a pi key with ${name} draws no spend reading`, () => {
    assert.deepEqual(usageReadings("pi", undefined, [], [spend]), []);
  });
}

test("a session that is not working carries the time of the last thing it wrote", () => {
  // Arrange
  const now = Date.parse("2026-10-26T12:00:00Z");

  // Act
  const age = formatAge("2026-10-26T09:00:00Z", now);

  // Assert
  assert.equal(age, "3h ago");
});

test("the numbers are drawn as the harness draws them", () => {
  assert.deepEqual([formatCount(0), formatCount(999), formatCount(1400), formatCount(1_400_000), formatCount(208_000_000)], ["0", "999", "1.4k", "1.4M", "208M"]);
  assert.equal(formatUSD(3.209), "$3.209");
});
