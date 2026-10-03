import type { SubscriptionUsage } from "./types.ts";

const MAX_AGE = 60 * 60 * 1000;
const RESERVE = 5;

export function subscriptionState(usage: SubscriptionUsage, now: number) {
  const provider = usage.provider === "claude" ? "Claude" : "OpenAI";
  const readAt = Date.parse(usage.read_at), resetsAt = Date.parse(usage.resets_at);
  let status = usage.status;
  if (status === "available") {
    if (!Number.isFinite(readAt) || readAt <= 0 || readAt > now + 60_000) status = "unavailable";
    else if (now - readAt > MAX_AGE || Number.isFinite(resetsAt) && resetsAt > 0 && resetsAt <= now) status = "stale";
  }
  const remaining = status === "available" ? usage.percent_remaining : null;
  const text = remaining === null ? "?" : `${Number(remaining.toFixed(2))}%`;
  const isReserve = remaining !== null && remaining <= RESERVE;
  const label = remaining === null ? `${provider} weekly remaining unavailable` : `${provider} ${text} weekly remaining`;
  const state = remaining !== null ? (isReserve ? "5% reserve reached" : "5% reserve") : ({ auth_required: "Subscription sign-in required", stale: "Reading stale", unavailable: "Reading unavailable", available: "Reading unavailable" })[status];
  const age = !Number.isFinite(readAt) || readAt <= 0 || readAt > now + 60_000 ? "Reading time unknown" : `Reading ${now - readAt < 60_000 ? "just now" : `${Math.floor((now - readAt) / 60_000)} min ago`}`;
  const reset = Number.isFinite(resetsAt) && resetsAt > 0 ? `Resets ${new Date(resetsAt).toISOString()}` : "Reset time unknown";
  const source = usage.source === "oauth" ? "quota-axi OAuth" : "quota-axi";
  const details = `${label}. ${reset}. ${age}. ${state}${remaining === null ? ". 5% reserve" : ""}. ${source}.`;
  return { remaining, text, isReserve, label, details };
}
