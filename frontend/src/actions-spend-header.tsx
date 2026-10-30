import { memo, Suspense, useState } from "react";
import { createPortal } from "react-dom";
import { boardPage } from "./board-page";
import { BRAND_MARKS } from "./brandMarks";
import type { ActionsSpend } from "./types";
import "./actions-spend.css";

const ActionsSpendPanel = boardPage(() => import("./actions-spend-panel").then((module) => module.ActionsSpendPanel), "GitHub Actions spend");
const DOLLARS = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD" });

export const ActionsSpendHeader = memo(function ActionsSpendHeader({ usage, month }: { usage?: ActionsSpend; month: string }) {
  const [isOpen, setIsOpen] = useState(false);
  if (!usage) return null;
  const reading = usage.month === month ? usage : undefined;
  const amount = reading?.total_usd;
  const hasTotal = amount != null && (reading?.status === "available" || reading?.status === "stale");
  const isOld = reading?.status === "stale";
  const total = hasTotal ? DOLLARS.format(amount) : "Unavailable";
  const label = hasTotal ? total + " this month" + (isOld ? " (old)" : "") : "unavailable";
  return <>
    <button className="actions-spend-header" aria-label={"GitHub Actions spend: " + label} aria-haspopup="dialog" aria-expanded={isOpen} onClick={() => setIsOpen(true)}>
      <svg className="icon brand-glyph" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d={BRAND_MARKS.github.path} /></svg>
      <span>{total}</span>{isOld && <span className="actions-spend-old">Old</span>}
    </button>
    {isOpen && createPortal(<Suspense fallback={<p role="status" className="actions-spend-loading">Opening Actions spend...</p>}>
      <ActionsSpendPanel usage={reading} month={month} onClose={() => setIsOpen(false)} />
    </Suspense>, document.body)}
  </>;
});
