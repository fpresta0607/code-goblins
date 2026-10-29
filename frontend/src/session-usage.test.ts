import { test } from "node:test";
import assert from "node:assert/strict";
import { formatAge, formatCount, formatUSD, sessionStats, usageBanner } from "./session-usage.ts";

const usage = {
  input_tokens: 1_400_000, output_tokens: 289_000, cache_read_tokens: 208_000_000,
  cost_usd: 3.209, context_tokens: 593_000, context_window: 1_000_000, model: "openrouter/deepseek/deepseek-v4.1-flash", effort: "",
};

test("the context ring uses the session measurement and never invents a missing limit", () => {
  for (const [contextTokens, contextWindow, percent] of [[24000, 100000, 24], [0, 100000, 0], [120000, 100000, 100], [24000, 0, undefined]] as const) {
    const stats = sessionStats({ usage: { ...usage, context_tokens: contextTokens, context_window: contextWindow }, model: "", effort: "", memory: 0 });
    assert.equal(stats.contextPercent, percent);
    if (contextWindow > 0) assert.ok(stats.details.some((stat) => stat.key === "context"));
  }
  assert.equal(sessionStats({ model: "", effort: "", memory: 0 }).contextPercent, undefined);
});

test("the tooltip shows every stat the conversation records", () => {
  // Arrange
  const facts = { usage, model: "deepseek-v4.1-flash", effort: "high", memory: 208 * 2 ** 20 };

  // Act
  const stats = sessionStats(facts);

  // Assert
  const parts = stats.details.map((part) => part.text);
  assert.deepEqual(parts.slice(0, 5), ["1.4M", "289k", "208M", "$3.209", "593k of 1.0M"]);
  assert.equal(parts[5], "208M", "the memory its processes hold");
  assert.equal(parts[6], "Deepseek V4.1 Flash high", "the session record's model takes precedence over startup settings, by its short name");
  assert.equal(stats.details.find((part) => part.key === "engine")?.label, "Model / configured effort");
  assert.equal(stats.hasUsage, true);
  assert.deepEqual(stats.missing, []);
});

test("a stat the conversation does not record is left out and named", () => {
  // Arrange
  const incomplete = { ...usage, cost_usd: 0, context_window: 0 };

  // Act
  const stats = sessionStats({ usage: incomplete, model: "", effort: "", memory: 0 });

  // Assert
  assert.ok(!stats.details.some((part) => part.key === "cost"), "no invented cost");
  assert.ok(stats.missing.includes("its cost"));
  assert.ok(stats.missing.includes("its context window"));
  assert.ok(stats.missing.includes("its memory"));
  assert.equal(stats.details.find((part) => part.key === "engine")?.text, "Deepseek V4.1 Flash");
});

test("a session whose usage cannot be read says so and still shows what it can", () => {
  // Arrange

  // Act
  const stats = sessionStats({ model: "claude-opus-5-5", effort: "xhigh", memory: 4096 });

  // Assert
  assert.equal(stats.hasUsage, false);
  assert.ok(stats.missing.includes("its token usage and spend"));
  assert.deepEqual(stats.details.map((part) => part.text), ["4.0k", "Opus 5.5 xhigh"]);
});

test("a record that names only the session's model and effort has recorded no usage", () => {
  // Arrange: a record before its first reply, as pi's model and thinking
  // level changes or Codex's turn context leave it.
  const engineOnly = { input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cost_usd: 0, context_tokens: 0, context_window: 0, model: "openrouter/z-ai/glm-5.3-flash", effort: "high" };

  // Act
  const stats = sessionStats({ usage: engineOnly, model: "", effort: "", memory: 0 });

  // Assert
  assert.equal(stats.hasUsage, false);
  assert.ok(stats.missing.includes("its token usage and spend"));
  assert.ok(!stats.missing.includes("its cost"));
  assert.equal(stats.contextPercent, undefined);
  assert.equal(stats.details.find((part) => part.key === "engine")?.text, "GLM 5.3 Flash");
});

test("a session that is not working carries the time of the last thing it wrote", () => {
  // Arrange
  const now = Date.parse("2026-10-26T12:00:00Z");

  // Act
  const age = formatAge("2026-10-26T09:00:00Z", now);

  // Assert
  assert.equal(age, "3h ago");
});

test("a measured subscription draws the usage banner and its reset", () => {
  // Arrange
  const subscriptions = [{ provider: "claude" as const, status: "available" as const, percent_remaining: 12, read_at: "", resets_at: "2026-10-28T09:00:00Z", source: "oauth" as const, floor_percent: 5 }];

  // Act
  const reading = usageBanner("claude", subscriptions, []);

  // Assert
  assert.equal(reading.banner?.text, "Approaching weekly usage limit");
  assert.match(reading.banner?.detail ?? "", /^Resets /);
  assert.equal(reading.why, "");
});

for (const [harness, why] of [["claude", "Claude usage could not be read"], ["codex", "Codex usage could not be read"], ["pi", "OpenRouter key spend could not be read"]]) {
  test(`a ${harness} session whose provider cannot be read draws no banner and names the provider`, () => {
    // Arrange

    // Act
    const reading = usageBanner(harness, [], []);

    // Assert
    assert.equal(reading.banner, null);
    assert.equal(reading.why, why);
  });
}

test("the numbers are drawn as the harness draws them", () => {
  assert.deepEqual([formatCount(0), formatCount(999), formatCount(1400), formatCount(1_400_000), formatCount(208_000_000)], ["0", "999", "1.4k", "1.4M", "208M"]);
  assert.equal(formatUSD(3.209), "$3.209");
});

test("a shared API key budget is labelled as shared key spend", () => {
  const reading = usageBanner("pi", [], [{ provider: "openrouter", status: "available", spent_usd: 4, limit_usd: 20, remaining_usd: 16, period: "monthly", resets_at: "", read_at: "", byok_usd: null, includes_byok: false, blocking: null, profile: "default", detail: "" }]);
  assert.equal(reading.banner?.text, "Shared key spend");
  assert.equal(reading.banner?.detail, "$4.000 of $20.000");
});
