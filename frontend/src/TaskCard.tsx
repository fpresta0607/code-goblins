import { useState, type SyntheticEvent } from "react";
import type { BoardActivity, Snapshot, Task } from "./types";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { clockText } from "./cards";
import { asksOverlord, nodeStatus, personaFor, pullRequestLabel, safePullRequest, taskColumn, waitingTarget } from "./workflow";

// A task's card on the board: a short title of at most two lines, then one
// muted line with the repo and the status, and a quiet clock of how long its
// session has run or how long it has waited; the goblin's own words stay in
// its panel. rank, when the card sits in an ordered list, is read out with it.
export function TaskCard({ task, snapshot, selected, presentations, now, rank, onSelect, onTerminal }: {
  task: Task; snapshot: Snapshot; selected: boolean; presentations: BoardActivity[]; now: number; rank?: string;
  onSelect: (task: Task, source: HTMLElement) => void;
  onTerminal: (task: Task, source: HTMLElement) => void;
}) {
  // A title cut off after two lines shows in full in a tip on hover or focus.
  const [clipped, setClipped] = useState(false);
  const measure = (event: SyntheticEvent<HTMLElement>) => {
    const title = event.currentTarget.querySelector<HTMLElement>(".card-title");
    setClipped(!!title && title.scrollHeight > title.clientHeight + 1);
  };
  const name = task.title || task.id;
  const tip = clipped ? { "data-tip": name, "data-tip-align": "start" } : {};
  const pr = safePullRequest(task.pr), asking = asksOverlord(snapshot, task.id), awaited = waitingTarget(snapshot, task);
  const waiting = task.phase === "queued";
  const clock = taskColumn(task) === "Completed" ? "" : clockText(task.since, now, waiting ? "waiting" : "running");
  const content = <>
    <Avatar persona={personaFor(task)} />
    <span className="card-copy">{presentations.some((event) => event.task_id === task.id) && <span className="browser-indicator">Browser active</span>}<strong className="card-title">{name}</strong>
      {rank && <span className="sr-only">, {rank}</span>}
      <span className="card-meta">{task.project && <><span className="card-repo">{task.project}</span><span className="card-sep" aria-hidden="true">·</span></>}<span className={"plain-status phase-" + task.phase}><span className="status-dot" /><span className="card-status-text">{nodeStatus({ id: task.id, title: task.title, task, relation: "" }, asking)}</span></span></span>
      {clock && <span className="card-clock"><Icon name="clock" /><span className="sr-only">{waiting ? "Waiting for" : "Running for"} </span>{clock}</span>}
    </span>
  </>;
  // Completed history has no live worktree to review, so its card is its
  // pull request.
  if (task.archived) return pr
    ? <a className="task-card history" href={pr} target="_blank" rel="noreferrer" onPointerEnter={measure} onFocus={measure} {...tip}>{content}<span className="card-pr"><Icon name="pull-request" />{pullRequestLabel(pr)}</span></a>
    : <div className="task-card history" onPointerEnter={measure} {...tip}>{content}</div>;
  return <div className={"task-card-shell" + (pr || awaited ? " has-pr" : "") + (clock ? " has-clock" : "")}>
    <button className={"task-card" + (selected ? " selected" : "")}
      aria-pressed={selected} onClick={(event) => onSelect(task, event.currentTarget)} onPointerEnter={measure} onFocus={measure} {...tip}>{content}</button>
    {!!task.generation && <button className="icon-button raised card-terminal" aria-label={"Open the terminal of " + name} data-tip="Terminal" data-tip-align="end" onClick={(event) => onTerminal(task, event.currentTarget)}><Icon name="terminal" /></button>}
    {awaited && <button className="card-waiting" aria-label={"Open " + (awaited.title || awaited.id) + ", which this goblin is waiting on"} data-tip={"Open " + (awaited.title || awaited.id)} data-tip-align="start" onClick={(event) => onSelect(awaited, event.currentTarget)}><Icon name="next" />{awaited.id}</button>}
    {pr && <a className="card-pr" href={pr} target="_blank" rel="noreferrer" aria-label={"Open pull request " + pullRequestLabel(pr)}><Icon name="pull-request" />{pullRequestLabel(pr)}</a>}
  </div>;
}
