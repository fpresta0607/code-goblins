import type { KeySpend, SubscriptionUsage } from "./types";
import type { SessionUsage } from "./session-messages";

// What a session has used, drawn from what the fleet already read: its own
// conversation's usage and its provider's allowance. Every number comes from
// one of those, and a reading none of them carries is left out, never drawn
// empty or said to be unavailable (the Overlord, 2026-10-28: "right now it's
// just saying context is unavailable").

// One reading the usage bar draws as a pie: the share of a limit used, in
// percent, and the numbers behind it in words.
export interface UsageReading {
  key: "context" | "week" | "spend";
  label: string;
  percent: number;
  detail: string;
}

// formatCount draws a count of tokens or bytes as pi's own footer does: a
// million, thousands, or the number itself.
export function formatCount(count: number): string {
  if (!Number.isFinite(count) || count <= 0) return "0";
  if (count >= 1_000_000) return trim(count / 1_000_000) + "M";
  if (count >= 1_000) return trim(count / 1_000) + "k";
  return String(Math.round(count));
}

function trim(value: number): string {
  if (value >= 100) return String(Math.round(value));
  const drawn = (Math.round(value * 10) / 10).toFixed(1);
  return value < 10 ? drawn : drawn.replace(/\.0$/, "");
}

// formatUSD draws dollars as the harness's footer does, to three decimals so
// a small spend is not read as nothing.
export function formatUSD(dollars: number): string {
  return "$" + (Math.round(dollars * 1000) / 1000).toFixed(3);
}

// formatAge draws when something was last written, short, as a timestamp
// under a message reads.
export function formatAge(at: string, now: number): string {
  const then = at && !at.startsWith("0001") ? Date.parse(at) : NaN;
  if (!Number.isFinite(then)) return "";
  const minutes = Math.max(0, Math.floor((now - then) / 60_000));
  if (minutes < 1) return "just now";
  if (minutes < 60) return minutes + "m ago";
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return hours + "h ago";
  const days = Math.floor(hours / 24);
  if (days < 30) return days + "d ago";
  const months = Math.floor(days / 30);
  return months < 12 ? months + " months ago" : Math.floor(months / 12) + " years ago";
}

// elapsedText draws how long a session has been working, short: seconds under
// a minute, minutes and seconds under an hour, and hours and minutes after.
export function elapsedText(since: string, now: number): string {
  const from = since && !since.startsWith("0001") ? Date.parse(since) : NaN;
  if (!Number.isFinite(from)) return "";
  const seconds = Math.max(0, Math.floor((now - from) / 1000));
  if (seconds < 60) return seconds + "s";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return minutes + "m " + (seconds % 60) + "s";
  return Math.floor(minutes / 60) + "h " + (minutes % 60) + "m";
}

// usageReadings is every reading the session backs: the context its last
// request carried against its model's window, the week its Claude or Codex
// subscription has used, and, for any other harness, the spend of the
// OpenRouter key it runs on against that key's limit.
export function usageReadings(harness: string, usage?: SessionUsage, subscriptions: SubscriptionUsage[] = [], spend: KeySpend[] = []): UsageReading[] {
  const readings: UsageReading[] = [];
  if (usage && usage.context_window > 0 && usage.context_tokens > 0) {
    readings.push({ key: "context", label: "Context", percent: share(usage.context_tokens, usage.context_window), detail: formatCount(usage.context_tokens) + " of " + formatCount(usage.context_window) + " tokens" });
  }
  if (harness === "claude" || harness === "codex") {
    const subscription = subscriptions.find((each) => each.provider === harness);
    if (subscription?.status === "available" && subscription.percent_remaining != null) {
      readings.push({ key: "week", label: "Week", percent: share(100 - subscription.percent_remaining, 100), detail: subscription.resets_at ? "Resets " + resetText(subscription.resets_at) : Math.round(subscription.percent_remaining) + "% left" });
    }
    return readings;
  }
  const key = spend.find((each) => each.provider === "openrouter");
  if (key?.status === "available" && key.spent_usd != null && key.limit_usd) {
    readings.push({ key: "spend", label: "Key spend", percent: share(key.spent_usd, key.limit_usd), detail: formatUSD(key.spent_usd) + " of " + formatUSD(key.limit_usd) + (key.resets_at ? ", resets " + resetText(key.resets_at) : "") });
  }
  return readings;
}

// share is part of whole in percent, held between 0 and 100.
function share(part: number, whole: number): number {
  return Math.max(0, Math.min(100, part * 100 / whole));
}

// resetText draws when an allowance resets, in the words the board uses for
// a time today or in the week ahead.
function resetText(at: string): string {
  const time = Date.parse(at);
  if (!Number.isFinite(time)) return at;
  return new Date(time).toLocaleString(undefined, { weekday: "short", month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
}
