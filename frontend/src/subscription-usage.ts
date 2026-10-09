import type { SubscriptionUsage } from "./types.ts";

const MAX_AGE = 60 * 60 * 1000;
// Within this many points of the reserve, the home's weekly floor for the
// provider, the ring turns the warning colour, the only mark of the reserve on
// the ring: the Overlord, 2026-10-07, on a tick by the ring's end at 75%: "why
// tick here with 75% remaining ... that doesnt make sense".
const NEAR_RESERVE = 5;

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
  // A floor of 0 keeps no reserve: the week runs until the provider refuses.
  const floor = usage.floor_percent;
  const isReserve = remaining !== null && floor !== null && floor > 0 && remaining <= floor;
  const isNearReserve = remaining !== null && floor !== null && remaining <= floor + NEAR_RESERVE;
  const reserve = floor === null ? "Reserve unknown" : floor === 0 ? "No reserve, runs to 0%" : `${floor}% reserve`;
  const label = remaining === null ? `${provider} weekly remaining unavailable` : `${provider} ${text} weekly remaining`;
  const state = remaining !== null ? (isReserve ? `${reserve} reached` : reserve) : ({ auth_required: "Subscription sign-in required", stale: "Reading stale", unavailable: "Reading unavailable", available: "Reading unavailable" })[status];
  const age = !Number.isFinite(readAt) || readAt <= 0 || readAt > now + 60_000 ? "Reading time unknown" : `Reading ${now - readAt < 60_000 ? "just now" : `${Math.floor((now - readAt) / 60_000)} min ago`}`;
  const reset = Number.isFinite(resetsAt) && resetsAt > 0 ? `Resets ${new Date(resetsAt).toLocaleString(undefined, { year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit", timeZoneName: "short" })}` : "Reset time unknown";
  const details = `${label}. ${reset}. ${age}. ${state}${remaining === null ? `. ${reserve}` : ""}.`;
  return { remaining, text, isReserve, isNearReserve, label, details };
}
