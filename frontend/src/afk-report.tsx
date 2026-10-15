import { useEffect, useId, useRef } from "react";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { AfkRecap } from "./afk-recap";
import { afkHeadline, parseAfkReport, switchedBy, type AfkReport } from "./afk";
import { useResource } from "./api";
import type { Task } from "./types";
import "./afk.css";

// The recap of the last stretch of AFK mode, one page over the board laid out
// as a Scrawl page is: a headline in plain words, how long he was away, how
// many things need him and what the CFO did, then, first and only when there
// are any, what needs him: how many cards wait in the Command Center, one for
// each step only he can take and each item that still waits on him, with Open
// Command Center, which opens only them, and a line for any step that has no
// card, then Done, each heading with what was done
// under it in plain words and its link, and last Decided for you, each
// question the CFO answered for him and for the goblins with its answer and
// Change, which opens it in History. What each line stood on is one tap away
// under its Evidence. It opens when AFK mode turns off, and again from the CFO panel's header, and
// ends on a button that closes it.
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
  return <dialog ref={dialog} className="question-modal afk-report" aria-labelledby={title} onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onClose(); }}>
    <div className="command-center-heading">
      <Avatar persona="cfo" />
      <div className="afk-report-title">
        <h2 id={title}>AFK recap</h2>
        {headline && <p className="afk-headline">{headline.away} <strong>{headline.needs}</strong>{headline.did && " " + headline.did}</p>}
        {data && <p className="muted">Turned on {switchedBy(data.from, data.asked)}, off {switchedBy(data.ended_from, data.ended_asked)}.</p>}
      </div>
      <button className="icon-button" aria-label="Close the recap" data-tip="Close" onClick={onClose}><Icon name="close" /></button>
    </div>
    {error && <button className="icon-button raised" aria-label="Load the recap again" data-tip="Retry" onClick={reload}><Icon name="refresh" /></button>}
    {data === undefined && !error && <p className="loading" role="status">Loading the recap</p>}
    {data === null && <p className="muted">No recap yet.</p>}
    {data && <AfkRecap report={data} tasks={tasks} now={now} onGoThrough={onGoThrough} onChange={onChange} />}
    {data !== undefined && <div className="afk-report-end"><button className="primary" onClick={onClose}>Close</button></div>}
  </dialog>;
}
