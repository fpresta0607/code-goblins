import { useId } from "react";
import { BRAND_MARKS } from "./brandMarks";
import { keySpendState } from "./key-spend";
import type { KeySpend } from "./types";

// The OpenRouter dial, beside the subscription rings and in their language: a
// ring of what is left of the key's own limit, and the key's dollars as the
// value that matters, with the limit and the window it resets over under them.
// A key with no limit has no ring fraction, since a percentage of nothing is
// no reading. The dollars are key-wide: every caller of one key shows one
// total, and the external BYOK spend is reported beside them, never in them.
export function KeySpendDial({ usage, now }: { usage: KeySpend; now: number }) {
  const state = keySpendState(usage, now);
  const description = useId();
  const brand = BRAND_MARKS.openrouter;
  const isRinged = state.percentRemaining !== null;
  return <>
    <span className={`subscription-dial key-spend-dial${state.isUsable ? "" : " unknown"}`} role={isRinged ? "progressbar" : "img"} tabIndex={0}
      aria-valuemin={isRinged ? 0 : undefined} aria-valuemax={isRinged ? 100 : undefined} aria-valuenow={isRinged ? state.percentRemaining ?? undefined : undefined}
      aria-label={state.label} aria-describedby={description} data-tip={state.details}>
      <svg className="subscription-ring" viewBox="0 0 52 52" aria-hidden="true" focusable="false">
        <circle className="subscription-track" cx="26" cy="26" r="22" />
        {isRinged && <circle className="subscription-fill" cx="26" cy="26" r="22" pathLength="100" strokeDasharray={`${state.percentRemaining} 100`} transform="rotate(-90 26 26)" />}
        <svg x="15" y="15" width="22" height="22" viewBox="0 0 24 24" fill={brand.ink}><path d={brand.path} /></svg>
      </svg>
      <span className="key-spend-values">
        <strong className="key-spend-spent">{state.text}</strong>
        <span className="key-spend-budget">{state.budget}</span>
      </span>
    </span>
    <span id={description} className="sr-only">{state.details}</span>
  </>;
}
