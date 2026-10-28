import { useState } from "react";
import { message, reportToCfo, request } from "./api";
import { Icon } from "./Icon";
import { keptFileAddress, keptPictures } from "./kept-files";
import { MessagePictures } from "./message-pictures";
import { messageState } from "./messages";
import type { Action } from "./types";

// TAKE_BACK_MESSAGE names the event Edit sends with a queued message's ID, for
// the box of its session to take it back (composer.tsx).
export const TAKE_BACK_MESSAGE = "cfo-take-back-message";

// One message the board holds for delivery, shown where it waits: its words,
// what it waits on in the supervisor's own plain reason, and the three things
// he can do with it (the Overlord, 2026-10-26: "Does this seem to be a way to
// like send now or edit the cue messages either or delete them?").
// Send now clears its wait so the next safe moment types it; Edit takes it
// back off the queue into the box, as Up does, to change and send again (the
// Overlord, 2026-10-28: no little edit window inside the chat); Cancel takes
// it back unsent, by its own row, so it never reaches the harness. pane draws
// it under the conversation, the pictures the box kept for it above its
// bubble in place of their paths as his sent message shows them, queue in the
// message box's own list, and both offer the same acts. In pane its outline
// says it is not sent yet, with no clock beside it, and what it waits on is
// its tip and its description (the Overlord, 2026-10-28: "You don't need the
// waiting clock, the outline already shows that it's queued").
export function QueuedMessage({ action, instance, name, variant, onDeleted }: {
  action: Action; instance: string; name: string; variant: "pane" | "queue"; onDeleted?: () => void;
}) {
  const [problem, setProblem] = useState("");
  const [isSending, setSending] = useState(false);
  // What he already asked of this message, so a second press asks nothing
  // more (the Overlord, 2026-10-28: "lack of indmepotent click on teh
  // queue"): Send now stays pressed until the message leaves the queue, and a
  // Cancel taken takes the actions away. A Cancel's request ID is the
  // message's own, so one sent again from another screen withdraws nothing
  // twice.
  const [asked, setAsked] = useState<"" | "now" | "withdrawn">("");
  const post = (body: object) => request("/api/actions", undefined, {
    method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify(body),
  });
  const act = async (body: { kind: string; text?: string }, after: () => void) => {
    if (isSending) return;
    setSending(true);
    setProblem("");
    const id = body.kind === "message_withdraw" ? "withdraw-" + action.id : crypto.randomUUID();
    try { await post({ id, task_id: action.task_id, target: action.id, ...body }); after(); }
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
  const kept = keptPictures(action.text);
  return <div className={"queued-message " + variant} data-queued={action.id}>
    {variant === "pane" && <MessagePictures sources={kept.paths.map((path) => keptFileAddress(instance, action.task_id || undefined, path))} />}
    {variant === "pane" && kept.text && <div className="message-bubble queued-bubble" data-tip={reason || state} aria-describedby={"queued-why-" + action.id}>{kept.text}</div>}
    {variant === "queue" && <span className="composer-sent-text">{action.text}</span>}
    {variant === "pane" ? <p className="queued-state sr-only" id={"queued-why-" + action.id}>{reason || state}</p>
      : <p className="queued-line" data-tip={reason}><strong>{state}</strong></p>}
    {isPending && asked !== "withdrawn" && <div className="queued-actions">
      {isWaiting && <button type="button" className="icon-button" aria-label="Send now" disabled={isSending || asked === "now" || !action.can_send_now}
        data-tip={asked === "now" ? "Sending now" : action.can_send_now ? "Send now · type it at the next safe moment" : "Send now · " + (action.send_now_why || "It cannot be typed into its session now.")}
        onClick={() => void act({ kind: "message_now" }, () => setAsked("now"))}><Icon name="arrow-up" /></button>}
      {isWaiting && <button type="button" className="icon-button" aria-label="Edit" data-tip="Edit · take it back into the box" disabled={isSending}
        onClick={() => window.dispatchEvent(new CustomEvent<string>(TAKE_BACK_MESSAGE, { detail: action.id }))}><Icon name="edit" /></button>}
      <button type="button" className="icon-button" aria-label="Cancel" data-tip="Cancel · take it back unsent" disabled={isSending}
        onClick={() => void act({ kind: "message_withdraw", text: action.text }, () => { setAsked("withdrawn"); onDeleted?.(); })}><Icon name="close" /></button>
    </div>}
    {problem && <p className="queued-problem" role="status">{problem}</p>}
  </div>;
}
