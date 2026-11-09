import { memo, Suspense, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { boardPage } from "./board-page";
import { BRAND_MARKS } from "./brandMarks";
import { useClickFeedback } from "./click-feedback";
import { message, reportToCfo, request } from "./api";
import { parseActionsSpend, type ActionsSpend } from "./types";
import "./actions-spend.css";

const ActionsSpendPanel = boardPage(() => import("./actions-spend-panel").then((module) => module.ActionsSpendPanel), "GitHub Actions spend");
const DOLLARS = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD" });
const REFRESH_INTERVAL_MS = 3_600_000;

export const ActionsSpendHeader = memo(function ActionsSpendHeader({ usage, month, instance, now }: { usage?: ActionsSpend; month: string; instance: string; now: number }) {
  const [isOpen, setIsOpen] = useState(false);
  const [isRepairing, setIsRepairing] = useState(false);
  const [repairError, showRepairError] = useClickFeedback();
  const repair = useRef<AbortController | null>(null);
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
        // checked_at is on the supervisor's clock, so the next check is kept
        // between this device's next whole minute and an hour from now: a
        // device clock ahead neither asks again on every update, nor one
        // behind keeps a reading for hours.
        const receivedAt = Date.now();
        const checkedAt = Date.parse(reading.checked_at);
        const nextCheck = (checkedAt > 0 ? checkedAt : receivedAt) + REFRESH_INTERVAL_MS;
        const nextMinute = receivedAt - (receivedAt % 60_000) + 60_000;
        setSelected({ reading, instance, refreshAt: Math.min(Math.max(nextCheck, nextMinute), receivedAt + REFRESH_INTERVAL_MS) });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        reportToCfo("reading GitHub Actions usage", message(error));
        setSelected({ reading: { status: "unavailable", month, total: null, repositories: [], read_at: "", checked_at: "", detail: "Could not read this month's GitHub Actions usage." }, instance, refreshAt: Date.now() + REFRESH_INTERVAL_MS });
      });
    return () => controller.abort();
  }, [shouldRequest, month, instance, now]);
  useEffect(() => () => repair.current?.abort(), []);
  const supplied = usage?.month === month ? usage : undefined;
  const reading = cached && (!supplied || Date.parse(cached.reading.checked_at) >= Date.parse(supplied.checked_at)) ? cached.reading : supplied;
  const isPending = reading?.recovery?.state === "ready" || reading?.recovery?.state === "running";
  const pendingRun = reading?.recovery?.run_id;
  useEffect(() => {
    if (!isOpen || !isPending) return;
    const controller = new AbortController();
    const deadline = setTimeout(() => controller.abort(), 5 * 60_000);
    async function followConsent() {
      try {
        for (let attempt = 0; attempt < 150 && !controller.signal.aborted; attempt++) {
          await new Promise((resolve) => setTimeout(resolve, 2000));
          if (controller.signal.aborted) return;
          const updated = parseActionsSpend(await request("/api/actions-spend?month=" + month, controller.signal, { headers: { "X-CFO-Token": instance } }));
          setSelected({ reading: updated, instance, refreshAt: Date.now() + REFRESH_INTERVAL_MS });
          if (updated.recovery?.state !== "ready" && updated.recovery?.state !== "running") return;
        }
      } catch (error: unknown) {
        if (!controller.signal.aborted) showRepairError(message(error));
      }
    }
    void followConsent();
    return () => { controller.abort(); clearTimeout(deadline); };
  }, [isOpen, isPending, pendingRun, instance, month]);
  async function recover(action: string) {
    if (repair.current) return;
    const controller = new AbortController();
    repair.current = controller;
    const deadline = setTimeout(() => controller.abort(), 40_000);
    setIsRepairing(true);
    showRepairError("");
    try {
      const updated = parseActionsSpend(await request("/api/actions-spend/repair", controller.signal, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ month, action }) }));
      if (!controller.signal.aborted) setSelected({ reading: updated, instance, refreshAt: Date.now() + REFRESH_INTERVAL_MS });
    } catch (error: unknown) {
      showRepairError(controller.signal.aborted ? "Billing request timed out. Reopen the panel." : message(error));
    } finally {
      clearTimeout(deadline);
      repair.current = null;
      setIsRepairing(false);
    }
  }
  if (!usage) return null;
  const total = reading?.status === "available" || reading?.status === "stale" ? reading.total : null;
  const isOld = reading?.status === "stale";
  const billed = total ? DOLLARS.format(total.billed_usd) : "Unavailable";
  const label = total ? `${billed} billed this month` + (isOld ? " (old)" : "") : "unavailable";
  return <>
    <button className="actions-spend-header" aria-label={"GitHub Actions spend: " + label} aria-haspopup="dialog" aria-expanded={isOpen} onClick={() => setIsOpen(true)}>
      <svg className="icon brand-glyph" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d={BRAND_MARKS.github.path} /></svg>
      <span className={total ? "actions-spend-billed" : undefined}>{billed}</span>{isOld && <span className="actions-spend-old">Old</span>}
    </button>
    {isOpen && createPortal(<Suspense fallback={<p role="status" className="actions-spend-loading">Opening Actions spend...</p>}>
      <ActionsSpendPanel usage={reading} month={month} onClose={() => setIsOpen(false)} onRepair={(action) => { void recover(action); }} isRepairing={isRepairing} repairError={repairError} />
    </Suspense>, document.body)}
  </>;
});
