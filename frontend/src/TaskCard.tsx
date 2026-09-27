import type { BoardActivity, Snapshot, Task } from "./types";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { asksOverlord, nodeStatus, personaFor, pullRequestLabel, safePullRequest, waitingTarget } from "./workflow";

// A task's card on the board: a short title of at most two lines, then one
// muted line with the repo and the status; the goblin's own words stay in its
// panel. rank, when the card sits in an ordered list, is read out with it.
export function TaskCard({ task, snapshot, selected, presentations, rank, onSelect, onTerminal }: {
  task: Task; snapshot: Snapshot; selected: boolean; presentations: BoardActivity[]; rank?: string;
  onSelect: (task: Task, source: HTMLElement) => void;
  onTerminal: (task: Task, source: HTMLElement) => void;
}) {
  const pr = safePullRequest(task.pr), asking = asksOverlord(snapshot, task.id), awaited = waitingTarget(snapshot, task);
  const content = <>
    <Avatar persona={personaFor(task)} />
    <span className="card-copy">{presentations.some((event) => event.task_id === task.id) && <span className="browser-indicator">Browser active</span>}<strong className="card-title">{task.title || task.id}</strong>
      {rank && <span className="sr-only">, {rank}</span>}
      <span className="card-meta">{task.project && <><span className="card-repo">{task.project}</span><span className="card-sep" aria-hidden="true">·</span></>}<span className={"plain-status phase-" + task.phase}><span className="status-dot" /><span className="card-status-text">{nodeStatus({ id: task.id, title: task.title, task, relation: "" }, asking)}</span></span></span>
    </span>
  </>;
  // Completed history has no live worktree to review, so its card is its
  // pull request.
  if (task.archived) return pr
    ? <a className="task-card history" href={pr} target="_blank" rel="noreferrer">{content}<span className="card-pr"><Icon name="pull-request" />{pullRequestLabel(pr)}</span></a>
    : <div className="task-card history">{content}</div>;
  return <div className={"task-card-shell" + (pr || awaited ? " has-pr" : "")}>
    <button className={"task-card" + (selected ? " selected" : "")}
      aria-pressed={selected} onClick={(event) => onSelect(task, event.currentTarget)}>{content}</button>
    {!!task.generation && <button className="icon-button raised card-terminal" aria-label={"Open the terminal of " + (task.title || task.id)} data-tip="Terminal" data-tip-align="end" onClick={(event) => onTerminal(task, event.currentTarget)}><Icon name="terminal" /></button>}
    {awaited && <button className="card-waiting" aria-label={"Open " + (awaited.title || awaited.id) + ", which this goblin is waiting on"} data-tip={"Open " + (awaited.title || awaited.id)} data-tip-align="start" onClick={(event) => onSelect(awaited, event.currentTarget)}><Icon name="next" />{awaited.id}</button>}
    {pr && <a className="card-pr" href={pr} target="_blank" rel="noreferrer" aria-label={"Open pull request " + pullRequestLabel(pr)}><Icon name="pull-request" />{pullRequestLabel(pr)}</a>}
  </div>;
}
