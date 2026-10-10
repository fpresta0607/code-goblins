import { useEffect, useRef, useState } from "react";
import { object, string, type Snapshot, type Task } from "./types";
import { message, request } from "./api";
import { Icon } from "./Icon";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import { taskName } from "./task-words";

// A queued task's line under its goblin's name, edited in place, as the
// Overlord asked on 2026-10-08: the small pencil after it, or a click on its
// text, turns it into a box holding the task's title and detail. The pencil
// is the line's one button (2026-10-10, "I get editing pencil a little
// icon"): a caret only opens or closes. Enter or the check saves
// it, Shift+Enter adds a line, and Escape or the cross puts the line back. A
// refused save keeps the box and its text and says why beside it for a moment.
export function TaskAdjustment({ task, snapshot }: { task: Task; snapshot: Snapshot }) {
  // saved is this panel's last save, which stands for the task until the
  // snapshot moves past the revision it was saved over.
  const [saved, setSaved] = useState<{ over: string; revision: string; text: string } | null>(null);
  const pending = saved?.over === task.queue_revision ? saved : null;
  const current = pending || { revision: task.queue_revision, text: [task.title, task.detail].filter(Boolean).join("\n\n") };
  const line = taskName(pending ? { id: task.id, title: pending.text.trim().split("\n")[0] } : task);
  // draft is the open box's text and the revision it opened on, so a task
  // changed elsewhere meanwhile is refused rather than overwritten.
  const [draft, setDraft] = useState<{ revision: string; text: string } | null>(null);
  const [isSending, setSending] = useState(false);
  const [feedback, showFeedback] = useClickFeedback();
  const attempt = useRef<{ payload: string; operation: string } | null>(null);
  const box = useRef<HTMLTextAreaElement>(null);
  const pencil = useRef<HTMLButtonElement>(null);
  const hasOpened = useRef(false);
  const isOpen = draft !== null;
  // The box opens with the cursor at the end of its text, and closing it
  // gives the focus back to the pencil.
  useEffect(() => {
    const element = box.current;
    if (isOpen && element) {
      hasOpened.current = true;
      element.focus();
      element.setSelectionRange(element.value.length, element.value.length);
    } else if (!isOpen && hasOpened.current) pencil.current?.focus();
  }, [isOpen]);
  const cancel = () => {
    if (isSending) return;
    setDraft(null);
    showFeedback("");
  };
  const save = async () => {
    if (!draft || isSending || task.starting || !draft.text.trim()) return;
    setSending(true); showFeedback("");
    const payload = JSON.stringify({ task: task.id, revision: draft.revision, text: draft.text, action: "save" });
    if (attempt.current?.payload !== payload) attempt.current = { payload, operation: crypto.randomUUID() };
    try {
      const response = object(await request("/api/tasks/adjust", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ task: task.id, revision: draft.revision, text: draft.text, action: "save", operation: attempt.current.operation }) }));
      setSaved({ over: task.queue_revision, revision: string(response.revision), text: draft.text });
      setDraft(null);
      attempt.current = null;
    } catch (failure: unknown) { showFeedback(message(failure)); }
    finally { setSending(false); }
  };
  if (!draft) return <div className="panel-goblin-task task-line">
    <span onClick={task.starting ? undefined : () => setDraft(current)}>{line}</span>
    <button ref={pencil} className="icon-button" aria-label="Edit task" data-tip="Edit task" disabled={task.starting} onClick={() => setDraft(current)}><Icon name="edit" /></button>
  </div>;
  return <>
    <div className="task-edit" role="group" aria-label="Edit the task" aria-busy={isSending}>
      <textarea ref={box} aria-label="Task title and detail" value={draft.text} maxLength={8000} rows={1} readOnly={isSending}
        onChange={(event) => setDraft({ ...draft, text: event.target.value })}
        onKeyDown={(event) => {
          if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); cancel(); }
          if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); void save(); }
        }} />
      <span className="task-edit-actions">
        <button className="icon-button" aria-label="Save task" data-tip="Save" disabled={isSending || task.starting || !draft.text.trim()} onClick={() => void save()}>{isSending ? <span className="card-start-spinner" /> : <Icon name="check" />}</button>
        <button className="icon-button" aria-label="Cancel edit" data-tip="Cancel" disabled={isSending} onClick={cancel}><Icon name="close" /></button>
      </span>
    </div>
    <ClickFeedback text={feedback} />
  </>;
}
