import { useEffect, useId, useRef } from "react";
import { Icon } from "./Icon";
import type { ActionsSpend } from "./types";

const DOLLARS = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD" });
const MINUTES = new Intl.NumberFormat("en-US", { maximumFractionDigits: 0 });

function localTime(time: string) {
  return <time dateTime={time}>{new Date(time).toLocaleString("en-US", { dateStyle: "medium", timeStyle: "short" })}</time>;
}

// The month's GitHub Actions use: what it was worth at list price, what
// GitHub covered (public repositories and the plan's included minutes) and
// what it billed, then each repository, highest use first, and when it was
// read.
export function ActionsSpendPanel({ usage, month, onClose }: { usage?: ActionsSpend; month: string; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId();
  useEffect(() => {
    const element = dialog.current;
    const source = document.activeElement;
    element?.showModal();
    return () => { element?.close(); if (source instanceof HTMLElement && source.isConnected) source.focus(); };
  }, []);
  const reading = usage?.total && usage.status !== "unavailable" ? { ...usage, total: usage.total } : undefined;
  const label = new Date(month + "-01T00:00:00").toLocaleDateString("en-US", { month: "long", year: "numeric" });
  const detail = usage ? usage.detail : "Waiting for this month's GitHub Actions usage.";
  const hasFailedSince = reading?.status === "stale" && Date.parse(reading.checked_at) > Date.parse(reading.read_at);
  return <dialog ref={dialog} className="actions-spend-panel" aria-labelledby={title} onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onClose(); }}>
    <header className="actions-spend-title">
      <div><h2 id={title}>GitHub Actions</h2><span className="muted">{label}</span></div>
      <button className="icon-button" aria-label="Close Actions spend" onClick={onClose}><Icon name="close" /></button>
    </header>
    {reading && <dl className="actions-spend-figures">
      <div><dt>Used</dt><dd>{DOLLARS.format(reading.total.used_usd)}</dd></div>
      <div><dt>Covered</dt><dd>{DOLLARS.format(reading.total.used_usd - reading.total.billed_usd)}</dd></div>
      <div><dt>Billed</dt><dd>{DOLLARS.format(reading.total.billed_usd)}</dd></div>
    </dl>}
    {reading && (reading.repositories.length > 0 ? <table>
      <thead><tr><th scope="col">Repository</th><th scope="col">Minutes</th><th scope="col">Used</th><th scope="col">Billed</th></tr></thead>
      <tbody>{reading.repositories.map((repository) => <tr key={repository.name}>
        <td>{repository.name}</td><td>{MINUTES.format(repository.minutes)}</td><td>{DOLLARS.format(repository.used_usd)}</td><td>{DOLLARS.format(repository.billed_usd)}</td>
      </tr>)}</tbody>
    </table> : <p className="actions-spend-none">No Actions use this month.</p>)}
    {detail && <p className="actions-spend-problem">{detail}</p>}
    {reading && <p className="actions-spend-read muted">
      Read {localTime(reading.read_at)}{reading.status === "stale" && <> · <span className="actions-spend-old">Old</span></>}{hasFailedSince && <> · Last attempt {localTime(reading.checked_at)}</>}
    </p>}
  </dialog>;
}
