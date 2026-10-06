import type { Task } from "./types";
import { Icon } from "./Icon";
import { listItem } from "./task-words";

// What a pause or stop kept and what it ended. The panel's status says the
// state and why, so this says neither.
export function LifecycleDetails({ task }: { task: Task }) {
  const record = task.lifecycle;
  if (!record || !record.kept.length && !record.validation_restarts && !record.stopped.length) return null;
  return <section className="lifecycle-details" aria-label="Task lifecycle">
    {record.kept.length > 0 && <>
      <h3>What’s preserved</h3>
      <ul>{record.kept.map((item) => <li key={item}><Icon name="check" /><span>{listItem(item)}</span></li>)}</ul>
    </>}
    {record.validation_restarts && <p className="preservation-notice"><Icon name="shield" />Validation restarts on Resume.</p>}
    {record.stopped.length > 0 && <details><summary>Stopped resources ({record.stopped.length})</summary><ul>{record.stopped.map((item) => <li key={item}>{item}</li>)}</ul></details>}
  </section>;
}
