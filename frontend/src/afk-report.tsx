import { useEffect, useId, useRef } from "react";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { AfkRecap } from "./afk-recap";
import { afkHeadline, parseAfkReport, stillWaiting, switchedBy, type AfkReport } from "./afk";
import { useResource } from "./api";
import type { Task } from "./types";
import "./afk.css";

// The recap of the last stretch of AFK mode, one page over the board laid out
// as a Scrawl page is: a headline in plain words, how long he was away, how
// many things need him and what the CFO did, then Done, each heading with
// what was done under it in plain words and its link, then Decided for you,
// each question the CFO answered for him and for the goblins with its answer
// and Change, which opens it in History, and last, only when there are any,
// the few lines that need him: a step only he could take, and an item that
// still waits on him in the Command Center, which Go through them steps
// through. What each line stood on is one tap away under its Evidence. It
// opens when AFK mode turns off, and again from the CFO panel's header.
export function AfkReportPage({ tasks, now, onClose, onGoThrough, onChange }: { tasks: Task[]; now: number; onClose: () => void; onGoThrough: (keys: string[]) => void; onChange: (key: string) => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId();
  const { data, error, reload } = useResource<AfkReport | null>("/api/afk/report", parseAfkReport);
  useEffect(() => {
    const element = dialog.current;
    const source = document.activeElement;
    element?.showModal();
    return () => { element?.close(); if (source instanceof HTMLElement && source.isConnected) source.focus(); };
  }, []);
  const headline = data ? afkHeadline(data, now) : null;
  const waiting = data ? stillWaiting(data.held) : [];
  return <dialog ref={dialog} className="question-modal afk-report" aria-labelledby={title} onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onClose(); }}>
    <div className="command-center-heading">
      <Avatar persona="cfo" />
      <div className="afk-report-title">
        <h2 id={title}>AFK recap</h2>
        {headline && <p className="afk-headline">{headline.away} <strong>{headline.needs}</strong>{headline.did && " " + headline.did}</p>}
        {data && <p className="muted">Turned on {switchedBy(data.from, data.asked)}, off {switchedBy(data.ended_from, data.ended_asked)}.</p>}
      </div>
      {waiting.length > 0 && <button className="primary go-through" onClick={() => onGoThrough(waiting.map((one) => one.item))}><Icon name="next" />Go through them</button>}
      <button className="icon-button" aria-label="Close the recap" data-tip="Close" onClick={onClose}><Icon name="close" /></button>
    </div>
    {error && <button className="icon-button raised" aria-label="Load the recap again" data-tip="Retry" onClick={reload}><Icon name="refresh" /></button>}
    {data === undefined && !error && <p className="loading" role="status">Loading the recap</p>}
    {data === null && <p className="muted">No recap yet.</p>}
    {data && <AfkRecap report={data} tasks={tasks} now={now} onGoThrough={onGoThrough} onChange={onChange} />}
  </dialog>;
}
