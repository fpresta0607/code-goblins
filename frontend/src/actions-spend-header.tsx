import { memo, Suspense, useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { boardPage } from "./board-page";
import { BRAND_MARKS } from "./brandMarks";
import { message, reportToCfo, request } from "./api";
import { parseActionsSpend, type ActionsSpend } from "./types";
import "./actions-spend.css";

const ActionsSpendPanel = boardPage(() => import("./actions-spend-panel").then((module) => module.ActionsSpendPanel), "GitHub Actions spend");
const DOLLARS = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD" });
const REFRESH_INTERVAL_MS = 3_600_000;

export const ActionsSpendHeader = memo(function ActionsSpendHeader({ usage, month, instance, now }: { usage?: ActionsSpend; month: string; instance: string; now: number }) {
  const [isOpen, setIsOpen] = useState(false);
  const [selected, setSelected] = useState<{ reading: ActionsSpend; instance: string; refreshAt: number }>();
  const cached = selected?.instance === instance && selected.reading.month === month ? selected : undefined;
  const shouldRequest = !!usage && usage.month !== month && (!cached || now >= cached.refreshAt);
  useEffect(() => {
    if (!shouldRequest) return;
    const controller = new AbortController();
    request("/api/actions-spend?month=" + month, controller.signal, { headers: { "X-CFO-Token": instance } })
      .then(parseActionsSpend)
      .then((reading) => {
        if (controller.signal.aborted) return;
        const checkedAt = Date.parse(reading.checked_at);
        setSelected({ reading, instance, refreshAt: (checkedAt > 0 ? checkedAt : Date.now()) + REFRESH_INTERVAL_MS });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        reportToCfo("reading GitHub Actions usage", message(error));
        setSelected({ reading: { status: "unavailable", month, total_usd: null, repositories: [], read_at: "", checked_at: "", detail: "Could not read this month's GitHub Actions usage." }, instance, refreshAt: Date.now() + REFRESH_INTERVAL_MS });
      });
    return () => controller.abort();
  }, [shouldRequest, month, instance]);
  if (!usage) return null;
  const source = usage.month === month ? usage : cached?.reading;
  const reading: ActionsSpend | undefined = source?.status === "available" && now - Date.parse(source.read_at) >= 7_200_000
    ? { ...source, status: "stale", detail: "The last GitHub Actions usage reading is old." } : source;
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
