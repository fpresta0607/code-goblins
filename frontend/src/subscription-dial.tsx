import { useId } from "react";
import { BRAND_MARKS } from "./brandMarks";
import { subscriptionState } from "./subscription-usage";
import type { SubscriptionUsage } from "./types";

export function SubscriptionDial({ usage, now }: { usage: SubscriptionUsage; now: number }) {
  const state = subscriptionState(usage, now);
  const description = useId();
  const brand = BRAND_MARKS[usage.provider === "claude" ? "claude" : "openai"];
  return <>
    <span className={`subscription-dial${state.isReserve ? " reserve" : ""}${state.remaining === null ? " unknown" : ""}`} role={state.remaining === null ? "img" : "progressbar"} tabIndex={0}
      aria-valuemin={state.remaining === null ? undefined : 0} aria-valuemax={state.remaining === null ? undefined : 100} aria-valuenow={state.remaining ?? undefined}
      aria-label={state.label} aria-describedby={description} data-tip={state.details} data-tip-align="end">
      <svg className="subscription-ring" viewBox="0 0 52 52" aria-hidden="true" focusable="false">
        <circle className="subscription-track" cx="26" cy="26" r="22" />
        {state.remaining !== null && <circle className="subscription-fill" cx="26" cy="26" r="22" pathLength="100" strokeDasharray={`${state.remaining} 100`} transform="rotate(-90 26 26)" />}
        <path className="subscription-reserve" d="M26 1v7" transform="rotate(18 26 26)" />
        <svg x="15" y="15" width="22" height="22" viewBox="0 0 24 24" fill={usage.provider === "claude" ? brand.ink : "currentColor"}><path d={brand.path} /></svg>
      </svg>
      <strong className="subscription-percent">{state.text}</strong>
    </span>
    <span id={description} className="sr-only">{state.details}</span>
  </>;
}
