import type { KeySpend, SubscriptionUsage } from "./types";
import type { SessionUsage } from "./session-messages";

// What a session has spent, drawn from what the fleet already read: its own
// conversation's usage, the model and effort the board recorded for it, and
// the memory its processes hold. Every number comes from one of those, and a
// stat none of them carries is left out and named as missing, never invented.

export interface SessionFacts {
  usage?: SessionUsage;
  model: string;
  effort: string;
  memory: number;
}

export interface Stat {
  key: string;
  text: string;
  label: string;
}

export interface SessionStats {
  // parts are the compact line's stats, in the order the harness draws them.
  parts: Stat[];
  // details are the same stats with their names, for the full usage panel.
  details: Stat[];
  // missing names, in plain words, each stat that could not be read.
  missing: string[];
  // hasUsage says the conversation recorded any usage at all.
  hasUsage: boolean;
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

// formatMemory draws the bytes a session's processes hold, in the binary
// units Windows reports them in.
export function formatMemory(bytes: number): string {
  if (bytes >= 2 ** 30) return trim(bytes / 2 ** 30) + "G";
  if (bytes >= 2 ** 20) return trim(bytes / 2 ** 20) + "M";
  if (bytes >= 2 ** 10) return trim(bytes / 2 ** 10) + "k";
  return String(Math.round(bytes));
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

// sessionStats reads every stat the fleet can back, and names each it cannot.
export function sessionStats({ usage, model, effort, memory }: SessionFacts): SessionStats {
  const parts: Stat[] = [];
  const details: Stat[] = [];
  const missing: string[] = [];
  const add = (key: string, compact: string, label: string, value: string) => {
    parts.push({ key, text: compact, label });
    details.push({ key, text: value, label });
  };
  if (usage) {
    if (usage.input_tokens > 0) add("input", "↑" + formatCount(usage.input_tokens), "Input tokens", formatCount(usage.input_tokens));
    if (usage.output_tokens > 0) add("output", "↓" + formatCount(usage.output_tokens), "Output tokens", formatCount(usage.output_tokens));
    if (usage.cache_read_tokens > 0) add("cache", "R" + formatCount(usage.cache_read_tokens), "Cache read", formatCount(usage.cache_read_tokens));
    if (usage.cost_usd > 0) add("cost", formatUSD(usage.cost_usd), "Cost", formatUSD(usage.cost_usd));
    else missing.push("its cost");
    if (usage.context_tokens > 0) {
      if (usage.context_window > 0) {
        const percent = Math.min(100, usage.context_tokens / usage.context_window * 100);
        add("context", (Math.round(percent * 10) / 10).toFixed(1) + "%/" + formatCount(usage.context_window), "Context", formatCount(usage.context_tokens) + " of " + formatCount(usage.context_window));
      } else {
        add("context", formatCount(usage.context_tokens), "Context", formatCount(usage.context_tokens));
        missing.push("its context window");
      }
    } else missing.push("its context");
  } else {
    missing.push("its token usage and spend");
  }
  if (memory > 0) add("memory", formatMemory(memory), "Memory", formatMemory(memory));
  else missing.push("its memory");
  if (model || effort) add("engine", [model, effort].filter(Boolean).join(" "), "Model and effort", [model, effort].filter(Boolean).join(" "));
  else missing.push("its model and effort");
  return { parts, details, missing, hasUsage: !!usage };
}

// What a session's provider says about its allowance, for the banner above
// the composer: the sentence, the detail under it, and why none is drawn.
export interface UsageReading {
  banner: { text: string; detail: string } | null;
  why: string;
}

// usageBanner reads a session's provider usage. A Claude Code or Codex
// session reads its subscription's remaining share, and a pi session its
// OpenRouter key's spend. A provider that is missing, stale or unreadable
// draws no banner, and the answer says why.
export function usageBanner(harness: string, subscriptions: SubscriptionUsage[] = [], spend: KeySpend[] = []): UsageReading {
  if (harness === "claude" || harness === "codex") {
    const subscription = subscriptions.find((each) => each.provider === harness);
    const label = harness === "claude" ? "Claude" : "Codex";
    if (!subscription || subscription.status !== "available" || subscription.percent_remaining == null) {
      return { banner: null, why: "its " + label + " usage could not be read" };
    }
    const used = Math.max(0, Math.min(100, 100 - subscription.percent_remaining));
    return {
      banner: {
        text: used >= 85 ? "Approaching weekly usage limit" : "Weekly usage",
        detail: subscription.resets_at ? "Resets " + resetText(subscription.resets_at) : Math.round(subscription.percent_remaining) + "% left",
      },
      why: "",
    };
  }
  const key = spend.find((each) => each.provider === "openrouter");
  if (!key || key.status !== "available" || key.spent_usd == null) return { banner: null, why: "its key spend could not be read" };
  const spent = formatUSD(key.spent_usd);
  const detail = key.limit_usd != null ? spent + " of " + formatUSD(key.limit_usd) + (key.resets_at ? " · Resets " + resetText(key.resets_at) : "") : spent + " spent";
  return { banner: { text: key.limit_usd != null ? "Session spend" : "Spent so far", detail }, why: "" };
}

// resetText draws when an allowance resets, in the words the board uses for
// a time today or in the week ahead.
function resetText(at: string): string {
  const time = Date.parse(at);
  if (!Number.isFinite(time)) return at;
  return new Date(time).toLocaleString(undefined, { weekday: "short", month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
}
