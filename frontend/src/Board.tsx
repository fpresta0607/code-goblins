import type { BoardActivity, Snapshot, Task } from "./types";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { CfoPin } from "./CfoPin";
import { asksOverlord, nodeStatus, personaFor, pullRequestLabel, safePullRequest, taskColumn, waitingTarget } from "./workflow";

export function Board({ snapshot, selected, onSelect, onTerminal, onOpenCfo, presentations }: {
  presentations:BoardActivity[];
  snapshot: Snapshot; selected?: string;
  onSelect: (task: Task, source: HTMLElement) => void;
  onTerminal: (task: Task, source: HTMLElement) => void;
  onOpenCfo: (source: HTMLElement) => void;
}) {
  return <section className="task-board" aria-label="Task board">
    <CfoPin snapshot={snapshot} onOpen={onOpenCfo} />
    {(["Tasks", "In progress", "Completed"] as const).map((column) => {
      const tasks = snapshot.tasks.filter((task) => taskColumn(task) === column);
      return <section key={column} className="board-column" aria-label={column}>
        <h2>{column}<span className="column-count">{tasks.length}</span></h2>
        <div className="task-cards">
          {tasks.map((task) => {
            const pr = safePullRequest(task.pr), asking = asksOverlord(snapshot, task.id), awaited = waitingTarget(snapshot, task);
            const content = <>
              <Avatar persona={personaFor(task)} />
              <span className="card-copy">{presentations.some(event=>event.task_id===task.id) && <span className="browser-indicator">Browser active</span>}<strong>{task.title || task.id}</strong>{task.project && <span className="project-label">{task.project}</span>}
                <span className={"plain-status phase-" + task.phase}><span className="status-dot" />{nodeStatus({ id: task.id, title: task.title, task, relation: "" }, asking)}</span>
                {task.activity && <span className="card-activity" title={task.activity}>{task.activity}</span>}
              </span>
            </>;
            // Completed history has no live worktree to review, so its card is
            // its pull request.
            if (task.archived) return pr
              ? <a key={task.id} className="task-card history" href={pr} target="_blank" rel="noreferrer">{content}<span className="card-pr"><Icon name="pull-request" />{pullRequestLabel(pr)}</span></a>
              : <div key={task.id} className="task-card history">{content}</div>;
            return <div key={task.id} className={"task-card-shell" + (pr || awaited ? " has-pr" : "")}>
              <button className={"task-card" + (selected === task.id ? " selected" : "")}
                aria-pressed={selected === task.id} onClick={(event) => onSelect(task, event.currentTarget)}>{content}</button>
              {!!task.generation && <button className="icon-button raised card-terminal" aria-label={"Open the terminal of " + (task.title || task.id)} data-tip="Terminal" data-tip-align="end" onClick={(event) => onTerminal(task, event.currentTarget)}><Icon name="terminal" /></button>}
              {awaited && <button className="card-waiting" aria-label={"Open " + (awaited.title || awaited.id) + ", which this goblin is waiting on"} data-tip={"Open " + (awaited.title || awaited.id)} data-tip-align="start" onClick={(event) => onSelect(awaited, event.currentTarget)}><Icon name="next" />{awaited.id}</button>}
              {pr && <a className="card-pr" href={pr} target="_blank" rel="noreferrer" aria-label={"Open pull request " + pullRequestLabel(pr)}><Icon name="pull-request" />{pullRequestLabel(pr)}</a>}
            </div>;
          })}
          {!tasks.length && <p className="column-empty">{column === "Tasks" ? "Nothing queued" : column === "Completed" ? "Verified work will appear here" : "No work in progress"}</p>}
        </div>
      </section>;
    })}
  </section>;
}
