import assert from "node:assert/strict";
import { test } from "node:test";
import { parseSnapshot } from "./types.ts";
import { keySpendState, money } from "./key-spend.ts";

const NOW = Date.parse("2026-10-15T12:00:00Z");
const RESET = "2026-10-20T00:00:00Z";
// usd is a dollar figure as a person's own locale writes it, the way the
// dial writes one.
const usd = (value: number) => value.toLocaleString(undefined, { style: "currency", currency: "USD", minimumFractionDigits: Number.isInteger(value) ? 0 : 2, maximumFractionDigits: 2 });
const reading = (changes: Record<string, unknown> = {}) => parseSnapshot({ healthy: true, key_spend: [{
  provider: "openrouter", status: "available", spent_usd: 9.04, limit_usd: 50, remaining_usd: 40.96, period: "weekly",
  resets_at: RESET, read_at: "2026-10-15T12:00:00Z", byok_usd: 0, includes_byok: false, blocking: null, detail: "", ...changes,
}] }).key_spend![0];

// The Overlord's own row: $9.04 spent against a $50 week, dollars first and
// the limit and window beside them.
test("the dial says the dollars spent, then the limit and the window they count against", () => {
  const state = keySpendState(reading(), NOW);
  assert.equal(state.text, usd(9.04));
  assert.equal(state.budget, `/ ${usd(50)} this week`);
  assert.equal(state.label, `OpenRouter ${usd(9.04)} of ${usd(50)} this week`);
  assert.ok(state.details.startsWith(`Spent ${usd(9.04)} of ${usd(50)} this week.`), state.details);
  assert.match(state.details, /Resets /);
  assert.match(state.details, /Reading just now\./);
  assert.equal(state.percentRemaining, 81.92);
  assert.equal(state.isUsable, true);
});

// The window comes from the provider: a daily, monthly or non-resetting
// limit says so, and never the week a fresh install starts with.
test("each window the provider names is said in its own words", () => {
  for (const [period, words] of [["daily", "today"], ["weekly", "this week"], ["monthly", "this month"], ["lifetime", "in total"]] as const) {
    const state = keySpendState(reading({ period }), NOW);
    assert.equal(state.budget, `/ ${usd(50)} ${words}`, period);
    assert.match(state.label, new RegExp(words), period);
  }
});

// The ring states what is left of the limit to the hundredth the dials beside
// it are read to, and never a fraction of a limit nobody set.
test("the ring's fraction is what is left of the limit, and nothing without one", () => {
  for (const [remaining, limit, percent] of [[40.96, 50, 81.92], [1, 3, 33.33], [7.77, 10, 77.7], [0, 50, 0], [60, 50, 100]] as const) {
    assert.equal(keySpendState(reading({ remaining_usd: remaining, limit_usd: limit }), NOW).percentRemaining, percent, `${remaining} of ${limit}`);
  }
  assert.equal(keySpendState(reading({ remaining_usd: 5, limit_usd: null }), NOW).percentRemaining, null);
  assert.equal(keySpendState(reading({ remaining_usd: null }), NOW).percentRemaining, null);
  assert.equal(keySpendState(reading({ remaining_usd: 5, limit_usd: 0 }), NOW).percentRemaining, null);
});

// A key with no limit has no window to be a fraction of: the dial shows the
// dollars it spent and never a percentage, a ring fraction or a made-up
// budget.
test("a key with no limit shows what it spent and invents no percentage", () => {
  for (const period of ["", "weekly", "lifetime"]) {
    const state = keySpendState(reading({ period, limit_usd: null, remaining_usd: null, spent_usd: 25.5 }), NOW);
    assert.equal(state.text, usd(25.5));
    assert.equal(state.budget, "no limit");
    assert.equal(state.percentRemaining, null);
    assert.equal(state.label, `OpenRouter ${usd(25.5)} spent, no limit`);
    assert.match(state.details, new RegExp(`Spent ${usd(25.5).replace(/[$]/g, "\\$")} in total, no limit`));
  }
});

// A figure the provider left out is no zero: the dial shows that it does not
// know, and says why, while the limit it does know is still named.
test("a missing or unmeasurable figure is said, never shown as zero", () => {
  for (const spent of [null, -1, "9.04", NaN, Infinity]) {
    const state = keySpendState(reading({ spent_usd: spent }), NOW);
    assert.equal(state.isUsable, false, `${String(spent)}`);
    assert.equal(state.text, "?", `${String(spent)}`);
    assert.equal(state.percentRemaining, null, `${String(spent)}`);
    assert.match(state.label, /^OpenRouter spend /);
  }
  const uncounted = keySpendState(reading({ period: "", spent_usd: null, detail: "the key's limit resets over a window this board cannot count" }), NOW);
  assert.equal(uncounted.text, "?");
  assert.equal(uncounted.budget, `/ ${usd(50)}`);
  assert.match(uncounted.details, /window this board cannot count/);
});

// A reading the dial cannot stand behind keeps its own words: no dollars as
// the value, the age always, the last known dollars named as old, and never
// an OpenAI subscription's allowance.
test("a missing, refused, rate-limited or unavailable reading says which it is", () => {
  for (const [status, words] of [["missing", "key missing"], ["auth_required", "key refused"], ["rate_limited", "rate limited"], ["unavailable", "unavailable"], ["stale", "stale"]] as const) {
    const state = keySpendState(reading({ status, detail: `the provider said ${status}` }), NOW);
    assert.equal(state.text, "?", status);
    assert.equal(state.isUsable, false, status);
    assert.match(state.label, new RegExp(words), status);
    assert.match(state.details, new RegExp(`Last read ${usd(9.04).replace(/[$]/g, "\\$")} of ${usd(50).replace(/[$]/g, "\\$")} this week`), `${status}: ${state.details}`);
    assert.doesNotMatch(state.details, /OpenAI|weekly remaining/);
  }
});

// A reading older than the hour it can be trusted for, or one whose window
// rolled over, is stale: its dollars are named as old, never as the value.
test("an old reading or one past its reset is stale, and its dollars are named as old", () => {
  const old = keySpendState(reading({ read_at: new Date(NOW - 90 * 60_000).toISOString() }), NOW);
  assert.equal(old.text, "?");
  assert.match(old.details, /Reading 90 min ago\. Reading stale\./);
  assert.match(old.details, new RegExp(`Last read ${usd(9.04).replace(/[$]/g, "\\$")}`));
  const rolledOver = keySpendState(reading({ resets_at: new Date(NOW - 60_000).toISOString() }), NOW);
  assert.equal(rolledOver.text, "?");
  assert.match(rolledOver.details, /Reading stale\./);
  const unknownAge = keySpendState(reading({ read_at: "" }), NOW);
  assert.equal(unknownAge.text, "?");
  assert.match(unknownAge.details, /Reading time unknown/);
});

// BYOK spend belongs to the provider account rather than to the key's credit
// balance: it is said beside the dollars, and never added to them.
test("BYOK spend is reported beside the key's dollars, counted or not", () => {
  const separate = keySpendState(reading({ byok_usd: 1.2, includes_byok: false }), NOW);
  assert.equal(separate.text, usd(9.04));
  assert.match(separate.details, new RegExp(`BYOK spend ${usd(1.2).replace(/[$]/g, "\\$")} in the same window, counted separately`));
  const within = keySpendState(reading({ byok_usd: 1.2, includes_byok: true }), NOW);
  assert.equal(within.text, usd(9.04));
  assert.match(within.details, /counted toward this limit/);
  assert.doesNotMatch(keySpendState(reading({ byok_usd: 0 }), NOW).details, /BYOK/);
});

// A member or workspace budget that stops requests first is named as that
// scope, with its own spend, and never as this key's spend.
test("a budget that blocks first is named as its own scope, not as the key's spend", () => {
  const state = keySpendState(reading({ blocking: { scope: "member", spent_usd: 32.5, limit_usd: 50, period: "weekly" } }), NOW);
  assert.equal(state.text, usd(9.04));
  assert.match(state.details, new RegExp(`Requests stop at the member budget first: ${usd(32.5).replace(/[$]/g, "\\$")} of ${usd(50).replace(/[$]/g, "\\$")} this week`));
  const workspace = keySpendState(reading({ blocking: { scope: "workspace_default_member", spent_usd: 1, limit_usd: 5, period: "lifetime" } }), NOW);
  assert.match(workspace.details, /at the workspace member budget first/);
});

// Money is written the way a person reads dollars: cents when there are
// cents, and no cents on a whole dollar.
test("a dollar figure carries cents only when it has them", () => {
  assert.equal(money(0), usd(0));
  assert.equal(money(50), usd(50));
  assert.equal(money(9.04), usd(9.04));
  assert.equal(money(1234.5), usd(1234.5));
  assert.equal(money(9.044), usd(9.04));
  assert.doesNotMatch(money(50), /\.00/);
});

// The snapshot's parse keeps a dollar figure only when it is a measured
// figure, and drops a provider this board has no dial for.
test("the snapshot keeps measured dollars and drops what it cannot state", () => {
  const snapshot = parseSnapshot({ healthy: true, key_spend: [
    { provider: "openrouter", status: "available", spent_usd: 9.04, limit_usd: "50", remaining_usd: -1, period: "yearly", resets_at: "", read_at: "2026-10-15T11:59:00Z", blocking: null, detail: "" },
    { provider: "anthropic", status: "available", spent_usd: 1 },
  ] });
  assert.equal(snapshot.key_spend!.length, 1);
  const held = snapshot.key_spend![0];
  assert.equal(held.spent_usd, 9.04);
  assert.equal(held.limit_usd, null);
  assert.equal(held.remaining_usd, null);
  assert.equal(held.period, "");
  assert.equal(held.includes_byok, false);
  assert.equal(keySpendState(held, NOW).text, usd(9.04));
});

// The dial never states money it does not have, whatever it was handed.
test("an unreadable snapshot yields no dial and no fabricated spend", () => {
  assert.deepEqual(parseSnapshot({ healthy: true }).key_spend, []);
  assert.deepEqual(parseSnapshot({ healthy: true, key_spend: [{ provider: "openrouter", status: "wat", spent_usd: 3, limit_usd: null, remaining_usd: null, period: "", resets_at: "", read_at: "", byok_usd: null, includes_byok: false, blocking: null, detail: "" }] }).key_spend!.map((entry) => entry.status), ["unavailable"]);
});
