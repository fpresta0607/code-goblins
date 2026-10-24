import type { KeySpend } from "./types.ts";

// MAX_AGE is how old a reading may be before the dial shows it stale: the
// hour the supervisor trusts a subscription reading for.
const MAX_AGE = 60 * 60 * 1000;

// WINDOWS reads each window the key's own limit resets over as a sentence
// says it. A window this build cannot count has none.
const WINDOWS: Record<string, string> = { daily: "today", weekly: "this week", monthly: "this month", lifetime: "in total" };

// SCOPES names the budget layer that stops requests before this key's own, in
// the words the dial uses for it. Every other layer's name is shown as the
// provider wrote it.
const SCOPES: Record<string, string> = { member: "member", workspace: "workspace", workspace_default_member: "workspace member", workspace_default_key: "workspace key", api_key: "API key", key_limit: "key" };

// STATES names why a reading is not usable, in the few words a dial's
// accessible name can carry.
const STATES: Record<string, string> = { missing: "key missing", auth_required: "key refused", rate_limited: "rate limited", unavailable: "unavailable", stale: "stale" };

// money writes a dollar figure as the dial does: cents for part of a dollar,
// whole dollars for a whole one, and never a figure rounded to a zero it is
// not.
export function money(value: number): string {
  return value.toLocaleString(undefined, { style: "currency", currency: "USD", minimumFractionDigits: Number.isInteger(value) ? 0 : 2, maximumFractionDigits: 2 });
}

// keySpendState is what the OpenRouter dial says: the dollars spent as its
// primary value, the limit and window they count against beside it, and an
// honest state for a reading that is missing, refused, rate limited, stale or
// one this build cannot count. It never invents a percentage for a key with
// no limit, and never shows dollars it cannot say the age of.
export function keySpendState(usage: KeySpend, now: number) {
  const readAt = Date.parse(usage.read_at), resetsAt = Date.parse(usage.resets_at);
  const isReadTime = Number.isFinite(readAt) && readAt > 0 && readAt <= now + 60_000;
  let status = usage.status;
  if (status === "available") {
    if (!isReadTime) status = "unavailable";
    else if (now - readAt > MAX_AGE || Number.isFinite(resetsAt) && resetsAt > 0 && resetsAt <= now) status = "stale";
  }
  // A reading this build cannot count still names its limit, so the dial says
  // the limit with no dollars rather than a number taken from the wrong
  // window.
  const isUsable = status === "available" && usage.spent_usd !== null;
  const spent = isUsable ? usage.spent_usd : null;
  const limit = usage.limit_usd;
  const window = WINDOWS[usage.period] ?? "";
  const over = limit === null ? "" : `${money(limit)}${window ? ` ${window}` : ""}`;
  const budget = limit === null ? "no limit" : `/ ${over}`;
  const text = spent === null ? "?" : money(spent);
  // The ring states what is left of the limit, to the hundredth the dials
  // beside it are read to.
  const percentRemaining = isUsable && limit !== null && limit > 0 && usage.remaining_usd !== null ? Math.round(Math.max(0, Math.min(100, usage.remaining_usd / limit * 100)) * 100) / 100 : null;
  const label = spent === null ? `OpenRouter spend ${STATES[status] ?? "unavailable"}` : limit === null ? `OpenRouter ${money(spent)} spent, no limit` : `OpenRouter ${money(spent)} of ${over}`;

  const parts: string[] = [];
  // Dollars the dial will not show as its value are still named, as the last
  // ones it read, with the age beside them.
  if (usage.spent_usd !== null) parts.push(`${isUsable ? "Spent" : "Last read"} ${money(usage.spent_usd)}${limit === null ? " in total, no limit" : ` of ${over}`}`);
  else if (limit !== null) parts.push(`Limit ${over}`);
  if (Number.isFinite(resetsAt) && resetsAt > 0) parts.push(`Resets ${new Date(resetsAt).toLocaleString(undefined, { year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit", timeZoneName: "short" })}`);
  parts.push(!isReadTime ? "Reading time unknown" : `Reading ${now - readAt < 60_000 ? "just now" : `${Math.floor((now - readAt) / 60_000)} min ago`}`);
  if (status === "stale") parts.push("Reading stale");
  else if (status !== "available" || !isUsable) parts.push(usage.detail || `Reading ${STATES[status] ?? "unavailable"}`);
  if (usage.byok_usd !== null && usage.byok_usd > 0) parts.push(`BYOK spend ${money(usage.byok_usd)} in the same window, counted ${usage.includes_byok ? "toward this limit" : "separately"}`);
  if (usage.blocking !== null) {
    const scope = SCOPES[usage.blocking.scope] ?? usage.blocking.scope;
    const blockingWindow = WINDOWS[usage.blocking.period] ?? "";
    parts.push(`Requests stop at the ${scope} budget first: ${money(usage.blocking.spent_usd)} of ${money(usage.blocking.limit_usd)}${blockingWindow ? ` ${blockingWindow}` : ""}`);
  }
  return { text, budget, percentRemaining, label, details: `${parts.join(". ")}.`, isUsable };
}
