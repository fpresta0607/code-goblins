import { useEffect, useId, useRef } from "react";
import { Avatar } from "./Avatar";
import { Disclosure } from "./Disclosure";
import { Icon, type IconName } from "./Icon";
import { ShowMore } from "./ShowMore";
import { AfkHeldList } from "./afk-held";
import { afkHeadline, afkTime, decisionSays, parseAfkReport, safeLink, settled, shownUnder, stillWaiting, switchedBy, type AfkDecision, type AfkReport } from "./afk";
import { AfkSpent } from "./afk-spent";
import { useResource } from "./api";
import { pullRequestLabel } from "./workflow";
import type { Task } from "./types";
import "./afk.css";

// The mark each kind of decision, and a goblin paused at a floor, wears in
// the report.
const MARKS: Record<string, IconName> = { left: "clock", merge: "merge", deploy: "external", migration: "database", install: "download", answer: "comment", other: "check", pause: "pause", strike: "close" };

// What still waits on the Overlord among what the CFO did: a merge word with
// no merge made. A line the CFO struck waits on nobody.
const waitsOnHim = (entry: AfkDecision): boolean => !entry.struck && entry.kind === "merge" && entry.outcome !== "merged";

// The report of the last stretch of AFK mode, as one page over the board. Its
// headline says in plain words how long he was away, how many things wait on
// him and what the CFO merged, deployed, migrated and installed, beside Go
// through them, which steps through what still waits on him in the Command
// Center. Then For you lists what was held or left for him that still waits,
// each as it stands now, and what was settled folds under it. What the CFO did
// follows, a drawer for each heading that holds something, a merge word with
// no merge open since it needs him, then what each goblin finished, what was
// spent and what could not be read. It opens when AFK mode turns off, and
// again from the CFO panel's header.
export function AfkReportPage({ tasks, now, onClose, onGoThrough }: { tasks: Task[]; now: number; onClose: () => void; onGoThrough: (keys: string[]) => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId();
  const { data, error, reload } = useResource<AfkReport | null>("/api/afk/report", parseAfkReport);
  useEffect(() => {
    const element = dialog.current;
    const source = document.activeElement;
    element?.showModal();
    return () => { element?.close(); if (source instanceof HTMLElement && source.isConnected) source.focus(); };
  }, []);
  const waiting = data ? stillWaiting(data.held) : [];
  const done = data ? settled(data.held) : [];
  const spent = data ? data.spent.filter(shownUnder) : [];
  const headline = data ? afkHeadline(data, now) : null;
  return <dialog ref={dialog} className="question-modal afk-report" aria-labelledby={title} onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onClose(); }}>
    <div className="command-center-heading">
      <Avatar persona="cfo" />
      <div className="afk-report-title">
        <h2 id={title}>AFK report</h2>
        {headline && <p className="afk-headline">{headline.away} <strong>{headline.waiting}</strong>{headline.did && " " + headline.did}</p>}
        {data && <p className="muted">Turned on {switchedBy(data.from, data.asked)}, off {switchedBy(data.ended_from, data.ended_asked)}.</p>}
      </div>
      {waiting.length > 0 && <button className="primary go-through" onClick={() => onGoThrough(waiting.map((one) => one.item))}><Icon name="next" />Go through them</button>}
      <button className="icon-button" aria-label="Close the report" data-tip="Close" onClick={onClose}><Icon name="close" /></button>
    </div>
    {error && <button className="icon-button raised" aria-label="Load the report again" data-tip="Retry" onClick={reload}><Icon name="refresh" /></button>}
    {data === undefined && !error && <p className="loading" role="status">Loading the report</p>}
    {data === null && <p className="muted">No report yet.</p>}
    {data && <>
      {waiting.length > 0 && <section aria-label="For you">
        <h3>For you <span className="column-count">{waiting.length}</span></h3>
        <AfkHeldList held={waiting} tasks={tasks} />
      </section>}
      {done.length > 0 && <section aria-label="Settled">
        <Disclosure kind="afk-drawer" title={<>Settled <span className="column-count">{done.length}</span></>}><AfkHeldList held={done} tasks={tasks} /></Disclosure>
      </section>}
      {(data.sections.length > 0 || data.finished.length > 0) && <div className="afk-did" role="group" aria-label="What the CFO did">
        <h3>What the CFO did</h3>
        {data.sections.map((section) => {
          const heading = <>{section.title} <span className="column-count">{section.entries.length}</span></>;
          const list = <ul className="inbox-list afk-decisions">{section.entries.map((entry) => {
            const says = decisionSays(entry);
            const unmerged = entry.kind === "merge" && entry.outcome !== "merged";
            const said = <>{says.href ? <a href={says.href} target="_blank" rel="noreferrer">{says.text}</a> : says.text}{says.outcome && ": " + says.outcome}</>;
            return <li key={entry.at + entry.kind + entry.what} className={entry.struck ? "struck" : undefined}>
              <span className={"delivery " + (waitsOnHim(entry) ? "uncertain" : "succeeded")}><Icon name={entry.struck ? "close" : unmerged ? "warning" : MARKS[entry.kind] || "check"} /></span>
              <div className="inbox-text">
                <strong>{entry.struck ? <s>{said}</s> : said}</strong>
                <ShowMore text={says.basis} className="afk-evidence" />
              </div>
              <time dateTime={entry.at}>{afkTime(entry.at, now)}</time>
            </li>;
          })}</ul>;
          return <section key={section.title} aria-label={section.title}>
            {section.entries.some(waitsOnHim) ? <><h4>{heading}</h4>{list}</> : <Disclosure kind="afk-drawer" title={heading}>{list}</Disclosure>}
          </section>;
        })}
        {data.finished.length > 0 && <section aria-label="Goblins finished">
          <Disclosure kind="afk-drawer" title={<>Goblins finished <span className="column-count">{data.finished.length}</span></>}>
            <ul className="inbox-list afk-decisions">{data.finished.map((finish) => <li key={finish.task + finish.pr}>
              <span className="delivery succeeded"><Icon name="pull-request" /></span>
              <span className="inbox-text"><strong>{finish.task}</strong>{safeLink(finish.pr) ? <a href={finish.pr} target="_blank" rel="noreferrer">{pullRequestLabel(finish.pr)}</a> : finish.pr}</span>
              <time dateTime={finish.at}>{afkTime(finish.at, now)}</time>
            </li>)}</ul>
          </Disclosure>
        </section>}
      </div>}
      {(spent.length > 0 || data.disk) && <section aria-label="Spent">
        <h3>Spent <span className="afk-spent-legend"><i className="before" />Before AFK <i className="used" />While AFK <i className="left" />Left</span></h3>
        <AfkSpent spent={spent} disk={data.disk} />
      </section>}
      {data.notes.length > 0 && <section aria-label="Not read">
        <h3>Not read</h3>
        <ul className="inbox-list afk-lines">{data.notes.map((note) => <li key={note}>{note}</li>)}</ul>
      </section>}
    </>}
  </dialog>;
}
