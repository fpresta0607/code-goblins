import { memo, Suspense, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { boardPage } from "./board-page";
import { BRAND_MARKS } from "./brandMarks";
import { useClickFeedback } from "./click-feedback";
import { message, reportToCeo, request } from "./api";
import { parseActionsSpend, type ActionsSpend } from "./types";
import "./actions-spend.css";

const ActionsSpendPanel = boardPage(() => import("./actions-spend-panel").then((module) => module.ActionsSpendPanel), "GitHub Actions spend");
const DOLLARS = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD" });
const REFRESH_INTERVAL_MS = 3_600_000;

interface KeptReading { reading: ActionsSpend; instance: string; refreshAt: number }

// keeping sets month's reading among the ones a device asked for itself.
const keeping = (month: string, reading: ActionsSpend, instance: string, refreshAt: number) => (kept: Record<string, KeptReading>) => ({ ...kept, [month]: { reading, instance, refreshAt } });

export const ActionsSpendHeader = memo(function ActionsSpendHeader({ usage, month, instance, now }: { usage?: ActionsSpend; month: string; instance: string; now: number }) {
  const [isOpen, setIsOpen] = useState(false);
  const [isLastMonth, setIsLastMonth] = useState(false);
  const [isRepairing, setIsRepairing] = useState(false);
  const [repairError, showRepairError] = useClickFeedback();
  const repair = useRef<AbortController | null>(null);
  // Each month's reading this device asked for itself, by month. The bar's
  // billed figure stays this month's; the window shows last month only while
  // it is chosen there (#2536).
  const [kept, setKept] = useState<Record<string, KeptReading>>({});
  const [year, monthNumber] = month.split("-").map(Number);
  const before = new Date(year, monthNumber - 2, 1);
  const lastMonth = `${before.getFullYear()}-${String(before.getMonth() + 1).padStart(2, "0")}`;
  const shownMonth = isOpen && isLastMonth ? lastMonth : month;
  const keptFor = (wanted: string) => kept[wanted]?.instance === instance ? kept[wanted] : undefined;
  const isDue = (wanted: string) => !!usage && usage.month !== wanted && now >= (keptFor(wanted)?.refreshAt ?? 0);
  const requested = [...new Set([month, shownMonth])].filter(isDue).join(" ");
  useEffect(() => {
    if (!requested) return;
    const controller = new AbortController();
    for (const wanted of requested.split(" ")) {
      request("/api/actions-spend?month=" + wanted, controller.signal, { headers: { "X-CEO-Token": instance } })
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
          setKept(keeping(wanted, reading, instance, Math.min(Math.max(nextCheck, nextMinute), receivedAt + REFRESH_INTERVAL_MS)));
        })
        .catch((error: unknown) => {
          if (controller.signal.aborted) return;
          reportToCeo("reading GitHub Actions usage", message(error));
          const detail = "Could not read " + (wanted === month ? "this" : "last") + " month's GitHub Actions usage.";
          setKept(keeping(wanted, { status: "unavailable", month: wanted, total: null, repositories: [], read_at: "", checked_at: "", detail }, instance, Date.now() + REFRESH_INTERVAL_MS));
        });
    }
    return () => controller.abort();
  }, [requested, month, instance, now]);
  useEffect(() => () => repair.current?.abort(), []);
  const readingOf = (wanted: string) => {
    const cached = keptFor(wanted);
    const supplied = usage?.month === wanted ? usage : undefined;
    return cached && (!supplied || Date.parse(cached.reading.checked_at) >= Date.parse(supplied.checked_at)) ? cached.reading : supplied;
  };
  const reading = readingOf(month);
  const shown = readingOf(shownMonth);
  const isPending = shown?.recovery?.state === "ready" || shown?.recovery?.state === "running";
  const pendingRun = shown?.recovery?.run_id;
  useEffect(() => {
    if (!isOpen || !isPending) return;
    const controller = new AbortController();
    const deadline = setTimeout(() => controller.abort(), 5 * 60_000);
    async function followConsent() {
      try {
        for (let attempt = 0; attempt < 150 && !controller.signal.aborted; attempt++) {
          await new Promise((resolve) => setTimeout(resolve, 2000));
          if (controller.signal.aborted) return;
          const updated = parseActionsSpend(await request("/api/actions-spend?month=" + shownMonth, controller.signal, { headers: { "X-CEO-Token": instance } }));
          setKept(keeping(shownMonth, updated, instance, Date.now() + REFRESH_INTERVAL_MS));
          if (updated.recovery?.state !== "ready" && updated.recovery?.state !== "running") return;
        }
      } catch (error: unknown) {
        if (!controller.signal.aborted) showRepairError(message(error));
      }
    }
    void followConsent();
    return () => { controller.abort(); clearTimeout(deadline); };
  }, [isOpen, isPending, pendingRun, instance, shownMonth]);
  async function recover(action: string) {
    if (repair.current) return;
    const repaired = shownMonth;
    const controller = new AbortController();
    repair.current = controller;
    const deadline = setTimeout(() => controller.abort(), 40_000);
    setIsRepairing(true);
    showRepairError("");
    try {
      const updated = parseActionsSpend(await request("/api/actions-spend/repair", controller.signal, { method: "POST", headers: { "Content-Type": "application/json", "X-CEO-Token": instance }, body: JSON.stringify({ month: repaired, action }) }));
      if (!controller.signal.aborted) setKept(keeping(repaired, updated, instance, Date.now() + REFRESH_INTERVAL_MS));
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
      <ActionsSpendPanel usage={shown} month={shownMonth} months={[month, lastMonth]} onMonth={(chosen) => setIsLastMonth(chosen === lastMonth)} onClose={() => { setIsOpen(false); setIsLastMonth(false); }} onRepair={(action) => { void recover(action); }} isRepairing={isRepairing} repairError={repairError} />
    </Suspense>, document.body)}
  </>;
});
