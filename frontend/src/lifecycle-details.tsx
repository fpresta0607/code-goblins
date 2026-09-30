import type { Task } from "./types";
import { Icon } from "./Icon";
import { statusText } from "./workflow";

export function LifecycleDetails({ task }: { task: Task }) {
  const record = task.lifecycle;
  const teardown = task.teardown.length > 0 ? <p className="windows-teardown" role="status">Finishing Windows teardown: {task.teardown.join(", ")}</p> : null;
  if (!record) return teardown;
  return <section className="lifecycle-details" aria-label="Task lifecycle">
    <p className={"plain-status phase-" + record.phase}><span className="status-dot" />{record.phase === "running" ? "Resumed" : statusText(record.phase)}{record.at && <time dateTime={record.at}> at {new Date(record.at).toLocaleString()}</time>}</p>
    {task.reason && <p>{task.reason}</p>}
    {teardown}
    <h3>What’s preserved</h3>
    <ul>{record.kept.map((item) => <li key={item}><Icon name="check" /><span>{item}</span></li>)}</ul>
    {record.action === "pause" && <p>{record.handoff_saved ? "Session and handoff saved." : "No new handoff was saved before the stopping-point deadline."}</p>}
    {record.validation_restarts && <p className="preservation-notice"><Icon name="shield" />Validation restarts on Resume.</p>}
    {record.stopped.length > 0 && <details><summary>Stopped resources ({record.stopped.length})</summary><ul>{record.stopped.map((item) => <li key={item}>{item}</li>)}</ul></details>}
    {record.problems.length > 0 && <div className="task-action-problem" role="status"><strong>Needs attention</strong><ul>{record.problems.map((item) => <li key={item}>{item}</li>)}</ul></div>}
  </section>;
}
