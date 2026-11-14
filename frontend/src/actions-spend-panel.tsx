import { useEffect, useId, useRef, useState } from "react";
import { ActionsSpendMonthMenu } from "./actions-spend-month-menu";
import { ClickFeedback } from "./click-feedback";
import { Icon } from "./Icon";
import type { ActionsSpend } from "./types";
import { linkedText } from "./messageText";

const DOLLARS = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD" });
const MINUTES = new Intl.NumberFormat("en-US", { maximumFractionDigits: 0 });

function localTime(time: string) {
  return <time dateTime={time}>{new Date(time).toLocaleString("en-US", { dateStyle: "medium", timeStyle: "short" })}</time>;
}

// The month's GitHub Actions use: what it was worth at list price, what
// GitHub covered (public repositories and the plan's included minutes) and
// what it billed, then each repository, highest use first, and when it was
// read. The month beside the title chooses this month or last month, and a
// retry is one icon in the title row that turns while the read runs (#2536).
export function ActionsSpendPanel({ usage, month, months, onMonth, onClose, onRepair, isRepairing, repairError }: { usage?: ActionsSpend; month: string; months: string[]; onMonth: (month: string) => void; onClose: () => void; onRepair: (action: string) => void; isRepairing: boolean; repairError: string }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const close = useRef<HTMLButtonElement>(null);
  const title = useId();
  const [guidanceAction, setGuidanceAction] = useState("");
  useEffect(() => {
    const element = dialog.current;
    const source = document.activeElement;
    element?.showModal();
    // The window opens on Close, as it did before the month became a button.
    close.current?.focus();
    return () => { element?.close(); if (source instanceof HTMLElement && source.isConnected) source.focus(); };
  }, []);
  const reading = usage?.total && usage.status !== "unavailable" ? { ...usage, total: usage.total } : undefined;
  const which = month === months[0] ? "this month" : "last month";
  const detail = usage ? usage.detail : "Waiting for " + which + "'s GitHub Actions usage.";
  const hasFailedSince = reading?.status === "stale" && Date.parse(reading.checked_at) > Date.parse(reading.read_at);
  const problem = usage?.problem;
  const recovery = usage?.recovery;
  const isPending = recovery?.state === "ready" || recovery?.state === "running";
  const actionLabel = problem?.action === "allow" ? "Allow billing access" : problem?.action === "renew" ? "Renew GitHub login" : problem?.action === "update" ? "Update " + problem.credential : "";
  const isRetryOffered = problem?.action === "retry" || !!problem && guidanceAction === problem.action;
  const recoveryDetail = isPending ? "Complete consent in Command Center on this PC. Billing refreshes once after success."
    : recovery && recovery.state !== "succeeded" ? "Consent was not completed (" + recovery.state + "). You can try again on this PC."
      : recovery && problem ? "Consent finished. The refreshed reading is still unavailable. Use the action shown above." : "";
  return <dialog ref={dialog} className="actions-spend-panel" aria-labelledby={title} onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onClose(); }}>
    <header className="actions-spend-title">
      <div><h2 id={title}>GitHub Actions</h2><ActionsSpendMonthMenu month={month} months={months} onChoose={onMonth} /></div>
      {isRetryOffered && <button type="button" className="icon-button actions-spend-retry" aria-label="Retry" aria-busy={isRepairing} onClick={() => onRepair("retry")}><Icon name="refresh" /></button>}
      <button ref={close} className="icon-button" aria-label="Close Actions spend" onClick={onClose}><Icon name="close" /></button>
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
    </table> : <p className="actions-spend-none">No Actions use {which}.</p>)}
    {detail && <p className="actions-spend-problem">{linkedText(detail)}</p>}
    {problem && <section className="actions-spend-recovery" aria-label="Billing recovery" aria-busy={isRepairing}>
      {problem.account && <p>Account: <strong>{problem.account}</strong></p>}
      {problem.action === "allow" && <p>Billing requires GitHub’s <code>user</code> scope. Review and allow access in the native GitHub consent flow on this PC.</p>}
      {actionLabel && <button type="button" className="primary" disabled={isRepairing || isPending} onClick={() => problem.action === "update" || problem.action === "renew" ? setGuidanceAction(problem.action) : onRepair(problem.action)}>{actionLabel}</button>}
      {guidanceAction === problem.action && <p role="status">{problem.action === "renew"
        ? <>Sign in again with <code>gh auth login</code> in a terminal on this PC{problem.account ? <> as <strong>{problem.account}</strong></> : null}, then Retry. The login stays in the GitHub CLI's own credential store.</>
        : <>Update {problem.credential} for {problem.account || "the selected account"} in the environment that starts Code Goblins. This credential takes precedence over the CLI login. Keep its value on this PC. A running process retains its inherited environment until its launcher supplies the updated value.</>}</p>}
      {recoveryDetail && <p role="status">{recoveryDetail}</p>}
      {isPending && <button type="button" onClick={onClose}>Continue in Command Center</button>}
      <ClickFeedback text={repairError} />
    </section>}
    {reading && <p className="actions-spend-read muted">
      Read {localTime(reading.read_at)}{reading.status === "stale" && <> · <span className="actions-spend-old">Old</span></>}{hasFailedSince && <> · Last attempt {localTime(reading.checked_at)}</>}
    </p>}
  </dialog>;
}
