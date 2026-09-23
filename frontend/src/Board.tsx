import type { BoardActivity, Snapshot, Task } from "./types";
import { Avatar } from "./Avatar";
import { nodeStatus, personaFor, taskColumn } from "./workflow";

export function Board({ snapshot, selected, onSelect, presentations }: {
  presentations:BoardActivity[];
  snapshot: Snapshot; selected?: string;
  onSelect: (task: Task, source: HTMLElement) => void;
}) {
  return <section className="task-board" aria-label="Task board">
    {(["Tasks", "In progress", "Completed"] as const).map((column) => {
      const tasks = snapshot.tasks.filter((task) => taskColumn(task) === column);
      return <section key={column} className="board-column" aria-label={column}>
        <h2>{column}<span className="column-count">{tasks.length}</span></h2>
        <div className="task-cards">
          {tasks.map((task) => <button key={task.id} className={"task-card" + (selected === task.id ? " selected" : "")}
            aria-pressed={selected === task.id} onClick={(event) => onSelect(task, event.currentTarget)}>
            <Avatar persona={personaFor(task)} />
            <span className="card-copy">{presentations.some(event=>event.task_id===task.id) && <span className="browser-indicator">Browser active</span>}<strong>{task.title || task.id}</strong>{task.project && <span className="project-label">{task.project}</span>}
              <span className={"plain-status phase-" + task.phase}><span className="status-dot" />{nodeStatus({ id: task.id, title: task.title, task, relation: "" })}</span>
            </span>
          </button>)}
          {!tasks.length && <p className="column-empty">{column === "Tasks" ? "Nothing queued" : column === "Completed" ? "Verified work will appear here" : "No work in progress"}</p>}
        </div>
      </section>;
    })}
  </section>;
}
