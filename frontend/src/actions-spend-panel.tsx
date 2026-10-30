import { useEffect, useId, useRef } from "react";
import { Icon } from "./Icon";
import type { ActionsSpend } from "./types";

const DOLLARS = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD" });

export function ActionsSpendPanel({ usage, month, onClose }: { usage?: ActionsSpend; month: string; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId();
  useEffect(() => {
    const element = dialog.current;
    const source = document.activeElement;
    element?.showModal();
    return () => { element?.close(); if (source instanceof HTMLElement && source.isConnected) source.focus(); };
  }, []);
  const amount = usage?.total_usd;
  const hasTotal = usage && amount != null && (usage.status === "available" || usage.status === "stale");
  const label = new Date(month + "-01T00:00:00Z").toLocaleDateString("en-US", { month: "long", year: "numeric", timeZone: "UTC" });
  const detail = usage?.detail || (!usage ? "Waiting for this month's GitHub Actions usage." : "");
  return <dialog ref={dialog} className="actions-spend-panel" aria-labelledby={title} onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onClose(); }}>
    <div className="actions-spend-title"><h2 id={title}>GitHub Actions</h2><button className="icon-button" aria-label="Close Actions spend" onClick={onClose}><Icon name="close" /></button></div>
    <p className="actions-spend-month">{label}</p>
    <p className="actions-spend-total">{hasTotal ? DOLLARS.format(amount) : "Spend unavailable"}</p>
    <p className="muted">This month, after included minutes. USD.</p>
    {usage?.status === "stale" && <p className="actions-spend-old">Old reading</p>}
    {detail && <p className="actions-spend-problem">{detail}</p>}
    {hasTotal && (usage.repositories.length > 0 ? <table>
      <thead><tr><th scope="col">Repository</th><th scope="col">Spend</th></tr></thead>
      <tbody>{usage.repositories.map((repository) => <tr key={repository.name}><td>{repository.name}</td><td>{DOLLARS.format(repository.spent_usd)}</td></tr>)}</tbody>
    </table> : <p>No repository has billable Actions usage this month.</p>)}
    <div className="actions-spend-dates muted">
      {hasTotal && <p>Read <time dateTime={usage.read_at}>{new Date(usage.read_at).toLocaleString("en-US", { dateStyle: "medium", timeStyle: "short" })}</time></p>}
      {usage && Date.parse(usage.checked_at) > 0 && usage.checked_at !== usage.read_at && <p>Last attempt <time dateTime={usage.checked_at}>{new Date(usage.checked_at).toLocaleString("en-US", { dateStyle: "medium", timeStyle: "short" })}</time></p>}
      <p>Read from GitHub at most once an hour.</p>
    </div>
  </dialog>;
}
