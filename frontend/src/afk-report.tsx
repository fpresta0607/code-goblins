import { useEffect, useId, useRef } from "react";
import { Avatar } from "./Avatar";
import { Icon, type IconName } from "./Icon";
import { AfkHeldList } from "./afk-held";
import { afkTime, decisionSays, parseAfkReport, safeLink, stillWaiting, yours, type AfkReport } from "./afk";
import { useResource } from "./api";
import { pullRequestLabel } from "./workflow";
import type { Task } from "./types";
import "./afk.css";

// The mark each kind of decision wears in the report.
const MARKS: Record<string, IconName> = { merge: "merge", deploy: "external", migration: "database", install: "download", answer: "comment", other: "check" };

// The report of the last stretch of AFK mode, as one page over the board: how
// many of each thing the CFO did, then what it merged, deployed, migrated,
// installed and answered, each with its link and the evidence it stood on,
// what each goblin finished, what is held for the Overlord and what became of
// it, and what was spent. It opens when AFK mode turns off, and again from the
// CFO's bar.
export function AfkReportPage({ tasks, now, onClose, onCommand }: { tasks: Task[]; now: number; onClose: () => void; onCommand: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId();
  const { data, error, reload } = useResource<AfkReport | null>("/api/afk/report", parseAfkReport);
  useEffect(() => {
    const element = dialog.current;
    const source = document.activeElement;
    element?.showModal();
    return () => { element?.close(); if (source instanceof HTMLElement && source.isConnected) source.focus(); };
  }, []);
  const waiting = data ? stillWaiting(data.held).length : 0;
  const tally: [string, number][] = data ? [...data.sections.map((section): [string, number] => [section.title, section.entries.length]), ["Goblins finished", data.finished.length], ["Held for you", data.held.length]] : [];
  return <dialog ref={dialog} className="question-modal afk-report" aria-labelledby={title} onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onClose(); }}>
    <div className="command-center-heading">
      <Avatar persona="cfo" />
      <div className="afk-report-title">
        <h2 id={title}>AFK report</h2>
        {data && <p>On from {afkTime(data.since, now)} to {afkTime(data.ended, now)}, {data.lasted}.</p>}
        {data && <p className="muted">Turned on from {yours(data.from)}, off from {yours(data.ended_from)}.</p>}
      </div>
      <button className="icon-button" aria-label="Close the report" data-tip="Close" data-tip-align="end" onClick={onClose}><Icon name="close" /></button>
    </div>
    {error && <div className="error-box" role="alert"><p>{error}</p><button className="icon-button" aria-label="Load the report again" data-tip="Retry" data-tip-align="start" onClick={reload}><Icon name="refresh" /></button></div>}
    {data === undefined && !error && <p className="loading" role="status">Loading the report</p>}
    {data === null && <p className="muted">No stretch of AFK mode has ended yet, so there is no report.</p>}
    {data && <>
      <ul className="afk-tally" aria-label="In all">{tally.map(([name, count]) => <li key={name} className={count ? undefined : "none"}>{name} <span className="column-count">{count}</span></li>)}</ul>
      {data.sections.filter((section) => section.entries.length > 0).map((section) => <section key={section.title} aria-label={section.title}>
        <h3>{section.title} <span className="column-count">{section.entries.length}</span></h3>
        <ul className="inbox-list afk-decisions">{section.entries.map((entry) => {
          const says = decisionSays(entry);
          const unmerged = entry.kind === "merge" && entry.outcome !== "merged";
          return <li key={entry.at + entry.kind + entry.what}>
            <span className={"delivery " + (unmerged ? "uncertain" : "succeeded")}><Icon name={unmerged ? "warning" : MARKS[entry.kind] || "check"} /></span>
            <span className="inbox-text">
              <strong>{says.href ? <a href={says.href} target="_blank" rel="noreferrer">{says.text}</a> : says.text}{says.outcome && ": " + says.outcome}</strong>
              <span className="afk-evidence">{says.basis}</span>
            </span>
            <time dateTime={entry.at}>{afkTime(entry.at, now)}</time>
          </li>;
        })}</ul>
      </section>)}
      {data.finished.length > 0 && <section aria-label="Goblins finished">
        <h3>Goblins finished <span className="column-count">{data.finished.length}</span></h3>
        <ul className="inbox-list afk-decisions">{data.finished.map((finish) => <li key={finish.task + finish.pr}>
          <span className="delivery succeeded"><Icon name="pull-request" /></span>
          <span className="inbox-text"><strong>{finish.task}</strong>{safeLink(finish.pr) ? <a href={finish.pr} target="_blank" rel="noreferrer">{pullRequestLabel(finish.pr)}</a> : finish.pr}</span>
          <time dateTime={finish.at}>{afkTime(finish.at, now)}</time>
        </li>)}</ul>
      </section>}
      <section aria-label="Held for you">
        <h3>Held for you <span className="column-count">{data.held.length}</span></h3>
        <AfkHeldList held={data.held} tasks={tasks} />
      </section>
      <section aria-label="Spent">
        <h3>Spent</h3>
        {data.spent.length ? <ul className="inbox-list afk-lines">{data.spent.map((line) => <li key={line}>{line}</li>)}</ul> : <p className="muted">No allowance was read.</p>}
      </section>
      {data.notes.length > 0 && <section aria-label="Not read">
        <h3>Not read</h3>
        <ul className="inbox-list afk-lines">{data.notes.map((note) => <li key={note}>{note}</li>)}</ul>
      </section>}
    </>}
    <div className="afk-report-actions">
      {waiting > 0 && <button onClick={onCommand}>Open Command Center</button>}
      <button className="primary" onClick={onClose}>Back to the board</button>
    </div>
  </dialog>;
}
