import { useId, useRef, useState } from "react";
import { object, string, type Snapshot, type Task } from "./types";
import { message, request } from "./api";
import { Icon } from "./Icon";

export function TaskAdjustment({ task, snapshot }: { task: Task; snapshot: Snapshot }) {
  const label = useId();
  const [text, setText] = useState([task.title, task.detail].filter(Boolean).join("\n\n"));
  const [revision, setRevision] = useState(task.queue_revision);
  const [isSending, setSending] = useState(false);
  const [outcome, setOutcome] = useState("");
  const [error, setError] = useState("");
  const attempt = useRef<{ payload: string; operation: string } | null>(null);
  const save = async () => {
    if (isSending || !text.trim()) return;
    setSending(true); setError(""); setOutcome("");
    const payload = JSON.stringify({ task: task.id, revision, text, action: "save" });
    if (attempt.current?.payload !== payload) attempt.current = { payload, operation: crypto.randomUUID() };
    try {
      const response = object(await request("/api/tasks/adjust", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ task: task.id, revision, text, action: "save", operation: attempt.current.operation }) }));
      setRevision(string(response.revision));
      setOutcome("Task changes saved.");
      attempt.current = null;
    } catch (failure: unknown) { setError(message(failure)); }
    finally { setSending(false); }
  };
  return <section className="task-adjustment" aria-labelledby={label}>
    <h3 id={label}>Adjust this task</h3>
    <label className="sr-only" htmlFor={label + "-text"}>Task title and detail</label>
    <textarea id={label + "-text"} value={text} onChange={(event) => setText(event.target.value)} maxLength={8000} rows={6} disabled={isSending || task.starting} />
    <p className="muted">First line is the title. Following lines are the detail.</p>
    <div className="task-controls">
      <button className="labelled-button" disabled={isSending || task.starting || !text.trim()} onClick={() => void save()}><Icon name="save" />Save changes</button>
    </div>
    <p className="muted">Save updates the task and its brief.</p>
    {(isSending || outcome) && <p role="status">{isSending ? "Saving..." : outcome}</p>}
    {error && <p className="task-action-problem" role="alert">{error}</p>}
    {task.notes.map((note, index) => <div className="task-note" key={index}><Icon name="comment" /><div><strong>Note sent to CFO · awaiting reply</strong><p>{note}</p></div></div>)}
  </section>;
}
