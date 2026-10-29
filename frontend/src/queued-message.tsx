import { memo, useState } from "react";
import { message, reportToCfo, request } from "./api";
import { Icon } from "./Icon";
import { messageState } from "./messages";
import type { Action } from "./types";

// One message the board holds for delivery, shown where it waits: its words,
// what it waits on in the supervisor's own plain reason, and the three things
// he can do with it (the Overlord, 2026-10-26: "Does this seem to be a way to
// like send now or edit the cue messages either or delete them?").
// Send now clears its wait so the next safe moment types it; Edit changes
// what will be sent; Cancel takes it back unsent, by its own row, so it never
// reaches the harness. pane draws it in the
// conversation, queue in the message box's own list, and both offer the same
// acts.
export const QueuedMessage = memo(function QueuedMessage({ action, instance, name, variant, onDeleted }: QueuedProps) {
  const [isEditing, setEditing] = useState(false);
  const [draft, setDraft] = useState(action.text);
  const [problem, setProblem] = useState("");
  const [isSending, setSending] = useState(false);
  const post = (body: object) => request("/api/actions", undefined, {
    method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify(body),
  });
  const act = async (body: object, after?: () => void) => {
    if (isSending) return;
    setSending(true);
    setProblem("");
    try { await post({ id: crypto.randomUUID(), task_id: action.task_id, target: action.id, ...body }); after?.(); }
    catch (error: unknown) { setProblem(message(error)); reportToCfo("a queued message to " + name, message(error)); }
    finally { setSending(false); }
  };
  // A message is typed only once it awaits the turn; one the board is
  // looking at again, running with nothing awaited, still waits.
  const isWaiting = action.status === "queued" || action.status === "running" && !action.awaiting;
  const isTyped = !!action.awaiting;
  const isPending = isWaiting || isTyped;
  const state = isWaiting ? (variant === "queue" ? "Queued" : "Waiting") : isTyped ? "Typed, awaiting the turn" : messageState(action);
  const reason = isPending ? action.message : "";
  return <div className={"queued-message " + variant} data-queued={action.id}>
    {isEditing ? <div className="queued-edit">
      <textarea aria-label={"Edit message: " + action.text} value={draft} rows={2} onChange={(event) => setDraft(event.target.value)} />
      <div className="queued-actions">
        <button type="button" className="labelled-button" disabled={isSending || !draft.trim()} onClick={() => void act({ kind: "message_edit", text: draft }, () => setEditing(false))}>Save</button>
        <button type="button" className="labelled-button" disabled={isSending} onClick={() => { setEditing(false); setDraft(action.text); }}>Cancel</button>
      </div>
    </div> : <>
      {variant === "pane" && <div className="message-bubble queued-bubble">{action.text}</div>}
      {variant === "queue" && <span className="composer-sent-text">{action.text}</span>}
      {variant === "pane" ? <p className="queued-state" aria-label={reason || state} data-tip={reason || state}><Icon name="clock" /></p>
        : <p className="queued-line" data-tip={reason}><strong>{state}</strong></p>}
      {isPending && <div className="queued-actions">
        {isWaiting && <button type="button" className="icon-button" aria-label="Send now" disabled={isSending || !action.can_send_now}
          data-tip={action.can_send_now ? "Send now · type it at the next safe moment" : "Send now · " + (action.send_now_why || "It cannot be typed into its session now.")}
          onClick={() => void act({ kind: "message_now" })}><Icon name="arrow-up" /></button>}
        {isWaiting && <button type="button" className="icon-button" aria-label="Edit" data-tip="Edit · change the words before delivery" disabled={isSending}
          onClick={() => { setDraft(action.text); setEditing(true); }}><Icon name="edit" /></button>}
        <button type="button" className="icon-button" aria-label="Cancel" data-tip="Cancel · take it back unsent" disabled={isSending}
          onClick={() => void act({ kind: "message_withdraw", text: action.text }, onDeleted)}><Icon name="close" /></button>
      </div>}
      {problem && <p className="queued-problem" role="status">{problem}</p>}
    </>}
  </div>;
}, isSameQueued);

type QueuedProps = { action: Action; instance: string; name: string; variant: "pane" | "queue"; onDeleted?: () => void };

// Each snapshot parses every action anew, so a comparison by identity drew
// every queued message again on every snapshot, 315 times in 120 s of a live
// board. It is drawn again only when what it shows or sends changes.
function isSameQueued(prior: QueuedProps, next: QueuedProps): boolean {
  const [was, is] = [prior.action, next.action];
  return was.id === is.id && was.task_id === is.task_id && was.text === is.text && was.status === is.status && was.message === is.message
    && !!was.awaiting === !!is.awaiting && was.can_send_now === is.can_send_now && was.send_now_why === is.send_now_why
    && prior.instance === next.instance && prior.name === next.name && prior.variant === next.variant && prior.onDeleted === next.onDeleted;
}
